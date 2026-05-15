package pgwire

import (
	"strconv"
	"strings"
	"unicode"
)

// translateSQL adapts a Postgres-flavoured statement into something SQLite
// can run. The transformations are deliberately small:
//   - $N positional parameters → ?N (sqlite supports the same syntax)
//   - SERIAL / BIGSERIAL → INTEGER PRIMARY KEY AUTOINCREMENT
//   - bytea / boolean → BLOB / BOOLEAN (sqlite affinity match)
//   - explicit cast `value::type` is left alone (sqlite ignores ::, but only
//     in some builds — we strip the trailing ::type to be safe)
//
// String literals and quoted identifiers are skipped so substitutions don't
// rewrite their contents.
func translateSQL(in string) string {
	out, _ := translate(in)
	return out
}

// translate returns both the SQLite-flavoured SQL and the parameter order
// (one entry per `?` in the output, value = 1-based pg parameter index).
func translate(in string) (string, []int) {
	out, order := rewriteSQL(in)
	out = stripCasts(out)
	out = replaceKeywords(out, map[string]string{
		"BIGSERIAL": "INTEGER PRIMARY KEY AUTOINCREMENT",
		"SERIAL":    "INTEGER PRIMARY KEY AUTOINCREMENT",
		"BYTEA":     "BLOB",
		"BOOLEAN":   "BOOLEAN",
	})
	return out, order
}

// dollarToQuestion rewrites Postgres `$N` placeholders to sqlite `?N`.
// Sqlite supports numbered placeholders, but modernc.org/sqlite's database/sql
// driver only counts positional `?` slots when comparing to args, so we emit
// a bare `?` for every $N occurrence — meaning each reuse expands the args
// slice. The walk respects single-quoted strings, double-quoted identifiers
// and dollar-quoted blocks ($tag$...$tag$).
//
// rewriteSQL returns the translated SQL plus the mapping from output `?` slot
// → original 1-based pg parameter index, so the caller can rebuild the
// argument list when a parameter is referenced more than once.
func dollarToQuestion(in string) string {
	out, _ := rewriteSQL(in)
	return out
}

// rewriteSQL is the variant of dollarToQuestion that also reports the
// parameter index for each `?` it emitted.
func rewriteSQL(in string) (string, []int) {
	var b strings.Builder
	b.Grow(len(in))
	var order []int
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
		case '$':
			if tag, end, ok := dollarQuoteTag(in, i); ok {
				j := findDollarQuoteEnd(in, end, tag)
				b.WriteString(in[i:j])
				i = j
				continue
			}
			if i+1 < len(in) && in[i+1] >= '0' && in[i+1] <= '9' {
				j := i + 1
				for j < len(in) && in[j] >= '0' && in[j] <= '9' {
					j++
				}
				n := 0
				for k := i + 1; k < j; k++ {
					n = n*10 + int(in[k]-'0')
				}
				b.WriteByte('?')
				order = append(order, n)
				i = j
				continue
			}
			b.WriteByte(c)
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String(), order
}

