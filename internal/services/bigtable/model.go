// Package bigtable implements the google.bigtable.v2.Bigtable data plane and
// the google.bigtable.admin.v2.BigtableTableAdmin table lifecycle surface on
// top of the shared state.Store. Wire semantics (cell chunks, filters,
// timestamps, SampleRowKeys) follow the real service as mirrored by the
// cloud.google.com/go/bigtable bttest fake.
package bigtable

import (
	"sort"
	"time"

	btapb "cloud.google.com/go/bigtable/admin/apiv2/adminpb"
	btpb "cloud.google.com/go/bigtable/apiv2/bigtablepb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func statusErrorf(c codes.Code, format string, args ...any) error {
	return status.Errorf(c, format, args...)
}

// cell is one stored value at one timestamp in one column. Exported fields
// are required for state.Store JSON round-tripping.
type cell struct {
	Ts     int64    `json:"ts"`
	Value  []byte   `json:"value,omitempty"`
	Labels []string `json:"labels,omitempty"`
}

// family mirrors a column family's data within a row: cells keyed by column
// qualifier, with a lexicographically sorted qualifier index for output order.
type family struct {
	Name     string            `json:"name"`
	Order    uint64            `json:"order"`
	ColNames []string          `json:"colNames,omitempty"`
	Cells    map[string][]cell `json:"cells"` // descending timestamp order
}

// row is a stored row. Served via state.Store serialised per row.
type row struct {
	Key      string             `json:"key"`
	Families map[string]*family `json:"families"`
}

// size returns the total size of all cell values in the row.
func (r *row) size() int {
	size := 0
	for _, f := range r.Families {
		for _, cs := range f.Cells {
			for _, c := range cs {
				size += len(c.Value)
			}
		}
	}
	return size
}

// tableMeta is the serialised table record: families + creation order.
type tableMeta struct {
	Name     string                 `json:"name"`
	Families map[string]*familyMeta `json:"families"`
	Counter  uint64                 `json:"counter"` // family creation order
}

type familyMeta struct {
	Name  string        `json:"name"`
	Order uint64        `json:"order"`
	Gc    *btapb.GcRule `json:"gc,omitempty"`
	Value *btapb.Type   `json:"value,omitempty"`
}

// table is the in-memory engine handle: metadata cached, rows on demand
// through the store.
type table struct {
	meta tableMeta
}

// isEmpty reports whether the row holds no cells at all.
func (r *row) isEmpty() bool {
	for _, f := range r.Families {
		for _, cs := range f.Cells {
			if len(cs) > 0 {
				return false
			}
		}
	}
	return true
}

// copy deep-copies families/cells (values aliased) for filter evaluation.
func (r *row) copy() *row {
	nr := &row{Key: r.Key, Families: make(map[string]*family)}
	for _, f := range r.Families {
		nf := &family{Name: f.Name, Order: f.Order, Cells: make(map[string][]cell)}
		nf.ColNames = append(nf.ColNames, f.ColNames...)
		for col, cs := range f.Cells {
			nf.Cells[col] = append([]cell(nil), cs...)
		}
		nr.Families[f.Name] = nf
	}
	return nr
}

// CellsByColumn registers the qualifier in the sorted index when new.
func (f *family) CellsByColumn(name string) []cell {
	if _, ok := f.Cells[name]; !ok {
		f.ColNames = append(f.ColNames, name)
		sort.Strings(f.ColNames)
	}
	return f.Cells[name]
}

