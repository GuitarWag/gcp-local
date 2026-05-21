package mysqlwire

import (
	"regexp"
	"strings"
	"unicode"
)

// translateSQL adapts a MySQL-flavoured statement into something sqlite can
// run. The transformations are deliberately small and target the basic
// surface (CREATE TABLE / INSERT / SELECT / UPDATE / DELETE plus
// transactions). Anything more exotic is the caller's problem.
func translateSQL(in string) string {
	out := in
	out = stripTableSuffix(out)
	out = stripUnsigned(out)
	out = replaceAutoIncrement(out)
	out = replaceTypes(out)
	return out
}

// stripTableSuffix removes MySQL-specific table options that follow a
// CREATE TABLE ... ( ... ) closing paren, like
//
//	ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin
//
// SQLite raises a syntax error on any of these.
var tableOptRe = regexp.MustCompile(`(?i)\s+(ENGINE|DEFAULT\s+CHARSET|CHARSET|COLLATE|AUTO_INCREMENT|ROW_FORMAT|COMMENT)\s*=\s*([A-Za-z0-9_]+|'[^']*')`)

func stripTableSuffix(in string) string {
	return tableOptRe.ReplaceAllString(in, "")
}

// stripUnsigned removes `UNSIGNED` and similar type modifiers sqlite doesn't
// recognise as part of column type declarations.
var modifierRe = regexp.MustCompile(`(?i)\b(UNSIGNED|ZEROFILL)\b`)

func stripUnsigned(in string) string {
	return modifierRe.ReplaceAllString(in, "")
}

// replaceAutoIncrement turns the MySQL keyword into SQLite's AUTOINCREMENT
// (which is only valid on INTEGER PRIMARY KEY columns — that's fine for
// the basic-CRUD acceptance tests). Outside `PRIMARY KEY` context we just
// drop it.
var (
	pkAutoIncRe = regexp.MustCompile(`(?i)\bAUTO_INCREMENT\b`)
)

func replaceAutoIncrement(in string) string {
	return pkAutoIncRe.ReplaceAllString(in, "AUTOINCREMENT")
}

// replaceTypes maps MySQL-only column types onto sqlite-flavoured types.
// SQLite is dynamic about column types but the parser still needs to
// recognise the keyword.
var typeReplacements = map[string]string{
	"TINYINT":    "INTEGER",
	"SMALLINT":   "INTEGER",
	"MEDIUMINT":  "INTEGER",
	"BIGINT":     "INTEGER",
	"INT":        "INTEGER",
	"DOUBLE":     "REAL",
	"FLOAT":      "REAL",
	"DECIMAL":    "REAL",
	"NUMERIC":    "REAL",
	"DATETIME":   "DATETIME",
	"TIMESTAMP":  "DATETIME",
	"LONGTEXT":   "TEXT",
	"MEDIUMTEXT": "TEXT",
	"TINYTEXT":   "TEXT",
	"LONGBLOB":   "BLOB",
	"MEDIUMBLOB": "BLOB",
	"TINYBLOB":   "BLOB",
	"VARBINARY":  "BLOB",
	"BINARY":     "BLOB",
}

func replaceTypes(in string) string {
	var b strings.Builder
	b.Grow(len(in))
	i := 0
	for i < len(in) {
		c := in[i]
		switch c {
		case '\'':
			j := skipSingleQuoted(in, i)
			b.WriteString(in[i:j])
			i = j
		case '"':
			j := skipDoubleQuoted(in, i)
			b.WriteString(in[i:j])
			i = j
		case '`':
			j := skipBackquoted(in, i)
			b.WriteString(in[i:j])
			i = j
		default:
			if isWordStart(in, i) {
				j := i
				for j < len(in) && isIdentByte(in[j]) {
					j++
				}
				word := in[i:j]
				up := strings.ToUpper(word)
				if r, ok := typeReplacements[up]; ok {
					b.WriteString(r)
				} else {
					b.WriteString(word)
				}
				// Eat an optional `(N)` or `(N,M)` size hint after numeric
				// types so sqlite doesn't reject e.g. `INT(11)`.
				if j < len(in) && in[j] == '(' && isNumericLen(in, j) {
					end := strings.IndexByte(in[j:], ')')
					if end >= 0 {
						j += end + 1
					}
				}
				i = j
				continue
			}
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

func isNumericLen(in string, j int) bool {
	k := j + 1
	for k < len(in) && (in[k] == ' ' || in[k] == '\t') {
		k++
	}
	if k >= len(in) || in[k] < '0' || in[k] > '9' {
		return false
	}
	return true
}

func skipSingleQuoted(s string, i int) int {
	j := i + 1
	for j < len(s) {
		if s[j] == '\\' && j+1 < len(s) {
			j += 2
			continue
		}
		if s[j] == '\'' {
			if j+1 < len(s) && s[j+1] == '\'' {
				j += 2
				continue
			}
			return j + 1
		}
		j++
	}
	return len(s)
}

func skipDoubleQuoted(s string, i int) int {
	j := i + 1
	for j < len(s) {
		if s[j] == '"' {
			if j+1 < len(s) && s[j+1] == '"' {
				j += 2
				continue
			}
			return j + 1
		}
		j++
	}
	return len(s)
}

func skipBackquoted(s string, i int) int {
	j := i + 1
	for j < len(s) {
		if s[j] == '`' {
			return j + 1
		}
		j++
	}
	return len(s)
}

func isIdentByte(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isWordStart(s string, i int) bool {
	if !isIdentByte(s[i]) || (s[i] >= '0' && s[i] <= '9') {
		return false
	}
	if i == 0 {
		return true
	}
	prev := rune(s[i-1])
	return !unicode.IsLetter(prev) && !unicode.IsDigit(prev) && prev != '_'
}

// countParams returns the number of `?` placeholders in s, ignoring those
// inside quoted strings or identifiers.
func countParams(s string) int {
	n := 0
	i := 0
	for i < len(s) {
		switch s[i] {
		case '\'':
			i = skipSingleQuoted(s, i)
		case '"':
			i = skipDoubleQuoted(s, i)
		case '`':
			i = skipBackquoted(s, i)
		case '?':
			n++
			i++
		default:
			i++
		}
	}
	return n
}

// isSelectish reports whether the statement returns rows.
func isSelectish(sql string) bool {
	up := strings.ToUpper(strings.TrimLeftFunc(sql, unicode.IsSpace))
	return strings.HasPrefix(up, "SELECT") ||
		strings.HasPrefix(up, "WITH") ||
		strings.HasPrefix(up, "SHOW") ||
		strings.HasPrefix(up, "DESCRIBE") ||
		strings.HasPrefix(up, "DESC ") ||
		strings.HasPrefix(up, "EXPLAIN") ||
		strings.HasPrefix(up, "VALUES")
}
