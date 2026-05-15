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

	_ "modernc.org/sqlite" // sqlite driver registered as "sqlite" with database/sql

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
	q := s.translateBQ(body.Query)
	rows, err := s.db.Query(q)
	if err != nil {
		s.writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	defer func() { _ = rows.Close() }()
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

// translateBQ rewrites BigQuery-flavoured SQL into SQLite-flavoured SQL.
// Specifically:
//   - Table references `project.dataset.table` or `dataset.table` are
//     rewritten to "dataset__table" when `dataset` is a known dataset id.
//   - Backticks around references are stripped.
//   - A small set of BigQuery scalar functions is mapped to SQLite
//     equivalents (see translateBQFunctions).
//
// Column-qualified references like `t.col` (where `t` is an alias) are
// left untouched, as are bare aggregate calls like `COUNT(*)`, `SUM(x)`,
// `GROUP BY`, `HAVING`, `ORDER BY`, and `JOIN ... ON`.
func (s *Service) translateBQ(q string) string {
	known := s.knownDatasets()
	q = translateBQFunctions(q)
	q = translateBQTables(q, known)
	return q
}

// knownDatasets returns the set of dataset IDs registered on the service,
// used to disambiguate `dataset.table` from `alias.column` in queries.
func (s *Service) knownDatasets() map[string]struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]struct{}, len(s.datasets))
	for id := range s.datasets {
		out[id] = struct{}{}
	}
	return out
}

// bqRefToken matches a dotted identifier sequence, optionally wrapped in
// backticks. We intentionally restrict to [A-Za-z_][A-Za-z0-9_-]* segments
// so we don't accidentally rewrite numeric literals like 1.5 or 3.14e10.
var bqRefToken = regexp.MustCompile("`?([A-Za-z_][A-Za-z0-9_-]*)\\.([A-Za-z_][A-Za-z0-9_-]*)(?:\\.([A-Za-z_][A-Za-z0-9_-]*))?`?")

// translateBQTables rewrites `dataset.table` or `project.dataset.table` to
// the SQLite physical table name "dataset__table", but only when the
// dataset segment is in `known`. Other dotted references (alias.column,
// numeric literals) are left as-is.
func translateBQTables(q string, known map[string]struct{}) string {
	return bqRefToken.ReplaceAllStringFunc(q, func(m string) string {
		sub := bqRefToken.FindStringSubmatch(m)
		// sub[1]=first, sub[2]=second, sub[3]=third (or "")
		first, second, third := sub[1], sub[2], sub[3]
		hadBackticks := strings.HasPrefix(m, "`") || strings.HasSuffix(m, "`")
		if third != "" {
			// project.dataset.table — keep if middle segment is known dataset.
			if _, ok := known[second]; ok {
				return fmt.Sprintf("%q", second+"__"+third)
			}
			// Unknown: strip backticks but don't rewrite.
			if hadBackticks {
				return first + "." + second + "." + third
			}
			return m
		}
		// dataset.table
		if _, ok := known[first]; ok {
			return fmt.Sprintf("%q", first+"__"+second)
		}
		if hadBackticks {
			return first + "." + second
		}
		return m
	})
}

// translateBQFunctions rewrites a small set of BigQuery scalar functions
// to SQLite-compatible forms. The set is deliberately small; complex
// surfaces (windows, STRUCT, ARRAY, UNNEST, PARTITION BY, WITH RECURSIVE,
// and BigQuery-specific date arithmetic) are NOT translated.
//
// Divergences from BigQuery:
//   - SAFE_CAST is translated to CAST. BigQuery returns NULL on cast
//     failure; SQLite raises (or coerces). We accept the looser semantics
//     for v1.
//   - CONCAT is translated to `||` concatenation. SQLite's `||` returns
//     NULL if any operand is NULL, which matches BigQuery's CONCAT.
func translateBQFunctions(q string) string {
	q = rewriteCurrentTimestamp(q)
	q = rewriteSafeCast(q)
	q = rewriteConcat(q)
	return q
}

var (
	reCurrentTimestamp = regexp.MustCompile(`(?i)CURRENT_TIMESTAMP\s*\(\s*\)`)
	reSafeCastOpen     = regexp.MustCompile(`(?i)SAFE_CAST\s*\(`)
	reConcatOpen       = regexp.MustCompile(`(?i)\bCONCAT\s*\(`)
)

func rewriteCurrentTimestamp(q string) string {
	return reCurrentTimestamp.ReplaceAllString(q, "CURRENT_TIMESTAMP")
}

func rewriteSafeCast(q string) string {
	// SAFE_CAST(x AS T) → CAST(x AS T). The inner expression is left
	// untouched; SQLite parses CAST(x AS T) the same way.
	return reSafeCastOpen.ReplaceAllString(q, "CAST(")
}

// rewriteConcat replaces CONCAT(a, b, c, ...) with (a || b || c || ...).
// It walks the string, finds each CONCAT( occurrence, scans forward to the
// matching close paren respecting nesting and single-quoted string
// literals, then splits the inner args on top-level commas.
func rewriteConcat(q string) string {
	for {
		loc := reConcatOpen.FindStringIndex(q)
		if loc == nil {
			return q
		}
		openParen := loc[1] - 1 // index of '('
		end, args, ok := splitTopLevelArgs(q, openParen)
		if !ok {
			// Unbalanced — bail out and let SQLite report the error.
			return q
		}
		if len(args) < 2 {
			// CONCAT(x) is just x; CONCAT() is illegal in BigQuery too.
			// Wrap as (x) so downstream tokenisation stays sane.
			q = q[:loc[0]] + "(" + strings.Join(args, " ") + ")" + q[end+1:]
			continue
		}
		joined := "(" + strings.Join(args, " || ") + ")"
		q = q[:loc[0]] + joined + q[end+1:]
	}
}

// splitTopLevelArgs assumes s[open] == '('. It returns the index of the
// matching ')', the argument strings (trimmed), and ok=true on success.
func splitTopLevelArgs(s string, open int) (int, []string, bool) {
	depth := 0
	inSingle := false
	inDouble := false
	inBacktick := false
	start := open + 1
	var args []string
	for i := open; i < len(s); i++ {
		c := s[i]
		switch {
		case inSingle:
			if c == '\'' {
				inSingle = false
			}
		case inDouble:
			if c == '"' {
				inDouble = false
			}
		case inBacktick:
			if c == '`' {
				inBacktick = false
			}
		default:
			switch c {
			case '\'':
				inSingle = true
			case '"':
				inDouble = true
			case '`':
				inBacktick = true
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					args = append(args, strings.TrimSpace(s[start:i]))
					return i, args, true
				}
			case ',':
				if depth == 1 {
					args = append(args, strings.TrimSpace(s[start:i]))
					start = i + 1
				}
			}
		}
	}
	return 0, nil, false
}
