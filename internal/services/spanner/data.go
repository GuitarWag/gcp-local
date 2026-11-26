package spanner

// data.go: ExecuteSql / Read execution paths — SQL classification, DML,
// queries, key-set reads, and mutation application against the SQLite engine.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	sppb "cloud.google.com/go/spanner/apiv1/spannerpb"
	structpb "google.golang.org/protobuf/types/known/structpb"
)

// runSQL executes one statement (query, DML, or DDL) inside the session's
// transaction context and builds a ResultSet.
func (s *Service) runSQL(sess *sessionRecord, sqlStr string, params *structpb.Struct, paramTypes map[string]*sppb.Type) (*sppb.ResultSet, error) {
	args, err := bindParams(params, paramTypes)
	if err != nil {
		return nil, err
	}
	return s.runSQLArgs(sess, sqlStr, args)
}

// runSQLArgs is the core executor. args are pre-converted sql.Named values.
func (s *Service) runSQLArgs(sess *sessionRecord, sqlStr string, args []any) (*sppb.ResultSet, error) {
	if strings.TrimSpace(sqlStr) == "" {
		return nil, errInvalidArgument("empty SQL statement")
	}
	kind := classifySQL(sqlStr)
	db, err := s.engine.dbFor(sess.database)
	if err != nil {
		return nil, err
	}
	switch kind {
	case "ddl":
		stmt, perr := parseDDL(sqlStr)
		if perr != nil {
			return nil, perr
		}
		if stmt.kind == ddlOther {
			return nil, errInvalidArgument("unsupported DDL statement: %s", firstLine(sqlStr))
		}
		if err := s.engine.execDDL(sess.database, stmt); err != nil {
			return nil, err
		}
		return &sppb.ResultSet{}, nil
	case "dml":
		q, err := translateQuery(sqlStr)
		if err != nil {
			return nil, err
		}
		res, err := execDML(db, q, args)
		if err != nil {
			return nil, sqlErrToGRPC(err)
		}
		return dmlResultSet(res), nil
	default: // query
		q, err := translateQuery(sqlStr)
		if err != nil {
			return nil, err
		}
		rows, meta, err := runQuery(db, q, args)
		if err != nil {
			return nil, sqlErrToGRPC(err)
		}
		return &sppb.ResultSet{Metadata: meta, Rows: rows}, nil
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// bindParams converts structpb params into sql.Named arguments. SQLite's
// named-parameter syntax matches Spanner's @name.
func bindParams(params *structpb.Struct, types map[string]*sppb.Type) ([]any, error) {
	if params == nil || len(params.GetFields()) == 0 {
		return nil, nil
	}
	var args []any
	for name, v := range params.GetFields() {
		gv, err := sqliteValue(v, types[name])
		if err != nil {
			return nil, err
		}
		args = append(args, sql.Named(name, gv))
	}
	return args, nil
}

// execDML runs INSERT/UPDATE/DELETE and reports the affected row count.
func execDML(db *sql.DB, q string, args []any) (int64, error) {
	res, err := db.Exec(q, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// dmlResultSet builds a ResultSet carrying just the DML row count.
func dmlResultSet(count int64) *sppb.ResultSet {
	return &sppb.ResultSet{
		Stats: &sppb.ResultSetStats{
			RowCount: &sppb.ResultSetStats_RowCountExact{RowCountExact: count},
		},
	}
}

// runQuery executes a SELECT and returns protobuf rows plus metadata
// describing the result column types.
func runQuery(db *sql.DB, q string, args []any) ([]*structpb.ListValue, *sppb.ResultSetMetadata, error) {
	stmt, err := db.Prepare(q)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = stmt.Close() }()
	rows, err := stmt.Query(args...)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	colTypes, err := rows.ColumnTypes()
	if err != nil {
		return nil, nil, err
	}
	fields := make([]*sppb.StructType_Field, 0, len(colTypes))
	for _, ct := range colTypes {
		fields = append(fields, &sppb.StructType_Field{
			Name: ct.Name(),
			Type: typeFromSQLite(ct),
		})
	}
	meta := &sppb.ResultSetMetadata{
		RowType: &sppb.StructType{Fields: fields},
	}
	var out []*structpb.ListValue
	for rows.Next() {
		raw := make([]any, len(colTypes))
		ptrs := make([]any, len(colTypes))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, err
		}
		lv := &structpb.ListValue{}
		for i, ct := range colTypes {
			typ := typeFromSQLite(ct)
			v, err := spannerValue(raw[i], typ)
			if err != nil {
				return nil, nil, err
			}
			lv.Values = append(lv.Values, v)
		}
		out = append(out, lv)
	}
	return out, meta, rows.Err()
}

// typeFromSQLite maps a SQLite column type back to a Spanner type code.
func typeFromSQLite(ct *sql.ColumnType) *sppb.Type {
	name := strings.ToUpper(ct.DatabaseTypeName())
	switch {
	case strings.Contains(name, "INT"):
		return &sppb.Type{Code: sppb.TypeCode_INT64}
	case strings.Contains(name, "REAL"), strings.Contains(name, "FLOA"), strings.Contains(name, "DOUBLE"):
		return &sppb.Type{Code: sppb.TypeCode_FLOAT64}
	default:
		return &sppb.Type{Code: sppb.TypeCode_STRING}
	}
}

// sqlErrToGRPC maps SQLite errors onto Spanner gRPC codes.
func sqlErrToGRPC(err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "no such table"):
		return errNotFound("Table not found: %s", extractIdent(msg, "no such table: "))
	case strings.Contains(msg, "no such column"):
		return errInvalidArgument("Unrecognized name: %s", extractIdent(msg, "no such column: "))
	case strings.Contains(msg, "UNIQUE constraint failed"):
		return errAlreadyExists("Row already exists: %s", msg)
	case strings.Contains(msg, "NOT NULL constraint failed"):
		return errInvalidArgument("NOT NULL constraint failed: %s", msg)
	default:
		return errInvalidArgument("%s", msg)
	}
}

