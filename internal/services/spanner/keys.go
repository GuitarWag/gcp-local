package spanner

// keys.go: KeySet → SQL WHERE conversion shared by Read and Delete.

import (
	"fmt"
	"strings"

	sppb "cloud.google.com/go/spanner/apiv1/spannerpb"
	structpb "google.golang.org/protobuf/types/known/structpb"
)

// keySetWhere renders a KeySet as a WHERE expression over the table's
// primary key. Keys and range bounds may be PK prefixes; they compare as
// SQLite row values. An empty result means "match nothing" unless ks.All.
func keySetWhere(ks *sppb.KeySet, info *tableMeta) (string, []any, error) {
	if ks.GetAll() {
		return "1=1", nil, nil
	}
	var conds []string
	var args []any
	for _, key := range ks.GetKeys() {
		tuple, vals, err := keyTuple(key, info)
		if err != nil {
			return "", nil, err
		}
		if tuple != "" {
			conds = append(conds, tuple+" = "+placeholders(len(vals)))
			args = append(args, vals...)
		}
	}
	for _, r := range ks.GetRanges() {
		var parts []string
		bound := func(key *structpb.ListValue, op string) error {
			tuple, vals, err := keyTuple(key, info)
			if err != nil || tuple == "" {
				return err
			}
			parts = append(parts, tuple+" "+op+" "+placeholders(len(vals)))
			args = append(args, vals...)
			return nil
		}
		var err error
		switch {
		case r.GetStartClosed() != nil:
			err = bound(r.GetStartClosed(), ">=")
		case r.GetStartOpen() != nil:
			err = bound(r.GetStartOpen(), ">")
		}
		if err != nil {
			return "", nil, err
		}
		switch {
		case r.GetEndClosed() != nil:
			err = bound(r.GetEndClosed(), "<=")
		case r.GetEndOpen() != nil:
			err = bound(r.GetEndOpen(), "<")
		}
		if err != nil {
			return "", nil, err
		}
		if len(parts) == 0 {
			parts = []string{"1=1"} // both bounds empty: whole table
		}
		conds = append(conds, "("+strings.Join(parts, " AND ")+")")
	}
	if len(conds) == 0 {
		return "", nil, nil
	}
	return "(" + strings.Join(conds, " OR ") + ")", args, nil
}

// keyTuple converts a key (a PK prefix) into a "(col, ...)" tuple and its
// typed bind values.
func keyTuple(key *structpb.ListValue, info *tableMeta) (string, []any, error) {
	if len(key.GetValues()) > len(info.pk) {
		return "", nil, errInvalidArgument("key has %d parts but table %s has %d primary key columns",
			len(key.GetValues()), info.table, len(info.pk))
	}
	cols := make([]string, 0, len(key.GetValues()))
	vals := make([]any, 0, len(key.GetValues()))
	for i, v := range key.GetValues() {
		col := info.pk[i]
		gv, err := sqliteValue(v, info.types[col], fmt.Sprintf("key column %s in table %s", col, info.table))
		if err != nil {
			return "", nil, err
		}
		cols = append(cols, quoteIdent(col))
		vals = append(vals, gv)
	}
	if len(cols) == 0 {
		return "", nil, nil
	}
	return "(" + strings.Join(cols, ", ") + ")", vals, nil
}

func placeholders(n int) string {
	return "(" + strings.TrimSuffix(strings.Repeat("?, ", n), ", ") + ")"
}
