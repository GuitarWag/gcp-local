package spanner

// mutation.go: Commit application (buffered DML + mutations) and table
// introspection.

import (
	"database/sql"
	"fmt"
	"strings"

	sppb "cloud.google.com/go/spanner/apiv1/spannerpb"
	structpb "google.golang.org/protobuf/types/known/structpb"
)

// sqlRunner is the subset of *sql.DB and *sql.Tx the executors need.
type sqlRunner interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
}

// commitWrites applies a transaction's buffered DML, then its mutations,
// inside one SQLite transaction: all of it lands or none of it does.
func (s *Service) commitWrites(database string, dml []bufferedDML, mutations []*sppb.Mutation) error {
	if len(dml) == 0 && len(mutations) == 0 {
		return nil
	}
	db, err := s.engine.dbFor(database)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return errInternal("begin sqlite tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := replayDML(tx, dml); err != nil {
		return err
	}
	for _, m := range mutations {
		if err := applyMutation(tx, m); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return errInternal("commit: %v", err)
	}
	return nil
}

func replayDML(r sqlRunner, dml []bufferedDML) error {
	for _, d := range dml {
		if _, err := r.Exec(d.sql, d.args...); err != nil {
			return sqlErrToGRPC(err)
		}
	}
	return nil
}

func applyMutation(tx *sql.Tx, m *sppb.Mutation) error {
	switch op := m.GetOperation().(type) {
	case *sppb.Mutation_Insert:
		return applyWrite(tx, op.Insert, "INSERT")
	case *sppb.Mutation_Update:
		return applyUpdate(tx, op.Update)
	case *sppb.Mutation_Replace:
		return applyWrite(tx, op.Replace, "INSERT OR REPLACE")
	case *sppb.Mutation_InsertOrUpdate:
		return applyWrite(tx, op.InsertOrUpdate, "INSERT OR REPLACE")
	case *sppb.Mutation_Delete_:
		return applyDelete(tx, op.Delete)
	default:
		return errInvalidArgument("unsupported mutation type")
	}
}

// applyWrite runs INSERT (UNIQUE violation → AlreadyExists) or
// INSERT OR REPLACE for Replace / InsertOrUpdate.
func applyWrite(tx *sql.Tx, w *sppb.Mutation_Write, verb string) error {
	if !validIdent.MatchString(w.GetTable()) {
		return errInvalidArgument("invalid table name %q", w.GetTable())
	}
	if len(w.GetColumns()) == 0 || len(w.GetValues()) == 0 {
		return errInvalidArgument("mutation needs columns and values")
	}
	info, err := tableInfo(tx, w.GetTable())
	if err != nil {
		return err
	}
	q := fmt.Sprintf("%s INTO %s (%s) VALUES %s",
		verb, quoteIdent(w.GetTable()), strings.Join(quotedIdents(w.GetColumns()), ", "), placeholders(len(w.GetColumns())))
	for _, lv := range w.GetValues() {
		args, err := writeArgs(w, lv, info, w.GetColumns())
		if err != nil {
			return err
		}
		if _, err := tx.Exec(q, args...); err != nil {
			return sqlErrToGRPC(err)
		}
	}
	return nil
}

// applyUpdate runs UPDATE ... WHERE pk = ?, failing NotFound when no row
// matched. PK columns select the row; the rest are assigned.
func applyUpdate(tx *sql.Tx, w *sppb.Mutation_Write) error {
	if !validIdent.MatchString(w.GetTable()) {
		return errInvalidArgument("invalid table name %q", w.GetTable())
	}
	info, err := tableInfo(tx, w.GetTable())
	if err != nil {
		return err
	}
	if len(info.pk) == 0 {
		return errInvalidArgument("table %s has no primary key", w.GetTable())
	}
	present := make(map[string]bool, len(w.GetColumns()))
	for _, c := range w.GetColumns() {
		present[c] = true
	}
	isPK := make(map[string]bool, len(info.pk))
	where := make([]string, len(info.pk))
	for i, c := range info.pk {
		if !present[c] {
			return errInvalidArgument("UPDATE mutation missing primary key column %s", c)
		}
		isPK[c] = true
		where[i] = quoteIdent(c) + " = ?"
	}
	var set, setCols []string
	for _, c := range w.GetColumns() {
		if !isPK[c] {
			set = append(set, quoteIdent(c)+" = ?")
			setCols = append(setCols, c)
		}
	}
	q := fmt.Sprintf("UPDATE %s SET %s WHERE %s",
		quoteIdent(w.GetTable()), strings.Join(set, ", "), strings.Join(where, " AND "))
	if len(set) == 0 {
		// PK-only update: verify existence instead.
		q = fmt.Sprintf("SELECT 1 FROM %s WHERE %s", quoteIdent(w.GetTable()), strings.Join(where, " AND "))
	}
	argCols := append(setCols, info.pk...)
	for _, lv := range w.GetValues() {
		args, err := writeArgs(w, lv, info, argCols)
		if err != nil {
			return err
		}
		var n int64
		if len(set) == 0 {
			var one int
			if tx.QueryRow(q, args...).Scan(&one) == nil {
				n = 1
			}
		} else {
			res, err := tx.Exec(q, args...)
			if err != nil {
				return sqlErrToGRPC(err)
			}
			if n, err = res.RowsAffected(); err != nil {
				return errInternal("rows affected: %v", err)
			}
		}
		if n == 0 {
			return errNotFound("Row not found: %s", rowKeyString(w, lv))
		}
	}
	return nil
}