// extractIdent pulls an identifier out of a SQLite error message.
func extractIdent(msg, prefix string) string {
	i := strings.Index(msg, prefix)
	if i < 0 {
		return msg
	}
	rest := msg[i+len(prefix):]
	if j := strings.IndexAny(rest, " \t\n,"); j >= 0 {
		return rest[:j]
	}
	return rest
}

// ---------------------------------------------------------------------------
// Read (key set + column list)
// ---------------------------------------------------------------------------

func (s *Service) Read(ctx context.Context, req *sppb.ReadRequest) (*sppb.ResultSet, error) {
	sess, err := s.lookupSession(req.GetSession())
	if err != nil {
		return nil, err
	}
	// Key sets constrain the table's primary key column, not the first
	// requested column.
	db, err := s.engine.dbFor(sess.database)
	if err != nil {
		return nil, err
	}
	if err := s.engine.checkTableExists(db, req.GetTable(), false); err != nil {
		return nil, err
	}
	pkCols, err := primaryKeyColsDB(db, req.GetTable())
	if err != nil {
		return nil, err
	}
	if len(pkCols) == 0 {
		return nil, errInvalidArgument("table %s has no primary key", req.GetTable())
	}
	q, named, err := readToSQL(req, pkCols[0])
	if err != nil {
		return nil, err
	}
	return s.runSQLArgs(sess, q, named)
}

func (s *Service) StreamingRead(req *sppb.ReadRequest, stream sppb.Spanner_StreamingReadServer) error {
	rs, err := s.Read(stream.Context(), req)
	if err != nil {
		return err
	}
	return streamResultSet(stream, rs)
}

func (s *Service) ExecuteSql(ctx context.Context, req *sppb.ExecuteSqlRequest) (*sppb.ResultSet, error) {
	sess, err := s.lookupSession(req.GetSession())
	if err != nil {
		return nil, err
	}
	// Transaction selector: "Begin" starts a transaction that stays open
	// until Commit/Rollback carries its id (the SDK's first statement in a
	// ReadWriteTransaction uses this).
	rs, err := s.runSQL(sess, req.GetSql(), req.GetParams(), req.GetParamTypes())
	if err != nil {
		return nil, err
	}
	if sel := req.GetTransaction(); sel != nil {
		if sel.GetBegin() != nil {
			tx, err := s.beginTxn(sess)
			if err != nil {
				return nil, err
			}
			sess.pendingTxn = tx.id
			if rs.GetMetadata() == nil {
				rs.Metadata = &sppb.ResultSetMetadata{}
			}
			rs.Metadata.Transaction = &sppb.Transaction{Id: tx.id}
		}
	}
	return rs, nil
}

