package mysqlwire

import (
	"strings"
	"unicode"
)

// translateSQL adapts a MySQL-flavoured statement into something sqlite can
// run. The transformations are deliberately small and target the basic
// surface (CREATE TABLE / INSERT / SELECT / UPDATE / DELETE plus
// transactions). Anything more exotic is the caller's problem.
//
// All rewrites happen in one pass that skips over single-quoted strings,
// double-quoted strings, backtick identifiers, `-- line` comments, and
// `/* block */` comments, so contents like `'ENGINE=InnoDB failed'` or
// `-- BIGINT is fine` are never mangled.
func translateSQL(in string) string {
	out := make([]byte, 0, len(in))
	i := 0
	for i < len(in) {
		c := in[i]
		switch {
		case c == '\'':
			j := skipSingleQuoted(in, i)
			out = append(out, in[i:j]...)
			i = j
			continue
		case c == '"':
			j := skipDoubleQuoted(in, i)
			out = append(out, in[i:j]...)
			i = j
			continue
		case c == '`':
			j := skipBackquoted(in, i)
			out = append(out, in[i:j]...)
			i = j
			continue
		case c == '-' && i+1 < len(in) && in[i+1] == '-':
			j := skipLineComment(in, i)
			out = append(out, in[i:j]...)
			i = j
			continue
		case c == '/' && i+1 < len(in) && in[i+1] == '*':
			j := skipBlockComment(in, i)
			out = append(out, in[i:j]...)
			i = j
			continue
		}
		if !isWordStart(in, i) {
			out = append(out, c)
			i++
			continue
		}
		j := i
		for j < len(in) && isIdentByte(in[j]) {
			j++
		}
		word := in[i:j]
		up := strings.ToUpper(word)

		// Modifiers sqlite doesn't recognise on column types.
		switch up {
		case "UNSIGNED", "ZEROFILL":
			if j < len(in) && (in[j] == ' ' || in[j] == '\t') {
				j++
			}
			i = j
			continue
		case "AUTO_INCREMENT":
			// `AUTO_INCREMENT=N` is a table option (start value) and
			// must be stripped — leaving it as `AUTOINCREMENT=N` would
			// be a sqlite parse error. The column-modifier form (no
			// trailing `=`) still rewrites to AUTOINCREMENT.
			if k := matchTableOption(in, i, up); k > 0 {
				for len(out) > 0 && (out[len(out)-1] == ' ' || out[len(out)-1] == '\t') {
					out = out[:len(out)-1]
				}
				i = k
				continue
			}
			out = append(out, "AUTOINCREMENT"...)
			i = j
			continue
		}

		// Type keywords that map onto sqlite-flavoured types.
		if r, ok := typeReplacements[up]; ok {
			out = append(out, r...)
			// Eat an optional `(N)` or `(N,M)` size hint after numeric
			// types so sqlite doesn't reject e.g. `INT(11)`.
			if j < len(in) && in[j] == '(' && isNumericLen(in, j) {
				if end := strings.IndexByte(in[j:], ')'); end >= 0 {
					j += end + 1
				}
			}
			i = j
			continue
		}

		// Table options that follow a CREATE TABLE ... ( ... ) closing paren,
		// like `ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin`.
		if k := matchTableOption(in, i, up); k > 0 {
			// Drop whitespace already emitted ahead of the option so the
			// rewrite doesn't leave a stranded space behind.
			for len(out) > 0 && (out[len(out)-1] == ' ' || out[len(out)-1] == '\t') {
				out = out[:len(out)-1]
			}
			i = k
			continue
		}

		out = append(out, word...)
		i = j
	}
	return string(out)
}

// typeReplacements maps MySQL-only column types onto sqlite-flavoured types.
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

// matchTableOption returns the position just past a `KEYWORD = VALUE` table
// option starting at `start` (where `up` is the already-uppercased keyword
// at that position), or 0 if the word doesn't introduce one. Recognised
// keywords (case-insensitive): ENGINE, CHARSET, COLLATE, AUTO_INCREMENT,
// ROW_FORMAT, COMMENT, plus the two-word form `DEFAULT CHARSET`.
func matchTableOption(in string, start int, up string) int {
	var afterKey int
	switch up {
	case "ENGINE", "CHARSET", "COLLATE", "AUTO_INCREMENT", "ROW_FORMAT", "COMMENT":
		afterKey = start + len(up)
	case "DEFAULT":
		j := start + len("DEFAULT")
		for j < len(in) && (in[j] == ' ' || in[j] == '\t') {
			j++
		}
		const charset = "CHARSET"
		if j+len(charset) > len(in) || !strings.EqualFold(in[j:j+len(charset)], charset) {
			return 0
		}
		k := j + len(charset)
		if k < len(in) && isIdentByte(in[k]) {
			return 0
		}
		afterKey = k
	default:
		return 0
	}
	j := afterKey
	for j < len(in) && (in[j] == ' ' || in[j] == '\t') {
		j++
	}
	if j >= len(in) || in[j] != '=' {
		return 0
	}
	j++
	for j < len(in) && (in[j] == ' ' || in[j] == '\t') {
		j++
	}
	if j >= len(in) {
		return 0
	}
	if in[j] == '\'' {
		return skipSingleQuoted(in, j)
	}
	if !isIdentByte(in[j]) {
		return 0
	}
	k := j
	for k < len(in) && isIdentByte(in[k]) {
		k++
	}
	return k
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

func skipLineComment(s string, i int) int {
	j := i + 2
	for j < len(s) && s[j] != '\n' {
		j++
	}
	return j
}

func skipBlockComment(s string, i int) int {
	j := i + 2
	for j+1 < len(s) {
		if s[j] == '*' && s[j+1] == '/' {
			return j + 2
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
// inside quoted strings, identifiers, or comments.
func countParams(s string) int {
	n := 0
	i := 0
	for i < len(s) {
		switch {
		case s[i] == '\'':
			i = skipSingleQuoted(s, i)
		case s[i] == '"':
			i = skipDoubleQuoted(s, i)
		case s[i] == '`':
			i = skipBackquoted(s, i)
		case s[i] == '-' && i+1 < len(s) && s[i+1] == '-':
			i = skipLineComment(s, i)
		case s[i] == '/' && i+1 < len(s) && s[i+1] == '*':
			i = skipBlockComment(s, i)
		case s[i] == '?':
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
