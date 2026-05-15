package bigquery

import (
	"context"
	"sort"
)

// ConsoleDatasets returns one row per dataset.
func (s *Service) ConsoleDatasets() ([]map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]map[string]any, 0, len(s.datasets))
	for id, d := range s.datasets {
		out = append(out, map[string]any{
			"id":           id,
			"creationTime": d.CreateTime,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := out[i]["id"].(string)
		b, _ := out[j]["id"].(string)
		return a < b
	})
	return out, nil
}

// ConsoleTables returns the tables inside a dataset.
func (s *Service) ConsoleTables(datasetID string) ([]map[string]any, error) {
	s.mu.Lock()
	tbls := s.tables[datasetID]
	out := make([]map[string]any, 0, len(tbls))
	for id, t := range tbls {
		out = append(out, map[string]any{
			"id":           id,
			"datasetId":    t.DatasetID,
			"creationTime": t.CreateTime,
			"fields":       len(t.Schema.Fields),
		})
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		a, _ := out[i]["id"].(string)
		b, _ := out[j]["id"].(string)
		return a < b
	})
	return out, nil
}

// ConsoleQuery executes a small read-only SELECT against the SQLite
// backend and returns the rows. The caller is the local console; we
// don't enforce timeouts beyond the engine's.
func (s *Service) ConsoleQuery(query string) (map[string]any, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := map[string]any{}
		for i, c := range cols {
			row[c] = vals[i]
		}
		out = append(out, row)
		if len(out) >= 200 {
			break
		}
	}
	return map[string]any{"columns": cols, "rows": out}, rows.Err()
}
