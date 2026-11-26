package spanner

import (
	"fmt"
	"strings"
)

// translate.go transpiles a Spanner SQL subset to SQLite. Modeled after
// bigquery's translateBQ: identifier guardrails, NULLS-only ordering,
// Spanner DDL → SQLite DDL, and parameter re-binding.

type ddlKind int

const (
	ddlCreateTable ddlKind = iota
	ddlAlterTable
	ddlDropTable
	ddlOther
)

type ddlStatement struct {
	kind      ddlKind
	table     string
	columns   []ddlColumn
	pk        []string
	addColumn []string // ALTER TABLE ADD COLUMN fragments (full SQLite defs)
}

type ddlColumn struct {
	name     string
	spanner  string // e.g. "INT64", "STRING(1024)", "ARRAY<INT64>"
	notNull  bool
	_default string
}

// parseDDL parses the supported DDL subset (CREATE TABLE, ALTER TABLE ADD
// COLUMN, DROP TABLE). Anything else returns ddlOther and is rejected later.
func parseDDL(sql string) (ddlStatement, error) {
	s := strings.TrimSpace(sql)
	upper := strings.ToUpper(s)
	switch {
	case hasPrefixFold(upper, "CREATE TABLE"):
		return parseCreateTable(s)
	case hasPrefixFold(upper, "CREATE DATABASE"), hasPrefixFold(upper, "CREATE INDEX"),
		hasPrefixFold(upper, "CREATE UNIQUE INDEX"), hasPrefixFold(upper, "DROP INDEX"):
		return ddlStatement{kind: ddlOther}, nil
	case hasPrefixFold(upper, "DROP TABLE"):
		name := strings.TrimSpace(s[len("DROP TABLE"):])
		name = strings.TrimSuffix(name, " IF EXISTS")
		return ddlStatement{kind: ddlDropTable, table: unquoteIdent(name)}, nil
	case hasPrefixFold(upper, "ALTER TABLE"):
		return parseAlterTable(s)
	default:
		return ddlStatement{kind: ddlOther}, nil
	}
}

func parseCreateTable(s string) (ddlStatement, error) {
	rest := strings.TrimSpace(s[len("CREATE TABLE"):])
	open := strings.Index(rest, "(")
	if open < 0 {
		return ddlStatement{}, errInvalidArgument("missing column list in CREATE TABLE")
	}
	table := unquoteIdent(strings.TrimSpace(rest[:open]))
	if !validIdent.MatchString(table) {
		return ddlStatement{}, errInvalidArgument("invalid table name %q", table)
	}
	// Find the paren that CLOSES the column list (depth-matched), not the
	// last paren in the string: Spanner appends "PRIMARY KEY (...)" after
	// the column list, and its own parens would confuse LastIndex.
	depth := 0
	closeIdx := -1
	for i := open; i < len(rest); i++ {
		switch rest[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				closeIdx = i
				i = len(rest)
			}
		}
	}
	if closeIdx < 0 {
		return ddlStatement{}, errInvalidArgument("unbalanced parens in CREATE TABLE")
	}
	body := rest[open+1 : closeIdx]
	tail := rest[closeIdx+1:]
	parts := splitTopLevel(body)
	var cols []ddlColumn
	var pk []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		up := strings.ToUpper(part)
		if hasPrefixFold(up, "PRIMARY KEY") || hasPrefixFold(up, "FOREIGN KEY") || hasPrefixFold(up, "CONSTRAINT") {
			continue
		}
		col, err := parseColumnDef(part)
		if err != nil {
			return ddlStatement{}, err
		}
		cols = append(cols, col)
	}
	// Spanner's table-level PRIMARY KEY (col [DESC], ...) after the close paren.
	if idx := strings.Index(strings.ToUpper(tail), "PRIMARY KEY"); idx >= 0 {
		pkPart := strings.TrimSpace(tail[idx+len("PRIMARY KEY"):])
		pkPart = strings.TrimPrefix(strings.TrimSuffix(strings.TrimSpace(pkPart), ")"), "(")
		for _, k := range strings.Split(pkPart, ",") {
			k = strings.TrimSpace(k)
			k = strings.TrimSuffix(k, " DESC")
			k = strings.TrimSuffix(k, " ASC")
			if k = unquoteIdent(k); k != "" {
				pk = append(pk, k)
			}
		}
	}
	return ddlStatement{kind: ddlCreateTable, table: table, columns: cols, pk: pk}, nil
}

func parseColumnDef(part string) (ddlColumn, error) {
	fields := strings.Fields(part)
	if len(fields) == 0 {
		return ddlColumn{}, errInvalidArgument("empty column definition")
	}
	name := unquoteIdent(fields[0])
	if !validIdent.MatchString(name) {
		return ddlColumn{}, errInvalidArgument("invalid column name %q", name)
	}
	// Type may be several tokens (ARRAY<INT64>, STRING(1024) NOT NULL...).
	typ := fields[1]
	if strings.HasPrefix(strings.ToUpper(typ), "ARRAY") {
		// Consume tokens until '>' closes the type.
		i := 2
		for i < len(fields) && !strings.Contains(strings.ToUpper(strings.Join(fields[1:i], " ")), ">") {
			i++
		}
		typ = strings.Join(fields[1:i], " ")
		rest := strings.Join(fields[i:], " ")
		return finishColumn(name, typ, rest)
	}
	if strings.Contains(typ, "(") && !strings.Contains(typ, ")") && len(fields) > 2 {
		// STRING ( 1024 ) split across tokens.
		typ = typ + strings.Join(fields[2:3], "")
		rest := strings.Join(fields[3:], " ")
		return finishColumn(name, typ, rest)
	}
	rest := strings.Join(fields[2:], " ")
	return finishColumn(name, typ, rest)
}

