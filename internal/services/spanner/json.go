package spanner

// json.go: structpb ↔ JSON helpers for ARRAY column storage.

import (
	"encoding/json"
	"fmt"

	structpb "google.golang.org/protobuf/types/known/structpb"
)

// jsonMarshal encodes a structpb.Value (usually a ListValue) as JSON text.
func jsonMarshal(v any) (string, error) {
	switch t := v.(type) {
	case *structpb.ListValue:
		b, err := json.Marshal(t.AsSlice())
		if err != nil {
			return "", err
		}
		return string(b), nil
	case *structpbValue:
		b, err := json.Marshal(t.AsInterface())
		if err != nil {
			return "", err
		}
		return string(b), nil
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("json marshal: %w", err)
		}
		return string(b), nil
	}
}

// jsonUnmarshalList decodes JSON text into a structpb.ListValue.
func jsonUnmarshalList(s string) (*structpb.ListValue, error) {
	var raw []any
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return nil, err
	}
	lv := &structpb.ListValue{}
	for _, item := range raw {
		v, err := structpb.NewValue(item)
		if err != nil {
			return nil, err
		}
		lv.Values = append(lv.Values, v)
	}
	return lv, nil
}
