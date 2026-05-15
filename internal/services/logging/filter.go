package logging

import (
	"fmt"
	"strings"
	"time"
)

// Cloud Logging filter parser. Subset only — enough to be useful, not
// the full query language. Supports:
//
//   severity {= != > >= < <=} <LEVEL|number>
//   logName {= !=} "<string>"
//   resource.type {= !=} "<string>"
//   resource.labels.<key> {= !=} "<string>"
//   timestamp {= != > >= < <=} "<RFC3339>"
//   <predicate> AND <predicate>  (case-insensitive)
//
// Anything else (OR, NOT, parens, regex, function calls, has-operator,
// global text search, …) is reported as an error rather than silently
// dropped — callers must know when their filter wasn't honoured.

// severityRank maps the standard Cloud Logging severities to their numeric
// rank. See https://cloud.google.com/logging/docs/reference/v2/rest/v2/LogEntry#LogSeverity
var severityRank = map[string]int{
	"DEFAULT":   0,
	"DEBUG":     100,
	"INFO":      200,
	"NOTICE":    300,
	"WARNING":   400,
	"ERROR":     500,
	"CRITICAL":  600,
	"ALERT":     700,
	"EMERGENCY": 800,
}

type predicate func(logEntry) bool

// parseFilter compiles a filter expression into a predicate that reports
// whether a log entry matches. An empty filter matches everything.
func parseFilter(s string) (predicate, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return func(logEntry) bool { return true }, nil
	}
	toks, err := tokenize(s)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	pred, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.toks) {
		return nil, fmt.Errorf("unexpected token %q at end of filter", p.toks[p.pos].val)
	}
	return pred, nil
}

// --- tokeniser ---------------------------------------------------------

type tokKind int

const (
	tIdent tokKind = iota
	tString
	tNumber
	tOp
	tAnd
)

type token struct {
	kind tokKind
	val  string
}

func tokenize(s string) ([]token, error) {
	var out []token
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			i++
		case c == '"':
			j := i + 1
			for j < len(s) && s[j] != '"' {
				if s[j] == '\\' && j+1 < len(s) {
					j += 2
					continue
				}
				j++
			}
			if j >= len(s) {
				return nil, fmt.Errorf("unterminated string literal")
			}
			out = append(out, token{tString, unescape(s[i+1 : j])})
			i = j + 1
		case c == '=' || c == '!' || c == '<' || c == '>':
			if i+1 < len(s) && (s[i+1] == '=') {
				out = append(out, token{tOp, s[i : i+2]})
				i += 2
			} else {
				if c == '!' {
					return nil, fmt.Errorf("expected '!=' at position %d", i)
				}
				out = append(out, token{tOp, s[i : i+1]})
				i++
			}
		case c == '(' || c == ')':
			return nil, fmt.Errorf("parentheses are not supported")
		case isIdentStart(c):
			j := i
			for j < len(s) && isIdentCont(s[j]) {
				j++
			}
			word := s[i:j]
			up := strings.ToUpper(word)
			switch up {
			case "AND":
				out = append(out, token{tAnd, "AND"})
			case "OR", "NOT":
				return nil, fmt.Errorf("%s is not supported; only AND between predicates", up)
			default:
				if isNumber(word) {
					out = append(out, token{tNumber, word})
				} else {
					out = append(out, token{tIdent, word})
				}
			}
			i = j
		default:
			return nil, fmt.Errorf("unexpected character %q in filter", c)
		}
	}
	return out, nil
}

func isIdentStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' || c == '/'
}

func isIdentCont(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9') || c == '.' || c == '-'
}

func isNumber(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			if i != 0 || (c != '-' && c != '+') {
				return false
			}
		}
	}
	return true
}

func unescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			b.WriteByte(s[i+1])
			i++
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// --- parser ------------------------------------------------------------

type parser struct {
	toks []token
	pos  int
}

func (p *parser) peek() *token {
	if p.pos >= len(p.toks) {
		return nil
	}
	return &p.toks[p.pos]
}

func (p *parser) eat() *token {
	t := p.peek()
	if t != nil {
		p.pos++
	}
	return t
}

func (p *parser) parseExpr() (predicate, error) {
	left, err := p.parsePredicate()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t == nil || t.kind != tAnd {
			break
		}
		p.eat()
		right, err := p.parsePredicate()
		if err != nil {
			return nil, err
		}
		l, r := left, right
		left = func(e logEntry) bool { return l(e) && r(e) }
	}
	return left, nil
}

