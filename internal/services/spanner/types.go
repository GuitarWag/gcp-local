package spanner

import (
	"fmt"
	"strconv"
	"strings"

	sppb "cloud.google.com/go/spanner/apiv1/spannerpb"
	structpb "google.golang.org/protobuf/types/known/structpb"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ---------------------------------------------------------------------------
// Spanner → SQLite type mapping
// ---------------------------------------------------------------------------

// sqliteValue converts a Spanner wire value (structpb.Value) to a Go value
// for database/sql, given the declared type. label names the value in the
// InvalidArgument error a malformed value produces.
func sqliteValue(v *structpbValue, typ *sppb.Type, label string) (any, error) {
	if v == nil || v.GetKind() == nil {
		return nil, nil
	}
	if _, isNull := v.GetKind().(*structpb.Value_NullValue); isNull {
		return nil, nil
	}
	bad := func() error {
		return errInvalidArgument("Invalid value for %s: Expected %s.", label, typ.GetCode())
	}
	str, isStr := v.GetKind().(*structpb.Value_StringValue)
	switch typ.GetCode() {
	case sppb.TypeCode_ARRAY:
		if v.GetListValue() == nil {
			return nil, bad()
		}
		return jsonMarshal(v.GetListValue())
	case sppb.TypeCode_INT64:
		if !isStr {
			return nil, bad()
		}
		n, err := strconv.ParseInt(str.StringValue, 10, 64)
		if err != nil {
			return nil, bad()
		}
		return n, nil
	case sppb.TypeCode_FLOAT64, sppb.TypeCode_FLOAT32:
		if f, ok := v.GetKind().(*structpb.Value_NumberValue); ok {
			return f.NumberValue, nil
		}
		// NaN / Infinity arrive as strings.
		if isStr && (str.StringValue == "NaN" || str.StringValue == "Infinity" || str.StringValue == "-Infinity") {
			return str.StringValue, nil
		}
		return nil, bad()
	case sppb.TypeCode_BOOL:
		b, ok := v.GetKind().(*structpb.Value_BoolValue)
		if !ok {
			return nil, bad()
		}
		return b.BoolValue, nil
	case sppb.TypeCode_TYPE_CODE_UNSPECIFIED:
		// No declared type (untyped bind parameter): keep the wire scalar.
		switch k := v.GetKind().(type) {
		case *structpb.Value_StringValue:
			return k.StringValue, nil
		case *structpb.Value_NumberValue:
			return k.NumberValue, nil
		case *structpb.Value_BoolValue:
			return k.BoolValue, nil
		}
		return nil, errInvalidArgument("Invalid value for %s: unsupported value kind.", label)
	default:
		// STRING / BYTES (base64) / TIMESTAMP / DATE / NUMERIC / JSON: text.
		if !isStr {
			return nil, bad()
		}
		return str.StringValue, nil
	}
}

// spannerValue converts a SQLite column value back into the Spanner wire
// representation for the declared column type.
func spannerValue(raw any, typ *sppb.Type) (*structpbValue, error) {
	if raw == nil {
		return nullValue(), nil
	}
	if typ.GetCode() == sppb.TypeCode_ARRAY {
		// raw arrives as TEXT holding JSON.
		s, ok := raw.(string)
		if !ok {
			return nil, errInternal("bad array storage %T", raw)
		}
		lv, err := jsonUnmarshalList(s)
		if err != nil {
			return nil, errInternal("bad array JSON: %v", err)
		}
		return listValue(lv), nil
	}
	switch typ.GetCode() {
	case sppb.TypeCode_INT64:
		switch v := raw.(type) {
		case int64:
			return stringValue(fmt.Sprintf("%d", v)), nil
		case float64:
			return stringValue(fmt.Sprintf("%d", int64(v))), nil
		case string:
			return stringValue(v), nil
		default:
			return nil, errInternal("bad INT64 storage %T", raw)
		}
	case sppb.TypeCode_FLOAT64, sppb.TypeCode_FLOAT32:
		switch v := raw.(type) {
		case float64:
			return numberValue(v), nil
		case string:
			// NaN / Infinity survive SQLite REAL columns as TEXT in
			// practice; pass them through, numbers via parse.
			return stringValue(v), nil
		case int64:
			return numberValue(float64(v)), nil
		default:
			return nil, errInternal("bad FLOAT64 storage %T", raw)
		}
	case sppb.TypeCode_BOOL:
		switch v := raw.(type) {
		case int64:
			return boolValue(v != 0), nil
		case bool:
			return boolValue(v), nil
		case string:
			return boolValue(v == "1" || v == "true"), nil
		default:
			return nil, errInternal("bad BOOL storage %T", raw)
		}
	default:
		// STRING / TIMESTAMP / DATE / NUMERIC / BYTES / JSON: stored as text.
		switch v := raw.(type) {
		case string:
			return stringValue(v), nil
		case []byte:
			return stringValue(string(v)), nil
		case int64:
			return stringValue(fmt.Sprintf("%d", v)), nil
		case float64:
			return stringValue(fmt.Sprintf("%v", v)), nil
		default:
			return nil, errInternal("bad TEXT storage %T", raw)
		}
	}
}

// ---------------------------------------------------------------------------
// Error helpers — Spanner-shaped gRPC errors
// ---------------------------------------------------------------------------

func errNotFound(format string, args ...any) error {
	return status.Error(codes.NotFound, "spanner: "+fmt.Sprintf(format, args...))
}

func errAlreadyExists(format string, args ...any) error {
	return status.Error(codes.AlreadyExists, "spanner: "+fmt.Sprintf(format, args...))
}

func errInvalidArgument(format string, args ...any) error {
	return status.Error(codes.InvalidArgument, "spanner: "+fmt.Sprintf(format, args...))
}

func errInternal(format string, args ...any) error {
	return status.Error(codes.Internal, "spanner: "+fmt.Sprintf(format, args...))
}

func errUnimplemented(format string, args ...any) error {
	return status.Error(codes.Unimplemented, "spanner: "+fmt.Sprintf(format, args...))
}

// ---------------------------------------------------------------------------
// Value constructors (structpb shims to keep call sites short)
// ---------------------------------------------------------------------------

type structpbValue = structpb.Value

func nullValue() *structpbValue {
	return &structpbValue{Kind: &structpb.Value_NullValue{NullValue: structpb.NullValue_NULL_VALUE}}
}

func stringValue(s string) *structpbValue {
	return &structpbValue{Kind: &structpb.Value_StringValue{StringValue: s}}
}

func numberValue(f float64) *structpbValue {
	return &structpbValue{Kind: &structpb.Value_NumberValue{NumberValue: f}}
}

func boolValue(b bool) *structpbValue {
	return &structpbValue{Kind: &structpb.Value_BoolValue{BoolValue: b}}
}

func listValue(lv *structpb.ListValue) *structpbValue {
	return &structpbValue{Kind: &structpb.Value_ListValue{ListValue: lv}}
}

func hasPrefixFold(s, p string) bool {
	return len(s) >= len(p) && strings.EqualFold(s[:len(p)], p)
}
