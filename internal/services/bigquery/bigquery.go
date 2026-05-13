package bigquery

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/GuitarWag/gcp-local/internal/config"
	"github.com/GuitarWag/gcp-local/internal/httpresp"
)

// Rejects hostile ids before they reach SQLite DDL/DML interpolation.
var validIdent = regexp.MustCompile(`^[A-Za-z0-9_\-]+$`)

type dataset struct {
	ID         string    `json:"datasetId"`
	Kind       string    `json:"kind"`
	CreateTime time.Time `json:"creationTime"`
}

type table struct {
	ID         string         `json:"tableId"`
	DatasetID  string         `json:"datasetId"`
	Kind       string         `json:"kind"`
	Schema     tableSchema    `json:"schema"`
	CreateTime time.Time      `json:"creationTime"`
	Raw        map[string]any `json:"-"`
}

type tableSchema struct {
	Fields []schemaField `json:"fields"`
}

type schemaField struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Mode string `json:"mode,omitempty"`
}

type insertAllRequest struct {
	Rows []struct {
		InsertID string         `json:"insertId"`
		JSON     map[string]any `json:"json"`
	} `json:"rows"`
}

type queryRequest struct {
	Query     string `json:"query"`
	UseLegacy *bool  `json:"useLegacySql,omitempty"`
}

type queryResponseSchema struct {
	Fields []schemaField `json:"fields"`
}

type queryResponse struct {
	JobComplete bool                `json:"jobComplete"`
	Schema      queryResponseSchema `json:"schema"`
	Rows        []row               `json:"rows"`
	TotalRows   string              `json:"totalRows"`
}

type row struct {
	F []cell `json:"f"`
}

type cell struct {
	V any `json:"v"`
}

type Service struct {
	project  string
	db       *sql.DB
	mu       sync.Mutex
	datasets map[string]*dataset
	tables   map[string]map[string]*table // datasetID -> tableID -> table
}

func New(_ any, cfg *config.Config) (*Service, error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	return &Service{
		project:  cfg.Project,
		db:       db,
		datasets: map[string]*dataset{},
		tables:   map[string]map[string]*table{},
	}, nil
}

func (s *Service) Name() string { return "bigquery" }

func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("/bigquery/v2/projects/", s.dispatch)
}

func (s *Service) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *Service) dispatch(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/bigquery/v2/projects/")
	parts := strings.Split(rest, "/")
	// parts: project, datasets[, dsId[, tables[, tblId[, action]]]]
	// or:    project, queries  (jobs.query)
	// or:    project, jobs
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	switch parts[1] {
	case "datasets":
		s.handleDatasets(w, r, parts)
	case "queries":
		s.handleQuery(w, r)
	case "jobs":
		s.handleJobs(w, r, parts)
	default:
		http.NotFound(w, r)
	}
}

func (s *Service) handleDatasets(w http.ResponseWriter, r *http.Request, parts []string) {
	switch len(parts) {
	case 2:
		if r.Method == http.MethodPost {
			s.createDataset(w, r)
			return
		}
		if r.Method == http.MethodGet {
			s.listDatasets(w)
			return
		}
	case 3:
		s.datasetItem(w, r, parts[2])
		return
	case 4:
		if parts[3] == "tables" {
			s.tableCollection(w, r, parts[2])
			return
		}
	case 5:
		if parts[3] == "tables" {
			s.tableItem(w, r, parts[2], parts[4])
			return
		}
	case 6:
		if parts[3] == "tables" && parts[5] == "insertAll" {
			s.insertAll(w, r, parts[2], parts[4])
			return
		}
	}
	http.NotFound(w, r)
}

func (s *Service) writeJSON(w http.ResponseWriter, code int, v any) {
	httpresp.JSON(w, code, v)
}

func (s *Service) writeErr(w http.ResponseWriter, code int, msg string) {
	s.writeJSON(w, code, map[string]any{"error": map[string]any{"code": code, "message": msg}})
}