func finishColumn(name, typ, rest string) (ddlColumn, error) {
	up := strings.ToUpper(rest)
	col := ddlColumn{name: name, spanner: typ}
	if strings.Contains(up, "NOT NULL") {
		col.notNull = true
	}
	if i := strings.Index(up, "DEFAULT"); i >= 0 {
		col._default = strings.TrimSpace(rest[i+len("DEFAULT"):])
	}
	return col, nil
}

func parseAlterTable(s string) (ddlStatement, error) {
	rest := strings.TrimSpace(s[len("ALTER TABLE"):])
	// ALTER TABLE <t> ADD [COLUMN] <def> | DROP [COLUMN] <name> | SET ...
	nameEnd := strings.IndexAny(rest, " \t")
	if nameEnd < 0 {
		return ddlStatement{}, errInvalidArgument("malformed ALTER TABLE")
	}
	table := unquoteIdent(rest[:nameEnd])
	rest = strings.TrimSpace(rest[nameEnd:])
	switch {
	case hasPrefixFold(rest, "ADD"):
		def := strings.TrimSpace(rest[3:])
		def = strings.TrimPrefix(def, "COLUMN")
		def = strings.TrimSpace(def)
		// Convert the Spanner type into the SQLite definition here so
		// execDDL can append it verbatim.
		col, err := parseColumnDef(def)
		if err != nil {
			return ddlStatement{}, err
		}
		frag := quoteIdent(col.name) + " " + sqliteTypeFor(col)
		return ddlStatement{kind: ddlAlterTable, table: table, addColumn: []string{frag}}, nil
	case hasPrefixFold(rest, "DROP"):
		return ddlStatement{}, errUnimplemented("ALTER TABLE DROP COLUMN is not supported by the emulator")
	case hasPrefixFold(strings.TrimSpace(rest), "SET"):
		// "ALTER TABLE t SET OPTIONS (...)" — accepted as a no-op.
		return ddlStatement{kind: ddlOther, table: table}, nil
	default:
		return ddlStatement{}, errInvalidArgument("unsupported ALTER TABLE clause")
	}
}

// sqliteColumns renders the CREATE TABLE column fragments and the PK list.
func sqliteColumns(stmt ddlStatement) ([]string, []string) {
	var cols []string
	for _, c := range stmt.columns {
		def := quoteIdent(c.name) + " " + sqliteTypeFor(c)
		if c.notNull {
			def += " NOT NULL"
		}
		if c._default != "" {
			def += " DEFAULT " + c._default
		}
		cols = append(cols, def)
	}
	pk := make([]string, 0, len(stmt.pk))
	for _, k := range stmt.pk {
		pk = append(pk, quoteIdent(k))
	}
	return cols, pk
}

func sqliteTypeFor(c ddlColumn) string {
	up := strings.ToUpper(c.spanner)
	switch {
	case strings.HasPrefix(up, "INT64"):
		return "INTEGER"
	case strings.HasPrefix(up, "FLOAT64"), strings.HasPrefix(up, "FLOAT32"):
		return "REAL"
	case strings.HasPrefix(up, "BOOL"):
		return "INTEGER"
	default:
		return "TEXT"
	}
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func unquoteIdent(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimSuffix(strings.TrimPrefix(name, "`"), "`")
	name = strings.TrimSuffix(strings.TrimPrefix(name, `"`), `"`)
	return name
}

// splitTopLevel splits on commas not nested in parens or angle brackets.
func splitTopLevel(s string) []string {
	var parts []string
	depth := 0
	start := 0
	for i, r := range s {
		switch r {
		case '(', '<':
			depth++
		case ')', '>':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])
	return parts
}

// translateQuery rewrites a Spanner query into SQLite SQL. Spanner's DML and
// SELECT surface overlaps SQLite heavily; the main rewrites are:
//   - @param → SQLite's also-@param (no change needed; we bind by name)
//   - STRUCT params and ARRAY params are passed as JSON (not supported here)
//   - SAFE functions and UNNEST remain unsupported → InvalidArgument
func translateQuery(q string) (string, error) {
	up := strings.ToUpper(strings.TrimSpace(q))
	for _, banned := range []string{"UNNEST", "SAFE.", "ARRAY_AGG", "STRUCT<"} {
		if strings.Contains(up, banned) {
			return "", errInvalidArgument("unsupported Spanner SQL feature %q in emulator", banned)
		}
	}
	return q, nil
}

// classifySQL decides whether a statement is DDL, DML, or a query.
func classifySQL(sql string) string {
	up := strings.ToUpper(strings.TrimSpace(sql))
	switch {
	case strings.HasPrefix(up, "CREATE"), strings.HasPrefix(up, "ALTER"), strings.HasPrefix(up, "DROP"):
		return "ddl"
	case strings.HasPrefix(up, "INSERT"), strings.HasPrefix(up, "UPDATE"), strings.HasPrefix(up, "DELETE"):
		return "dml"
	case strings.HasPrefix(up, "SELECT"), strings.HasPrefix(up, "WITH"):
		return "query"
	default:
		return "query"
	}
}

var _ = fmt.Sprintf // keep fmt imported for future use in this file
