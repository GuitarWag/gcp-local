package bigtable

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/GuitarWag/gcp-local/internal/state"
)

const (
	namespaceTables = "bigtable/tables"
	namespaceRows   = "bigtable/rows"
)

// store access ---------------------------------------------------------------

// loadTable reads table metadata from the store.
func (s *Service) loadTable(fullName string) (*table, error) {
	raw, err := s.store.Get(namespaceTables, fullName)
	if err != nil {
		return nil, errNotFound("table %q not found", fullName)
	}
	var meta tableMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil, fmt.Errorf("bigtable: corrupt table record: %w", err)
	}
	if meta.Families == nil {
		meta.Families = map[string]*familyMeta{}
	}
	return &table{meta: meta}, nil
}

// saveTable writes table metadata back.
func (s *Service) saveTable(t *table) error {
	raw, err := json.Marshal(t.meta)
	if err != nil {
		return err
	}
	return s.store.Put(namespaceTables, t.meta.Name, raw)
}

// deleteTable removes table metadata and every row record.
func (s *Service) deleteTable(fullName string) error {
	if _, err := s.store.Get(namespaceTables, fullName); err != nil {
		return errNotFound("table %q not found", fullName)
	}
	rows, err := s.store.List(namespaceRows, fullName+"\x00")
	if err != nil {
		return err
	}
	for k := range rows {
		if err := s.store.Delete(namespaceRows, k); err != nil {
			return err
		}
	}
	return s.store.Delete(namespaceTables, fullName)
}

// listTables returns metadata for all tables of one instance prefix.
func (s *Service) listTables(instancePrefix string) ([]*table, error) {
	all, err := s.store.List(namespaceTables, instancePrefix+"/tables/")
	if err != nil {
		return nil, err
	}
	out := make([]*table, 0, len(all))
	for _, raw := range all {
		var meta tableMeta
		if err := json.Unmarshal(raw, &meta); err != nil {
			continue
		}
		if meta.Families == nil {
			meta.Families = map[string]*familyMeta{}
		}
		out = append(out, &table{meta: meta})
	}
	return out, nil
}

// loadRow reads one row (nil when absent).
func (s *Service) loadRow(fullTable, key string) (*row, error) {
	raw, err := s.store.Get(namespaceRows, rowKey(fullTable, key))
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil, nil // absent row is a normal outcome
		}
		return nil, err
	}
	var r row
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("bigtable: corrupt row record: %w", err)
	}
	if r.Families == nil {
		r.Families = map[string]*family{}
	}
	return &r, nil
}

// saveRow persists a row record. Rows that end empty are deleted from the
// store so scans and SampleRowKeys stay accurate.
func (s *Service) saveRow(fullTable string, r *row) error {
	k := rowKey(fullTable, r.Key)
	if r.isEmpty() {
		if err := s.store.Delete(namespaceRows, k); err != nil && !errors.Is(err, state.ErrNotFound) {
			return err
		}
		return nil
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return s.store.Put(namespaceRows, k, raw)
}

// rowKey namespaces a row under its table.
func rowKey(fullTable, key string) string {
	return fullTable + "\x00" + key
}

// rowKeys lists all stored row keys of a table, sorted ascending.
func (s *Service) rowKeys(fullTable string) ([]string, error) {
	all, err := s.store.List(namespaceRows, fullTable+"\x00")
	if err != nil {
		return nil, err
	}
	prefixLen := len(fullTable) + 1
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k[prefixLen:])
	}
	sortStrings(keys)
	return keys, nil
}

// forEachRow loads every stored row of the table in ascending key order,
// filtered by match.
func (s *Service) forEachRow(fullTable string, match func(string) bool, fn func(*row) error) error {
	return s.forEachRowDir(fullTable, match, false, fn)
}

// forEachRowDesc loads every stored row of the table in descending key
// order, filtered by match (ReadRows with Reversed set).
func (s *Service) forEachRowDesc(fullTable string, match func(string) bool, fn func(*row) error) error {
	return s.forEachRowDir(fullTable, match, true, fn)
}

// forEachRowDir is the shared ascending/descending row iterator.
func (s *Service) forEachRowDir(fullTable string, match func(string) bool, desc bool, fn func(*row) error) error {
	keys, err := s.rowKeys(fullTable)
	if err != nil {
		return err
	}
	if desc {
		for i, j := 0, len(keys)-1; i < j; i, j = i+1, j-1 {
			keys[i], keys[j] = keys[j], keys[i]
		}
	}
	for _, k := range keys {
		if !match(k) {
			continue
		}
		r, err := s.loadRow(fullTable, k)
		if err != nil {
			return err
		}
		if r == nil {
			continue
		}
		if err := fn(r); err != nil {
			return err
		}
	}
	return nil
}

func sortStrings(ss []string) { sort.Slice(ss, func(i, j int) bool { return ss[i] < ss[j] }) }
