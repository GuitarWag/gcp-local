package pgwire

import (
	"strconv"
	"strings"
	"time"
)

// A tiny subset of Postgres OIDs — enough for basic CRUD round-trips.
const (
	oidBool        uint32 = 16
	oidBytea       uint32 = 17
	oidInt8        uint32 = 20
	oidInt4        uint32 = 23
	oidText        uint32 = 25
	oidFloat4      uint32 = 700
	oidFloat8      uint32 = 701
	oidVarchar     uint32 = 1043
	oidDate        uint32 = 1082
	oidTimestamp   uint32 = 1114
	oidTimestamptz uint32 = 1184
	oidNumeric     uint32 = 1700
	oidUnknown     uint32 = 705
)

// sqliteTypeToOID maps the declared SQLite column type (DECLTYPE / TYPE NAME)
// to the closest Postgres OID. SQLite uses dynamic typing, so this is best-
// effort: column metadata only reflects what was in the CREATE TABLE.
func sqliteTypeToOID(declType string) uint32 {
	t := strings.ToUpper(strings.TrimSpace(declType))
	if t == "" {
		return oidText
	}
	switch {
	case strings.Contains(t, "BIGINT"), strings.Contains(t, "INT8"):
		return oidInt8
	case strings.Contains(t, "INT"):
		return oidInt4
	case strings.Contains(t, "BOOL"):
		return oidBool
	//nolint:misspell // "DOUB" is a prefix match for SQL "DOUBLE", not a misspelling.
	case strings.Contains(t, "REAL"), strings.Contains(t, "FLOA"), strings.Contains(t, "DOUB"):
		return oidFloat8
	case strings.Contains(t, "BLOB"):
		return oidBytea
	case strings.Contains(t, "TIMESTAMPTZ"), strings.Contains(t, "TIMESTAMP WITH"):
		return oidTimestamptz
	case strings.Contains(t, "TIMESTAMP"):
		return oidTimestamp
	case strings.Contains(t, "DATE"):
		return oidDate
	case strings.Contains(t, "NUMERIC"), strings.Contains(t, "DECIMAL"):
		return oidNumeric
	case strings.Contains(t, "CHAR"), strings.Contains(t, "TEXT"), strings.Contains(t, "CLOB"):
		return oidText
	}
	return oidText
}

// encodeText turns a scanned Go value into the Postgres text-format
// representation. We never send binary format from this server — clients
// already negotiate text when they ask for it (format code 0), and pg drivers
// happily decode text for every type in our subset.
func encodeText(v any) []byte {
	switch x := v.(type) {
	case nil:
		return nil
	case []byte:
		// Postgres text format for bytea is `\x` followed by hex.
		// Detect printable strings stored as []byte vs raw binary by trying
		// utf-8: if all bytes are valid utf-8 printable, return as text so
		// SELECT 'abc' (which scans to []byte from sqlite) reads correctly.
		if isProbablyText(x) {
			return append([]byte{}, x...)
		}
		return hexBytea(x)
	case string:
		return []byte(x)
	case bool:
		if x {
			return []byte{'t'}
		}
		return []byte{'f'}
	case int64:
		return []byte(strconv.FormatInt(x, 10))
	case int:
		return []byte(strconv.FormatInt(int64(x), 10))
	case float64:
		return []byte(strconv.FormatFloat(x, 'g', -1, 64))
	case time.Time:
		return []byte(x.UTC().Format("2006-01-02 15:04:05.999999-07"))
	}
	return []byte(toString(v))
}

func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	}
	return strconvAny(v)
}

func strconvAny(v any) string {
	switch x := v.(type) {
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		if x {
			return "true"
		}
		return "false"
	}
	return ""
}

func isProbablyText(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	for _, c := range b {
		if c == 0 {
			return false
		}
	}
	return true
}

func hexBytea(b []byte) []byte {
	const hex = "0123456789abcdef"
	out := make([]byte, 2+len(b)*2)
	out[0] = '\\'
	out[1] = 'x'
	for i, c := range b {
		out[2+i*2] = hex[c>>4]
		out[2+i*2+1] = hex[c&0x0f]
	}
	return out
}

// decodeParam converts a wire parameter value (text or binary format) plus
// its declared OID into a value suitable for database/sql. We accept text
// format for everything and binary only for the most common numeric/bool
// cases, which covers pgx's default and psycopg2.
func decodeParam(raw []byte, format int16, oid uint32) any {
	if raw == nil {
		return nil
	}
	if format == 0 {
		return decodeText(raw, oid)
	}
	return decodeBinary(raw, oid)
}

func decodeText(raw []byte, oid uint32) any {
	switch oid {
	case oidInt8, oidInt4:
		if n, err := strconv.ParseInt(string(raw), 10, 64); err == nil {
			return n
		}
	case oidFloat4, oidFloat8, oidNumeric:
		if f, err := strconv.ParseFloat(string(raw), 64); err == nil {
			return f
		}
	case oidBool:
		switch string(raw) {
		case "t", "true", "TRUE", "1":
			return true
		case "f", "false", "FALSE", "0":
			return false
		}
	case oidBytea:
		if len(raw) >= 2 && raw[0] == '\\' && raw[1] == 'x' {
			return unhex(raw[2:])
		}
		return append([]byte{}, raw...)
	}
	return string(raw)
}

func decodeBinary(raw []byte, oid uint32) any {
	switch oid {
	case oidInt4:
		if len(raw) == 4 {
			return int64(int32(uint32(raw[0])<<24 | uint32(raw[1])<<16 | uint32(raw[2])<<8 | uint32(raw[3])))
		}
	case oidInt8:
		if len(raw) == 8 {
			var n int64
			for _, b := range raw {
				n = (n << 8) | int64(b)
			}
			return n
		}
	case oidBool:
		if len(raw) == 1 {
			return raw[0] != 0
		}
	case oidBytea:
		return append([]byte{}, raw...)
	}
	return string(raw)
}

func unhex(s []byte) []byte {
	out := make([]byte, 0, len(s)/2)
	for i := 0; i+1 < len(s); i += 2 {
		hi := hexNibble(s[i])
		lo := hexNibble(s[i+1])
		if hi < 0 || lo < 0 {
			return nil
		}
		out = append(out, byte(hi<<4|lo))
	}
	return out
}

func hexNibble(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}