func (s *Service) ExecuteStreamingSql(req *sppb.ExecuteSqlRequest, stream sppb.Spanner_ExecuteStreamingSqlServer) error {
	rs, err := s.ExecuteSql(stream.Context(), req)
	if err != nil {
		return err
	}
	return streamResultSet(stream, rs)
}

// streamResultSet sends metadata + rows + stats as PartialResultSets.
func streamResultSet(stream sppb.Spanner_ExecuteStreamingSqlServer, rs *sppb.ResultSet) error {
	if rs.GetMetadata() != nil {
		if err := stream.Send(&sppb.PartialResultSet{Metadata: rs.GetMetadata()}); err != nil {
			return err
		}
	}
	for _, row := range rs.GetRows() {
		if err := stream.Send(&sppb.PartialResultSet{
			Metadata: rs.GetMetadata(),
			Values:   row.GetValues(),
		}); err != nil {
			return err
		}
	}
	if rs.GetStats() != nil {
		if err := stream.Send(&sppb.PartialResultSet{
			Metadata: rs.GetMetadata(),
			Stats:    rs.GetStats(),
		}); err != nil {
			return err
		}
	}
	return nil
}

// readToSQL converts a ReadRequest (table, columns, key set) into
// positional-parameter SQL ("?N" placeholders bound by index).
func readToSQL(req *sppb.ReadRequest, pkCol string) (string, []any, error) {
	table := req.GetTable()
	if !validIdent.MatchString(table) {
		return "", nil, errInvalidArgument("invalid table name %q", table)
	}
	var cols []string
	for _, c := range req.GetColumns() {
		if !validIdent.MatchString(c) {
			return "", nil, errInvalidArgument("invalid column name %q", c)
		}
		cols = append(cols, quoteIdent(c))
	}
	if len(cols) == 0 {
		return "", nil, errInvalidArgument("no columns requested")
	}
	q := fmt.Sprintf("SELECT %s FROM %s", strings.Join(cols, ", "), quoteIdent(table))
	var named []any
	n := 0
	ks := req.GetKeySet()
	if ks == nil || (len(ks.GetKeys()) == 0 && len(ks.GetRanges()) == 0 && !ks.GetAll()) {
		// Empty key set matches nothing.
		return q + " WHERE 1=0", nil, nil
	}
	var conds []string
	for _, key := range ks.GetKeys() {
		var vals []string
		for _, pt := range key.GetValues() {
			n++
			vals = append(vals, "?")
			named = append(named, pointValue(pt))
		}
		if len(vals) > 0 {
			// Point keys match the full PK tuple; with a single-PK table
			// that is one equality (composite keys are a non-goal).
			conds = append(conds, fmt.Sprintf("(%s = ?)", quoteIdent(pkCol)))
			n = len(named) // extra tuple slots beyond column 0 are unused
		}
	}
	// Key ranges constrain the first requested column (composite ranges are
	// a documented non-goal).
	for _, r := range ks.GetRanges() {
		start := r.GetStartClosed()
		startOpen := false
		if start == nil {
			start = r.GetStartOpen()
			startOpen = true
		}
		end := r.GetEndClosed()
		endOpen := false
		if end == nil {
			end = r.GetEndOpen()
			endOpen = true
		}
		if start == nil && end == nil {
			continue
		}
		var parts []string
		if start != nil {
			n++
			op := ">="
			if startOpen {
				op = ">"
			}
			parts = append(parts, fmt.Sprintf("%s %s ?", quoteIdent(pkCol), op))
			named = append(named, firstKeyValue(start))
		}
		if end != nil {
			n++
			op := "<="
			if endOpen {
				op = "<"
			}
			parts = append(parts, fmt.Sprintf("%s %s ?", quoteIdent(pkCol), op))
			named = append(named, firstKeyValue(end))
		}
		if len(parts) > 0 {
			conds = append(conds, "("+strings.Join(parts, " AND ")+")")
		}
	}
	if len(conds) == 0 && !ks.GetAll() {
		return q + " WHERE 1=0", nil, nil
	}
	if len(conds) > 0 {
		q += " WHERE (" + strings.Join(conds, " OR ") + ")"
	}
	if req.GetLimit() > 0 {
		q += fmt.Sprintf(" LIMIT %d", req.GetLimit())
	}
	q += fmt.Sprintf(" ORDER BY %s ASC", quoteIdent(pkCol))
	return q, named, nil
}

