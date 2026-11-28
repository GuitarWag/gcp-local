package bigtable

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"encoding/json"

	btapb "cloud.google.com/go/bigtable/admin/apiv2/adminpb"
)

// ConsoleStatus returns the service status surfaced on the /console UI.
func (s *Service) ConsoleStatus() map[string]any {
	return map[string]any{
		"project": s.project,
		"surface": "gRPC: google.bigtable.v2.Bigtable + admin.v2 BigtableTableAdmin",
		"state":   "ready",
		"note":    "ReadRows, MutateRow(s), SampleRowKeys, CheckAndMutateRow and table admin are implemented. ReadModifyWriteRow and query/change-stream RPCs return UNIMPLEMENTED.",
	}
}

// ConsoleTables lists every stored table across all projects and instances.
func (s *Service) ConsoleTables() ([]map[string]any, error) {
	all, err := s.store.List(namespaceTables, "")
	if err != nil {
		return nil, err
	}
	type entry struct {
		meta     tableMeta
		rowCount int
	}
	entries := make([]entry, 0, len(all))
	for _, raw := range all {
		var meta tableMeta
		if err := json.Unmarshal(raw, &meta); err != nil {
			continue
		}
		if meta.Families == nil {
			meta.Families = map[string]*familyMeta{}
		}
		keys, err := s.rowKeys(meta.Name)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry{meta: meta, rowCount: len(keys)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].meta.Name < entries[j].meta.Name })
	tables := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		tables = append(tables, map[string]any{
			"name":     e.meta.Name,
			"id":       lastSegment(e.meta.Name),
			"families": consoleFamilies(e.meta.Families),
			"rows":     e.rowCount,
		})
	}
	return tables, nil
}

// consoleFamilies renders the table's families sorted by creation order.
func consoleFamilies(fams map[string]*familyMeta) []map[string]any {
	type entry struct {
		id string
		fm *familyMeta
	}
	entries := make([]entry, 0, len(fams))
	for id, fm := range fams {
		entries = append(entries, entry{id: id, fm: fm})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].fm.Order < entries[j].fm.Order })
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, map[string]any{
			"id":        e.id,
			"name":      e.fm.Name,
			"order":     e.fm.Order,
			"gcRule":    renderGcRule(e.fm.Gc),
			"valueType": renderValueType(e.fm.Value),
		})
	}
	return out
}

// renderGcRule renders a GcRule as a compact display string, recursing into
// nested union/intersection rules.
func renderGcRule(rule *btapb.GcRule) string {
	switch r := rule.GetRule().(type) {
	case nil:
		return ""
	case *btapb.GcRule_MaxNumVersions:
		return fmt.Sprintf("versions > %d", r.MaxNumVersions)
	case *btapb.GcRule_MaxAge:
		return fmt.Sprintf("age > %s", r.MaxAge.AsDuration())
	case *btapb.GcRule_Union_:
		parts := make([]string, 0, len(r.Union.Rules))
		for _, sub := range r.Union.Rules {
			parts = append(parts, renderGcRule(sub))
		}
		return strings.Join(parts, " or ")
	case *btapb.GcRule_Intersection_:
		parts := make([]string, 0, len(r.Intersection.Rules))
		for _, sub := range r.Intersection.Rules {
			parts = append(parts, renderGcRule(sub))
		}
		return strings.Join(parts, " and ")
	}
	return ""
}

// renderValueType maps a family value type to a display string.
func renderValueType(v *btapb.Type) string {
	switch {
	case v.GetBytesType() != nil:
		return "BYTES"
	case v.GetStringType() != nil:
		return "STRING"
	default:
		return ""
	}
}

// ConsoleRows lists rows of one table in ascending key order.
func (s *Service) ConsoleRows(table string, limit int) ([]map[string]any, error) {
	if _, err := s.loadTable(table); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	keys, err := s.rowKeys(table)
	if err != nil {
		return nil, err
	}
	if len(keys) > limit {
		keys = keys[:limit]
	}
	out := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		r, err := s.loadRow(table, k)
		if err != nil {
			return nil, err
		}
		if r == nil {
			continue
		}
		cellCount := 0
		families := make([]map[string]any, 0, len(r.Families))
		for _, fam := range r.sortedFamilies() {
			cells := make([]map[string]any, 0, len(fam.Cells))
			for _, col := range fam.ColNames {
				for _, c := range fam.Cells[col] {
					cellCount++
					cells = append(cells, consoleCell(col, c))
				}
			}
			families = append(families, map[string]any{
				"name":  fam.Name,
				"order": fam.Order,
				"cells": cells,
			})
		}
		out = append(out, map[string]any{
			"key":       r.Key,
			"size":      r.size(),
			"cellCount": cellCount,
			"families":  families,
		})
	}
	return out, nil
}

// consoleCell renders one cell; the value string appears only for printable
// UTF-8 values.
func consoleCell(qual string, c cell) map[string]any {
	m := map[string]any{
		"qualifier": qual,
		"ts":        c.Ts,
		"size":      len(c.Value),
	}
	if printableUTF8(c.Value) {
		m["value"] = string(c.Value)
	}
	return m
}

// printableUTF8 reports whether v is valid UTF-8 with only printable runes.
// An empty value counts as printable.
func printableUTF8(v []byte) bool {
	if !utf8.Valid(v) {
		return false
	}
	for _, r := range string(v) {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

// lastSegment returns the text after the last "/" in a full resource name.
func lastSegment(name string) string {
	if i := strings.LastIndex(name, "/"); i >= 0 {
		return name[i+1:]
	}
	return name
}