func (s *Service) createDataset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DatasetReference struct {
			DatasetID string `json:"datasetId"`
		} `json:"datasetReference"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	id := body.DatasetReference.DatasetID
	if id == "" {
		s.writeErr(w, http.StatusBadRequest, "datasetId required")
		return
	}
	if !validIdent.MatchString(id) {
		s.writeErr(w, http.StatusBadRequest, "invalid datasetId")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.datasets[id]; ok {
		s.writeErr(w, http.StatusConflict, "dataset exists")
		return
	}
	d := &dataset{ID: id, Kind: "bigquery#dataset", CreateTime: time.Now().UTC()}
	s.datasets[id] = d
	s.tables[id] = map[string]*table{}
	s.writeJSON(w, http.StatusOK, d)
}

func (s *Service) listDatasets(w http.ResponseWriter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := []*dataset{}
	for _, d := range s.datasets {
		items = append(items, d)
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"kind": "bigquery#datasetList", "datasets": items})
}

func (s *Service) datasetItem(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.datasets[id]
	if !ok {
		s.writeErr(w, http.StatusNotFound, "dataset not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.writeJSON(w, http.StatusOK, d)
	case http.MethodDelete:
		delete(s.datasets, id)
		for tableID := range s.tables[id] {
			_, _ = s.db.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %q", id+"__"+tableID))
		}
		delete(s.tables, id)
		w.WriteHeader(http.StatusNoContent)
	default:
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

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
		s.writeJSON(w, http.StatusOK, map[string]any{"kind": "bigquery#tableList", "tables": items})
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
	case "INTEGER", "INT64":
		return "INTEGER"
	case "FLOAT", "FLOAT64", "NUMERIC":
		return "REAL"
	case "BOOL", "BOOLEAN":
		return "INTEGER"
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
	s.writeJSON(w, http.StatusOK, map[string]any{"kind": "bigquery#tableDataInsertAllResponse"})
}

func (s *Service) handleQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body queryRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	// BigQuery refers to tables as `project.dataset.table`. SQLite expects "dataset__table".
	q := translateBQ(body.Query)
	rows, err := s.db.Query(q)
	if err != nil {
		s.writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		s.writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	colTypes, _ := rows.ColumnTypes()
	fieldType := s.fieldTypeIndex()
	schema := queryResponseSchema{}
	for i, c := range cols {
		t := "STRING"
		if bt, ok := fieldType[c]; ok {
			t = bt
		} else if i < len(colTypes) {
			t = sqliteToBQType(colTypes[i].DatabaseTypeName())
		}
		schema.Fields = append(schema.Fields, schemaField{Name: c, Type: t})
	}
	out := queryResponse{JobComplete: true, Schema: schema}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			s.writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		var rr row
		for _, v := range vals {
			rr.F = append(rr.F, cell{V: fmt.Sprintf("%v", v)})
		}
		out.Rows = append(out.Rows, rr)
	}
	out.TotalRows = fmt.Sprintf("%d", len(out.Rows))
	s.writeJSON(w, http.StatusOK, out)
}

func (s *Service) handleJobs(w http.ResponseWriter, r *http.Request, parts []string) {
	// /bigquery/v2/projects/{p}/jobs — also accepts jobs.query via POST with body containing query
	if len(parts) == 2 && r.Method == http.MethodPost {
		// Accept either a jobs.query payload or a full job spec; treat as query.
		var body struct {
			Configuration struct {
				Query struct {
					Query string `json:"query"`
				} `json:"query"`
			} `json:"configuration"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			s.writeErr(w, http.StatusBadRequest, "invalid json")
			return
		}
		if body.Configuration.Query.Query == "" {
			s.writeErr(w, http.StatusBadRequest, "query required")
			return
		}
		// Stub job response
		s.writeJSON(w, http.StatusOK, map[string]any{
			"kind": "bigquery#job",
			"jobReference": map[string]any{
				"projectId": s.project,
				"jobId":     fmt.Sprintf("job-%d", time.Now().UnixNano()),
			},
			"status": map[string]any{"state": "DONE"},
		})
		return
	}
	http.NotFound(w, r)
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
	case "INT64", "INTEGER":
		return "INTEGER"
	case "FLOAT64", "FLOAT", "NUMERIC":
		return "FLOAT"
	case "BOOL", "BOOLEAN":
		return "BOOLEAN"
	case "":
		return "STRING"
	default:
		return strings.ToUpper(t)
	}
}

func sqliteToBQType(t string) string {
	switch strings.ToUpper(t) {
	case "INTEGER", "INT":
		return "INTEGER"
	case "REAL", "FLOAT", "DOUBLE":
		return "FLOAT"
	case "BOOLEAN":
		return "BOOLEAN"
	default:
		return "STRING"
	}
}

// translateBQ rewrites table references from `project.dataset.table` or `dataset.table`
// to the SQLite name `dataset__table`. Coarse but enough for simple tests.
func translateBQ(q string) string {
	// Replace `project.dataset.table` → "dataset__table".
	out := q
	// Strip leading project prefix if present.
	// Look for backticks first.
	out = strings.ReplaceAll(out, "`", "")
	parts := strings.Fields(out)
	for i, p := range parts {
		if strings.Contains(p, ".") && !strings.Contains(p, "(") && !strings.HasSuffix(p, ",") {
			segs := strings.Split(p, ".")
			if len(segs) == 3 {
				parts[i] = fmt.Sprintf("%q", segs[1]+"__"+segs[2])
			} else if len(segs) == 2 {
				parts[i] = fmt.Sprintf("%q", segs[0]+"__"+segs[1])
			}
		}
	}
	return strings.Join(parts, " ")
}