func (p *parser) parsePredicate() (predicate, error) {
	field := p.eat()
	if field == nil || field.kind != tIdent {
		return nil, fmt.Errorf("expected field name in filter")
	}
	op := p.eat()
	if op == nil || op.kind != tOp {
		return nil, fmt.Errorf("expected comparison operator after %q", field.val)
	}
	val := p.eat()
	if val == nil || (val.kind != tString && val.kind != tIdent && val.kind != tNumber) {
		return nil, fmt.Errorf("expected value after %q %q", field.val, op.val)
	}
	return buildPredicate(field.val, op.val, val.val)
}

// buildPredicate ties a (field, op, value) triple to its evaluator.
func buildPredicate(field, op, val string) (predicate, error) {
	switch {
	case field == "severity":
		want, ok := severityRank[strings.ToUpper(val)]
		if !ok {
			// allow plain numeric ranks too
			if !isNumber(val) {
				return nil, fmt.Errorf("unknown severity %q", val)
			}
			n := 0
			for i := 0; i < len(val); i++ {
				n = n*10 + int(val[i]-'0')
			}
			want = n
		}
		cmp, err := numericCmp(op)
		if err != nil {
			return nil, err
		}
		return func(e logEntry) bool {
			got, ok := severityRank[strings.ToUpper(e.Severity)]
			if !ok {
				got = 0
			}
			return cmp(got, want)
		}, nil

	case field == "logName":
		if op != "=" && op != "!=" {
			return nil, fmt.Errorf("logName supports = and != only")
		}
		return func(e logEntry) bool {
			eq := e.LogName == val
			if op == "=" {
				return eq
			}
			return !eq
		}, nil

	case field == "resource.type":
		if op != "=" && op != "!=" {
			return nil, fmt.Errorf("resource.type supports = and != only")
		}
		return func(e logEntry) bool {
			t, _ := e.Resource["type"].(string)
			eq := t == val
			if op == "=" {
				return eq
			}
			return !eq
		}, nil

	case strings.HasPrefix(field, "resource.labels."):
		if op != "=" && op != "!=" {
			return nil, fmt.Errorf("resource.labels supports = and != only")
		}
		key := strings.TrimPrefix(field, "resource.labels.")
		if key == "" {
			return nil, fmt.Errorf("resource.labels.<key> requires a key")
		}
		return func(e logEntry) bool {
			labels, _ := e.Resource["labels"].(map[string]any)
			s, _ := labels[key].(string)
			eq := s == val
			if op == "=" {
				return eq
			}
			return !eq
		}, nil

	case field == "timestamp":
		ts, err := time.Parse(time.RFC3339, val)
		if err != nil {
			return nil, fmt.Errorf("timestamp value %q is not RFC3339: %w", val, err)
		}
		cmp, err := timeCmp(op)
		if err != nil {
			return nil, err
		}
		return func(e logEntry) bool { return cmp(e.Timestamp, ts) }, nil
	}

	return nil, fmt.Errorf("unsupported field %q in filter", field)
}

func numericCmp(op string) (func(a, b int) bool, error) {
	switch op {
	case "=":
		return func(a, b int) bool { return a == b }, nil
	case "!=":
		return func(a, b int) bool { return a != b }, nil
	case ">":
		return func(a, b int) bool { return a > b }, nil
	case ">=":
		return func(a, b int) bool { return a >= b }, nil
	case "<":
		return func(a, b int) bool { return a < b }, nil
	case "<=":
		return func(a, b int) bool { return a <= b }, nil
	}
	return nil, fmt.Errorf("unsupported operator %q", op)
}

func timeCmp(op string) (func(a, b time.Time) bool, error) {
	switch op {
	case "=":
		return func(a, b time.Time) bool { return a.Equal(b) }, nil
	case "!=":
		return func(a, b time.Time) bool { return !a.Equal(b) }, nil
	case ">":
		return func(a, b time.Time) bool { return a.After(b) }, nil
	case ">=":
		return func(a, b time.Time) bool { return a.After(b) || a.Equal(b) }, nil
	case "<":
		return func(a, b time.Time) bool { return a.Before(b) }, nil
	case "<=":
		return func(a, b time.Time) bool { return a.Before(b) || a.Equal(b) }, nil
	}
	return nil, fmt.Errorf("unsupported operator %q", op)
}