func skipSingleQuoted(s string, i int) int {
	j := i + 1
	for j < len(s) {
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

// dollarQuoteTag matches `$tag$` or `$$` at position i; returns the tag and
// the byte index just past the closing `$`.
func dollarQuoteTag(s string, i int) (string, int, bool) {
	if i >= len(s) || s[i] != '$' {
		return "", i, false
	}
	j := i + 1
	for j < len(s) && (isIdentByte(s[j])) {
		j++
	}
	if j < len(s) && s[j] == '$' {
		return s[i+1 : j], j + 1, true
	}
	return "", i, false
}

func findDollarQuoteEnd(s string, from int, tag string) int {
	delim := "$" + tag + "$"
	idx := strings.Index(s[from:], delim)
	if idx < 0 {
		return len(s)
	}
	return from + idx + len(delim)
}

func isIdentByte(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// stripCasts drops Postgres `::type` casts. Conservative — only removes the
// pattern `::IDENT` (no parens), which is the common case.
func stripCasts(in string) string {
	var b strings.Builder
	b.Grow(len(in))
	i := 0
	for i < len(in) {
		if i+1 < len(in) && in[i] == ':' && in[i+1] == ':' {
			j := i + 2
			for j < len(in) && (isIdentByte(in[j]) || in[j] == ' ') {
				j++
			}
			i = j
			continue
		}
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
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// replaceKeywords does case-insensitive word-boundary substitution outside
// of quoted regions.
func replaceKeywords(in string, repl map[string]string) string {
	if len(repl) == 0 {
		return in
	}
	var b strings.Builder
	b.Grow(len(in))
	i := 0
	for i < len(in) {
		c := in[i]
		if c == '\'' {
			j := skipSingleQuoted(in, i)
			b.WriteString(in[i:j])
			i = j
			continue
		}
		if c == '"' {
			j := skipDoubleQuoted(in, i)
			b.WriteString(in[i:j])
			i = j
			continue
		}
		if isWordStart(in, i) {
			j := i
			for j < len(in) && isIdentByte(in[j]) {
				j++
			}
			word := in[i:j]
			up := strings.ToUpper(word)
			if r, ok := repl[up]; ok {
				b.WriteString(r)
			} else {
				b.WriteString(word)
			}
			i = j
			continue
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
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

// splitCommands splits a Simple-Query payload into individual statements
// on `;`, ignoring semicolons inside strings, dollar quotes or comments.
// Empty / whitespace-only fragments are dropped.
func splitCommands(in string) []string {
	var out []string
	var b strings.Builder
	i := 0
	flush := func() {
		s := strings.TrimSpace(b.String())
		if s != "" {
			out = append(out, s)
		}
		b.Reset()
	}
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
		case '$':
			if tag, end, ok := dollarQuoteTag(in, i); ok {
				j := findDollarQuoteEnd(in, end, tag)
				b.WriteString(in[i:j])
				i = j
				continue
			}
			b.WriteByte(c)
			i++
		case '-':
			if i+1 < len(in) && in[i+1] == '-' {
				j := i
				for j < len(in) && in[j] != '\n' {
					j++
				}
				b.WriteString(in[i:j])
				i = j
				continue
			}
			b.WriteByte(c)
			i++
		case ';':
			flush()
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	flush()
	return out
}

// commandTag derives the response CommandComplete tag from a statement.
// Postgres tags look like `SELECT 0`, `INSERT 0 3`, `UPDATE 5`, `CREATE TABLE`.
func commandTag(sql string, rowsAffected int64) string {
	up := strings.ToUpper(strings.TrimLeftFunc(sql, unicode.IsSpace))
	switch {
	case strings.HasPrefix(up, "SELECT"):
		return "SELECT " + strconv.FormatInt(rowsAffected, 10)
	case strings.HasPrefix(up, "INSERT"):
		return "INSERT 0 " + strconv.FormatInt(rowsAffected, 10)
	case strings.HasPrefix(up, "UPDATE"):
		return "UPDATE " + strconv.FormatInt(rowsAffected, 10)
	case strings.HasPrefix(up, "DELETE"):
		return "DELETE " + strconv.FormatInt(rowsAffected, 10)
	case strings.HasPrefix(up, "CREATE TABLE"):
		return "CREATE TABLE"
	case strings.HasPrefix(up, "DROP TABLE"):
		return "DROP TABLE"
	case strings.HasPrefix(up, "BEGIN"), strings.HasPrefix(up, "START TRANSACTION"):
		return "BEGIN"
	case strings.HasPrefix(up, "COMMIT"), strings.HasPrefix(up, "END"):
		return "COMMIT"
	case strings.HasPrefix(up, "ROLLBACK"):
		return "ROLLBACK"
	}
	idx := strings.IndexFunc(up, unicode.IsSpace)
	if idx < 0 {
		return up
	}
	return up[:idx]
}

// isSelectish reports whether the statement returns rows.
func isSelectish(sql string) bool {
	up := strings.ToUpper(strings.TrimLeftFunc(sql, unicode.IsSpace))
	return strings.HasPrefix(up, "SELECT") ||
		strings.HasPrefix(up, "WITH") ||
		strings.HasPrefix(up, "VALUES") ||
		strings.Contains(up, " RETURNING ")
}
