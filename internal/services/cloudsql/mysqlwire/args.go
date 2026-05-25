package mysqlwire

import (
	"strconv"

	"github.com/go-mysql-org/go-mysql/mysql"
)

// convertArgs reduces COM_STMT_EXECUTE binary-protocol params (which come in
// as Go ints/floats or mysql.TypedBytes for the variable-length types) to
// values database/sql can bind directly.
func convertArgs(args []any) []any {
	if len(args) == 0 {
		return nil
	}
	out := make([]any, len(args))
	for i, a := range args {
		out[i] = convertOne(a)
	}
	return out
}

func convertOne(a any) any {
	switch v := a.(type) {
	case nil:
		return nil
	case mysql.TypedBytes:
		return decodeTypedBytes(v)
	case int8:
		return int64(v)
	case int16:
		return int64(v)
	case int32:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	case uint8:
		return uint64(v)
	case uint16:
		return uint64(v)
	case uint32:
		return uint64(v)
	case uint64:
		return v
	case float32:
		return float64(v)
	case float64:
		return v
	case []byte:
		return v
	case string:
		return v
	default:
		return v
	}
}

func decodeTypedBytes(v mysql.TypedBytes) any {
	if v.Bytes == nil {
		return nil
	}
	switch v.Type {
	case mysql.MYSQL_TYPE_TINY_BLOB,
		mysql.MYSQL_TYPE_MEDIUM_BLOB,
		mysql.MYSQL_TYPE_LONG_BLOB,
		mysql.MYSQL_TYPE_BLOB,
		mysql.MYSQL_TYPE_GEOMETRY,
		mysql.MYSQL_TYPE_VECTOR:
		return v.Bytes
	case mysql.MYSQL_TYPE_DECIMAL, mysql.MYSQL_TYPE_NEWDECIMAL:
		// Keep precision: hand the string form to sqlite which stores it
		// as text and converts on the fly if Scan asks for a float.
		return string(v.Bytes)
	}
	// MYSQL_TYPE_BIT also arrives as TypedBytes; try integer parse so simple
	// 0/1 bit columns survive a round trip. Fall back to bytes otherwise.
	if v.Type == mysql.MYSQL_TYPE_BIT {
		if n, err := strconv.ParseInt(string(v.Bytes), 10, 64); err == nil {
			return n
		}
		return v.Bytes
	}
	return string(v.Bytes)
}