// sortedFamilies returns families in creation order.
func (r *row) sortedFamilies() []*family {
	out := make([]*family, 0, len(r.Families))
	for _, f := range r.Families {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out
}

// getOrCreateFamily fetches or creates a family in a row.
func (r *row) getOrCreateFamily(name string, order uint64) *family {
	if _, ok := r.Families[name]; !ok {
		r.Families[name] = &family{Name: name, Order: order, Cells: make(map[string][]cell)}
	}
	return r.Families[name]
}

// applyMutations applies a mutation sequence to a row in place.
func applyMutations(tbl *table, r *row, muts []*btpb.Mutation) error {
	if len(r.Key) == 0 {
		return errInvalidArgument("row keys must be non-empty")
	}
	for _, mut := range muts {
		switch mut := mut.Mutation.(type) {
		case *btpb.Mutation_SetCell_:
			set := mut.SetCell
			fm, ok := tbl.meta.Families[set.FamilyName]
			if !ok {
				return errInvalidArgument("unknown family %q", set.FamilyName)
			}
			ts := set.TimestampMicros
			if ts == -1 {
				ts = newTimestamp()
			}
			if !validTimestamp(ts) {
				return errInvalidArgument("invalid timestamp %d", ts)
			}
			f := r.getOrCreateFamily(set.FamilyName, fm.Order)
			f.Cells[string(set.ColumnQualifier)] = appendOrReplaceCell(
				f.CellsByColumn(string(set.ColumnQualifier)), cell{Ts: ts, Value: set.Value})
		case *btpb.Mutation_DeleteFromColumn_:
			del := mut.DeleteFromColumn
			if _, ok := tbl.meta.Families[del.FamilyName]; !ok {
				return errInvalidArgument("unknown family %q", del.FamilyName)
			}
			f, ok := r.Families[del.FamilyName]
			if !ok {
				continue
			}
			col := string(del.ColumnQualifier)
			cs := f.Cells[col]
			if del.TimeRange != nil {
				tr := del.TimeRange
				if !validTimestamp(tr.StartTimestampMicros) {
					return errInvalidArgument("invalid timestamp %d", tr.StartTimestampMicros)
				}
				if !validTimestamp(tr.EndTimestampMicros) && tr.EndTimestampMicros != 0 {
					return errInvalidArgument("invalid timestamp %d", tr.EndTimestampMicros)
				}
				if tr.StartTimestampMicros >= tr.EndTimestampMicros && tr.EndTimestampMicros != 0 {
					return errInvalidArgument("inverted or invalid timestamp range [%d, %d]",
						tr.StartTimestampMicros, tr.EndTimestampMicros)
				}
				cs = deleteTimeRange(cs, tr.StartTimestampMicros, tr.EndTimestampMicros)
			} else {
				cs = nil
			}
			if len(cs) == 0 {
				delete(f.Cells, col)
				f.ColNames = removeSorted(f.ColNames, col)
				if len(f.Cells) == 0 {
					delete(r.Families, del.FamilyName)
				}
			} else {
				f.Cells[col] = cs
			}
		case *btpb.Mutation_DeleteFromFamily_:
			delete(r.Families, mut.DeleteFromFamily.FamilyName)
		case *btpb.Mutation_DeleteFromRow_:
			r.Families = make(map[string]*family)
		default:
			return errInvalidArgument("unsupported mutation type %T", mut)
		}
	}
	return nil
}

// deleteTimeRange removes cells in [start, end) from a descending list.
func deleteTimeRange(cs []cell, start, end int64) []cell {
	// Cells are in descending timestamp order; find the half-open range.
	si, ei := 0, len(cs)
	if start > 0 {
		ei = sort.Search(len(cs), func(i int) bool { return cs[i].Ts < start })
	}
	if end > 0 {
		si = sort.Search(len(cs), func(i int) bool { return cs[i].Ts < end })
	}
	if si < ei {
		cs = append(cs[:si], cs[ei:]...)
	}
	return cs
}

func removeSorted(names []string, name string) []string {
	i := sort.SearchStrings(names, name)
	if i < len(names) && names[i] == name {
		names = append(names[:i], names[i+1:]...)
	}
	return names
}

// appendOrReplaceCell replaces a cell at the same timestamp or inserts a new
// one, keeping descending timestamp order.
func appendOrReplaceCell(cs []cell, newCell cell) []cell {
	replaced := false
	for i, c := range cs {
		if c.Ts == newCell.Ts {
			cs[i] = newCell
			replaced = true
			break
		}
	}
	if !replaced {
		cs = append(cs, newCell)
	}
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].Ts > cs[j].Ts })
	return cs
}

// validTimestamp enforces millisecond granularity in the valid range.
func validTimestamp(ts int64) bool {
	if ts < 0 {
		return false
	}
	return ts%1000 == 0
}

// newTimestamp returns the current time truncated to milliseconds, in
// microseconds since epoch.
func newTimestamp() int64 {
	return microsNow() / 1000 * 1000
}

func microsNow() int64 {
	return time.Now().UnixNano() / 1e3
}