// writeArgs converts the named columns of one mutation row using the
// table's declared column types.
func writeArgs(w *sppb.Mutation_Write, lv *structpb.ListValue, info *tableMeta, cols []string) ([]any, error) {
	if len(lv.GetValues()) != len(w.GetColumns()) {
		return nil, errInvalidArgument("mutation row has %d values, want %d", len(lv.GetValues()), len(w.GetColumns()))
	}
	idx := make(map[string]int, len(w.GetColumns()))
	for i, c := range w.GetColumns() {
		idx[c] = i
	}
	args := make([]any, 0, len(cols))
	for _, c := range cols {
		v, err := sqliteValue(lv.GetValues()[idx[c]], info.types[c],
			fmt.Sprintf("column %s in table %s", c, w.GetTable()))
		if err != nil {
			return nil, err
		}
		args = append(args, v)
	}
	return args, nil
}

// applyDelete applies a Delete mutation over the table's key set.
func applyDelete(tx *sql.Tx, d *sppb.Mutation_Delete) error {
	if !validIdent.MatchString(d.GetTable()) {
		return errInvalidArgument("invalid table name %q", d.GetTable())
	}
	if d.GetKeySet() == nil {
		return errInvalidArgument("delete mutation needs a key set")
	}
	info, err := tableInfo(tx, d.GetTable())
	if err != nil {
		return err
	}
	where, args, err := keySetWhere(d.GetKeySet(), info)
	if err != nil || where == "" {
		return err
	}
	q := fmt.Sprintf("DELETE FROM %s WHERE %s", quoteIdent(d.GetTable()), where)
	if _, err := tx.Exec(q, args...); err != nil {
		return sqlErrToGRPC(err)
	}
	return nil
}

func quotedIdents(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = quoteIdent(n)
	}
	return out
}

// tableMeta is a table's PK columns (declared order) and column types.
type tableMeta struct {
	table string
	pk    []string
	types map[string]*sppb.Type
}

func tableInfo(r sqlRunner, table string) (*tableMeta, error) {
	rows, err := r.Query(fmt.Sprintf("PRAGMA table_info(%s)", quoteIdent(table)))
	if err != nil {
		return nil, sqlErrToGRPC(err)
	}
	defer func() { _ = rows.Close() }()
	info := &tableMeta{table: table, types: make(map[string]*sppb.Type)}
	pkPos := map[int]string{}
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return nil, errInternal("table_info: %v", err)
		}
		info.types[name] = &sppb.Type{Code: typeCodeFromDecl(typ)}
		if pk > 0 {
			pkPos[pk] = name
		}
	}
	if err := rows.Err(); err != nil {
		return nil, errInternal("table_info: %v", err)
	}
	if len(info.types) == 0 {
		return nil, errNotFound("Table not found: %s", table)
	}
	for i := 1; i <= len(pkPos); i++ {
		info.pk = append(info.pk, pkPos[i])
	}
	return info, nil
}

// typeCodeFromDecl maps a SQLite declared type back to a Spanner TypeCode.
func typeCodeFromDecl(decl string) sppb.TypeCode {
	up := strings.ToUpper(decl)
	switch {
	case strings.Contains(up, "BOOL"):
		return sppb.TypeCode_BOOL
	case strings.Contains(up, "INT"):
		return sppb.TypeCode_INT64
	case strings.Contains(up, "REAL"), strings.Contains(up, "FLOA"), strings.Contains(up, "DOUBLE"):
		return sppb.TypeCode_FLOAT64
	default:
		return sppb.TypeCode_STRING
	}
}

// rowKeyString renders a human-readable key for NotFound messages.
func rowKeyString(w *sppb.Mutation_Write, lv *structpb.ListValue) string {
	parts := make([]string, len(w.GetColumns()))
	for i, c := range w.GetColumns() {
		parts[i] = fmt.Sprintf("%s=%v", c, lv.GetValues()[i])
	}
	return strings.Join(parts, ", ")
}
