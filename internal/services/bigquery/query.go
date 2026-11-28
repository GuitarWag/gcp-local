package bigquery

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

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
		t := bqTypeString
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
			kindKey: "bigquery#job",
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
