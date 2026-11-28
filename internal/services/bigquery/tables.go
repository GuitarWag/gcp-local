package bigquery

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	_ "modernc.org/sqlite" // sqlite driver registered as "sqlite" with database/sql
)

func (s *Service) tableCollection(w http.ResponseWriter, r *http.Request, dsID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.datasets[dsID]; !ok {
		s.writeErr(w, http.StatusNotFound, "dataset not found")
		return
	}
	switch r.Method {
	case http.MethodPost:
		var body struct {
			TableReference struct {
				TableID string `json:"tableId"`
			} `json:"tableReference"`
			Schema tableSchema `json:"schema"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			s.writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		id := body.TableReference.TableID
		if id == "" {
			s.writeErr(w, http.StatusBadRequest, "tableId required")
			return
		}
		if !validIdent.MatchString(id) {
			s.writeErr(w, http.StatusBadRequest, "invalid tableId")
			return
		}
		for _, f := range body.Schema.Fields {
			if !validIdent.MatchString(f.Name) {
				s.writeErr(w, http.StatusBadRequest, "invalid schema field name")
				return
			}
		}
		if _, ok := s.tables[dsID][id]; ok {
			s.writeErr(w, http.StatusConflict, "table exists")
			return
		}
		if err := s.createSQLTable(dsID, id, body.Schema); err != nil {
			s.writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		t := &table{
			ID:         id,
			DatasetID:  dsID,
			Kind:       "bigquery#table",
			Schema:     body.Schema,
			CreateTime: time.Now().UTC(),
		}
		s.tables[dsID][id] = t
		s.writeJSON(w, http.StatusOK, t)
	case http.MethodGet:
		items := []*table{}
		for _, t := range s.tables[dsID] {
			items = append(items, t)
		}
		s.writeJSON(w, http.StatusOK, map[string]any{kindKey: "bigquery#tableList", "tables": items})
	default:
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Service) tableItem(w http.ResponseWriter, r *http.Request, dsID, tblID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.datasets[dsID]; !ok {
		s.writeErr(w, http.StatusNotFound, "dataset not found")
		return
	}
	t, ok := s.tables[dsID][tblID]
	if !ok {
		s.writeErr(w, http.StatusNotFound, "table not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.writeJSON(w, http.StatusOK, t)
	case http.MethodDelete:
		delete(s.tables[dsID], tblID)
		_, _ = s.db.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %q", dsID+"__"+tblID))
		w.WriteHeader(http.StatusNoContent)
	default:
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Service) createSQLTable(dsID, tblID string, schema tableSchema) error {
	if len(schema.Fields) == 0 {
		return errors.New("schema fields required")
	}
	cols := []string{}
	for _, f := range schema.Fields {
		cols = append(cols, fmt.Sprintf("%q %s", f.Name, sqlType(f.Type)))
	}
	stmt := fmt.Sprintf("CREATE TABLE %q (%s)", dsID+"__"+tblID, strings.Join(cols, ", "))
	_, err := s.db.Exec(stmt)
	return err
}

func sqlType(t string) string {
	switch strings.ToUpper(t) {
	case bqTypeInteger, "INT64":
		return bqTypeInteger
	case bqTypeFloat, "FLOAT64", "NUMERIC":
		return "REAL"
	case "BOOL", bqTypeBoolean:
		return bqTypeInteger
	default:
		return "TEXT"
	}
}

func (s *Service) insertAll(w http.ResponseWriter, r *http.Request, dsID, tblID string) {
	if r.Method != http.MethodPost {
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s.mu.Lock()
	t, ok := s.tables[dsID][tblID]
	s.mu.Unlock()
	if !ok {
		s.writeErr(w, http.StatusNotFound, "table not found")
		return
	}
	var body insertAllRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	cols := []string{}
	for _, f := range t.Schema.Fields {
		cols = append(cols, fmt.Sprintf("%q", f.Name))
	}
	placeholders := strings.Repeat("?,", len(t.Schema.Fields))
	placeholders = strings.TrimSuffix(placeholders, ",")
	stmt := fmt.Sprintf("INSERT INTO %q (%s) VALUES (%s)", dsID+"__"+tblID, strings.Join(cols, ", "), placeholders)
	for _, r := range body.Rows {
		args := []any{}
		for _, f := range t.Schema.Fields {
			args = append(args, r.JSON[f.Name])
		}
		if _, err := s.db.Exec(stmt, args...); err != nil {
			s.writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	s.writeJSON(w, http.StatusOK, map[string]any{kindKey: "bigquery#tableDataInsertAllResponse"})
}

// On column-name collisions across tables, first observed type wins.
func (s *Service) fieldTypeIndex() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for _, ts := range s.tables {
		for _, t := range ts {
			for _, f := range t.Schema.Fields {
				if _, ok := out[f.Name]; ok {
					continue
				}
				out[f.Name] = normaliseBQType(f.Type)
			}
		}
	}
	return out
}

func normaliseBQType(t string) string {
	switch strings.ToUpper(t) {
	case "INT64", bqTypeInteger:
		return bqTypeInteger
	case "FLOAT64", bqTypeFloat, "NUMERIC":
		return bqTypeFloat
	case "BOOL", bqTypeBoolean:
		return bqTypeBoolean
	case "":
		return bqTypeString
	default:
		return strings.ToUpper(t)
	}
}

func sqliteToBQType(t string) string {
	switch strings.ToUpper(t) {
	case bqTypeInteger, "INT":
		return bqTypeInteger
	case "REAL", bqTypeFloat, "DOUBLE":
		return bqTypeFloat
	case bqTypeBoolean:
		return bqTypeBoolean
	default:
		return bqTypeString
	}
}