// pointValue extracts a single scalar key value for positional binding.
func pointValue(v *structpb.Value) any {
	gv, _ := sqliteValue(v, nil)
	return gv
}

// firstKeyValue binds the first column value of a range boundary key.
func firstKeyValue(lv *structpb.ListValue) any {
	if len(lv.GetValues()) == 0 {
		return nil
	}
	return pointValue(lv.GetValues()[0])
}

// applyMutations applies Commit mutations to the database inside one SQLite
// transaction so partial failures roll back cleanly.
func (s *Service) applyMutations(database string, mutations []*sppb.Mutation) error {
	db, err := s.engine.dbFor(database)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return errInternal("begin sqlite tx: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
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

func applyMutation(tx *sql.Tx, m *sppb.Mutation) error {
	switch op := m.GetOperation().(type) {
	case *sppb.Mutation_Insert:
		return applyWrite(tx, op.Insert, kindInsert)
	case *sppb.Mutation_Update:
		return applyWrite(tx, op.Update, kindUpdate)
	case *sppb.Mutation_Replace:
		return applyWrite(tx, op.Replace, kindReplace)
	case *sppb.Mutation_InsertOrUpdate:
		return applyWrite(tx, op.InsertOrUpdate, kindInsertOrUpdate)
	case *sppb.Mutation_Delete_:
		return applyDelete(tx, op.Delete)
	default:
		return errInvalidArgument("unsupported mutation type")
	}
}

const (
	kindInsert         = "INSERT"
	kindUpdate         = "UPDATE"
	kindReplace        = "REPLACE"
	kindInsertOrUpdate = "INSERT_OR_UPDATE"
)

// applyWrite applies one Mutation_Write. Strategy per kind:
//   - INSERT: plain INSERT → UNIQUE violation → AlreadyExists
//   - UPDATE: UPDATE with WHERE on the PK; zero rows affected → NotFound
//   - REPLACE / INSERT_OR_UPDATE: INSERT OR REPLACE
func applyWrite(tx *sql.Tx, w *sppb.Mutation_Write, kind string) error {
	if !validIdent.MatchString(w.GetTable()) {
		return errInvalidArgument("invalid table name %q", w.GetTable())
	}
	if len(w.GetColumns()) == 0 || len(w.GetValues()) == 0 {
		return errInvalidArgument("mutation needs columns and values")
	}
	colTypes, err := tableColumnTypes(tx, w.GetTable())
	if err != nil {
		return err
	}
	if kind == kindUpdate {
		return applyUpdate(tx, w, colTypes)
	}
	verb := "INSERT"
	if kind == kindReplace || kind == kindInsertOrUpdate {
		verb = "INSERT OR REPLACE"
	}
	placeholders := make([]string, len(w.GetColumns()))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	q := fmt.Sprintf("%s INTO %s (%s) VALUES (%s)",
		verb,
		quoteIdent(w.GetTable()),
		strings.Join(quotedIdents(w.GetColumns()), ", "),
		strings.Join(placeholders, ", "),
	)
	for _, lv := range w.GetValues() {
		args, err := writeArgs(w, lv, colTypes)
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
// matched. Non-PK columns are updated; PK columns act as the selector.
func applyUpdate(tx *sql.Tx, w *sppb.Mutation_Write, colTypes map[string]*sppb.Type) error {
	pkCols, err := primaryKeyColumns(tx, w.GetTable())
	if err != nil {
		return err
	}
	if len(pkCols) == 0 {
		return errInvalidArgument("table %s has no primary key", w.GetTable())
	}
	pkSet := make(map[string]bool, len(pkCols))
	for _, c := range pkCols {
		pkSet[c] = true
	}
	var set []string
	var setCols []string
	for _, c := range w.GetColumns() {
		if pkSet[c] {
			continue
		}
		set = append(set, fmt.Sprintf("%s = ?", quoteIdent(c)))
		setCols = append(setCols, c)
	}
	where := make([]string, len(pkCols))
	for i, c := range pkCols {
		where[i] = fmt.Sprintf("%s = ?", quoteIdent(c))
	}
	// The mutation must include the PK columns to identify the row.
	pkIndex := make(map[string]int, len(w.GetColumns()))
	for i, c := range w.GetColumns() {
		pkIndex[c] = i
	}
	for _, c := range pkCols {
		if _, ok := pkIndex[c]; !ok {
			return errInvalidArgument("UPDATE mutation missing primary key column %s", c)
		}
	}
	if len(set) == 0 {
		// PK-only update: verify existence instead.
		q := fmt.Sprintf("SELECT 1 FROM %s WHERE %s", quoteIdent(w.GetTable()), strings.Join(where, " AND "))
		for _, lv := range w.GetValues() {
			var args []any
			for _, c := range pkCols {
				v, err := sqliteValue(lv.GetValues()[pkIndex[c]], colTypes[c])
				if err != nil {
					return err
				}
				args = append(args, v)
			}
			row := tx.QueryRow(q, args...)
			var one int
			if err := row.Scan(&one); err != nil {
				return errNotFound("Row not found: %s", rowKeyString(w, lv))
			}
		}
		return nil
	}
	q := fmt.Sprintf("UPDATE %s SET %s WHERE %s",
		quoteIdent(w.GetTable()),
		strings.Join(set, ", "),
		strings.Join(where, " AND "),
	)
	for _, lv := range w.GetValues() {
		var args []any
		for _, c := range setCols {
			v, err := sqliteValue(lv.GetValues()[pkIndex[c]], colTypes[c])
			if err != nil {
				return err
			}
			args = append(args, v)
		}
		for _, c := range pkCols {
			v, err := sqliteValue(lv.GetValues()[pkIndex[c]], colTypes[c])
			if err != nil {
				return err
			}
			args = append(args, v)
		}
		res, err := tx.Exec(q, args...)
		if err != nil {
			return sqlErrToGRPC(err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return errInternal("rows affected: %v", err)
		}
		if n == 0 {
			return errNotFound("Row not found: %s", rowKeyString(w, lv))
		}
	}
	return nil
}

// writeArgs converts one mutation row's values using the table's declared
// column types.
func writeArgs(w *sppb.Mutation_Write, lv *structpb.ListValue, colTypes map[string]*sppb.Type) ([]any, error) {
	var args []any
	for i, col := range w.GetColumns() {
		if i >= len(lv.GetValues()) {
			return nil, errInvalidArgument("mutation row has %d values, want %d", len(lv.GetValues()), len(w.GetColumns()))
		}
		v, err := sqliteValue(lv.GetValues()[i], colTypes[col])
		if err != nil {
			return nil, err
		}
		args = append(args, v)
	}
	return args, nil
}

// applyDelete applies a Delete mutation. The table name lives on the
// Mutation_Delete operation itself.
func applyDelete(tx *sql.Tx, d *sppb.Mutation_Delete) error {
	if !validIdent.MatchString(d.GetTable()) {
		return errInvalidArgument("invalid table name %q", d.GetTable())
	}
	ks := d.GetKeySet()
	if ks == nil {
		return errInvalidArgument("delete mutation needs a key set")
	}
	if ks.GetAll() {
		if _, err := tx.Exec(fmt.Sprintf("DELETE FROM %s", quoteIdent(d.GetTable()))); err != nil {
			return sqlErrToGRPC(err)
		}
		return nil
	}
	n := 0
	var conds []string
	var named []any
	for _, key := range ks.GetKeys() {
		var vals []string
		for _, pt := range key.GetValues() {
			n++
			vals = append(vals, "?")
			named = append(named, pointValue(pt))
		}
		if len(vals) > 0 {
			// Point keys match the full PK tuple; with a single-PK table
			// that is one equality (composite keys are a non-goal).
			conds = append(conds, "(pkcol = ?)")
		}
	}
	// Ranges over the PK column.
	for _, r := range ks.GetRanges() {
		start := r.GetStartClosed()
		startOpen := false
		if start == nil {
			start = r.GetStartOpen()
			startOpen = true
		}
		end := r.GetEndClosed()
		endOpen := false
		if end == nil {
			end = r.GetEndOpen()
			endOpen = true
		}
		var parts []string
		if start != nil {
			n++
			op := ">="
			if startOpen {
				op = ">"
			}
			parts = append(parts, fmt.Sprintf("pkcol %s ?", op))
			named = append(named, firstKeyValue(start))
		}
		if end != nil {
			n++
			op := "<="
			if endOpen {
				op = "<"
			}
			parts = append(parts, fmt.Sprintf("pkcol %s ?", op))
			named = append(named, firstKeyValue(end))
		}
		if len(parts) > 0 {
			conds = append(conds, "("+strings.Join(parts, " AND ")+")")
		}
	}
	if len(conds) == 0 {
		return nil // matches nothing
	}
	// Resolve the actual PK column names for the table, then substitute.
	pkCols, err := primaryKeyColumns(tx, d.GetTable())
	if err != nil {
		return err
	}
	pkName := quoteIdent(d.GetTable() + ".pk")
	if len(pkCols) > 0 {
		pkName = quoteIdent(pkCols[0])
	}
	q := fmt.Sprintf("DELETE FROM %s WHERE (%s)", quoteIdent(d.GetTable()), strings.Join(conds, " OR "))
	q = strings.ReplaceAll(q, "pkcol", pkName)
	if _, err := tx.Exec(q, named...); err != nil {
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

// tableColumnTypes returns declared SQLite types per column of a table.
func tableColumnTypes(tx *sql.Tx, table string) (map[string]*sppb.Type, error) {
	rows, err := tx.Query(fmt.Sprintf("PRAGMA table_info(%s)", quoteIdent(table)))
	if err != nil {
		return nil, sqlErrToGRPC(err)
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]*sppb.Type)
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return nil, errInternal("table_info: %v", err)
		}
		out[name] = &sppb.Type{Code: typeCodeFromDecl(typ)}
	}
	return out, rows.Err()
}

// primaryKeyColumns returns the PK column names in declared order.
// primaryKeyColsDB resolves a table's primary key columns from the open DB.
func primaryKeyColsDB(db *sql.DB, table string) ([]string, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, errInternal("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	return primaryKeyColumns(tx, table)
}

func primaryKeyColumns(tx *sql.Tx, table string) ([]string, error) {
	rows, err := tx.Query(fmt.Sprintf("PRAGMA table_info(%s)", quoteIdent(table)))
	if err != nil {
		return nil, sqlErrToGRPC(err)
	}
	defer func() { _ = rows.Close() }()
	type pkCol struct {
		name string
		idx  int
	}
	var pk []pkCol
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pkv int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pkv); err != nil {
			return nil, errInternal("table_info: %v", err)
		}
		if pkv > 0 {
			pk = append(pk, pkCol{name: name, idx: pkv})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := 1; i < len(pk); i++ {
		for j := i; j > 0 && pk[j].idx < pk[j-1].idx; j-- {
			pk[j], pk[j-1] = pk[j-1], pk[j]
		}
	}
	out := make([]string, len(pk))
	for i, c := range pk {
		out[i] = c.name
	}
	return out, nil
}

// typeCodeFromDecl maps a SQLite declared type back to a Spanner TypeCode.
func typeCodeFromDecl(decl string) sppb.TypeCode {
	up := strings.ToUpper(decl)
	switch {
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
