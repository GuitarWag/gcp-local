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

// runSQL executes one statement (query, DML, or DDL) and builds a
// ResultSet. A non-nil txn routes the statement through the transaction's
// DML buffer; nil autocommits.
func (s *Service) runSQL(sess *sessionRecord, sqlStr string, params *structpb.Struct, paramTypes map[string]*sppb.Type, txn *txnRecord) (*sppb.ResultSet, error) {
	args, err := bindParams(params, paramTypes)
	if err != nil {
		return nil, err
	}
	return s.runSQLArgs(sess, sqlStr, args, txn)
}

// runSQLArgs is the core executor. args are pre-converted bind values.
func (s *Service) runSQLArgs(sess *sessionRecord, sqlStr string, args []any, txn *txnRecord) (*sppb.ResultSet, error) {
	if strings.TrimSpace(sqlStr) == "" {
		return nil, errInvalidArgument("empty SQL statement")
	}
	kind := classifySQL(sqlStr)
	if kind == "ddl" {
		stmt, err := parseDDL(sqlStr)
		if err != nil {
			return nil, err
		}
		if stmt.kind == ddlOther {
			return nil, errInvalidArgument("unsupported DDL statement: %s", firstLine(sqlStr))
		}
		if err := s.engine.execDDL(sess.database, stmt); err != nil {
			return nil, err
		}
		return &sppb.ResultSet{}, nil
	}
	q, err := translateQuery(sqlStr)
	if err != nil {
		return nil, err
	}
	db, err := s.engine.dbFor(sess.database)
	if err != nil {
		return nil, err
	}
	if txn == nil || txn.pdml {
		return runStmt(db, kind, q, args)
	}
	return s.runInTxn(db, txn, kind, q, args)
}

// runStmt runs a DML statement or a query on r.
func runStmt(r sqlRunner, kind, q string, args []any) (*sppb.ResultSet, error) {
	if kind == "dml" {
		res, err := r.Exec(q, args...)
		if err != nil {
			return nil, sqlErrToGRPC(err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return nil, errInternal("rows affected: %v", err)
		}
		return dmlResultSet(n), nil
	}
	rows, meta, err := runQuery(r, q, args)
	if err != nil {
		return nil, sqlErrToGRPC(err)
	}
	return &sppb.ResultSet{Metadata: meta, Rows: rows}, nil
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
		gv, err := sqliteValue(v, types[name], "bind parameter "+name)
		if err != nil {
			return nil, err
		}
		args = append(args, sql.Named(name, gv))
	}
	return args, nil
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
func runQuery(r sqlRunner, q string, args []any) ([]*structpb.ListValue, *sppb.ResultSetMetadata, error) {
	rows, err := r.Query(q, args...)
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
	return &sppb.Type{Code: typeCodeFromDecl(ct.DatabaseTypeName())}
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
	if !validIdent.MatchString(req.GetTable()) {
		return nil, errInvalidArgument("invalid table name %q", req.GetTable())
	}
	db, err := s.engine.dbFor(sess.database)
	if err != nil {
		return nil, err
	}
	info, err := tableInfo(db, req.GetTable())
	if err != nil {
		return nil, err
	}
	if len(info.pk) == 0 {
		return nil, errInvalidArgument("table %s has no primary key", req.GetTable())
	}
	q, args, err := readToSQL(req, info)
	if err != nil {
		return nil, err
	}
	return s.execSelected(sess, req.GetTransaction(), q, args)
}

// execSelected runs a statement under a TransactionSelector and attaches
// the transaction id when the selector began one inline.
func (s *Service) execSelected(sess *sessionRecord, sel *sppb.TransactionSelector, q string, args []any) (*sppb.ResultSet, error) {
	txn, begun, err := s.resolveTxn(sess, sel)
	if err != nil {
		return nil, err
	}
	rs, err := s.runSQLArgs(sess, q, args, txn)
	if err != nil {
		if begun {
			s.dropTxn(txn.id)
		}
		return nil, err
	}
	if begun {
		withTxnID(rs, txn)
	}
	return rs, nil
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
	args, err := bindParams(req.GetParams(), req.GetParamTypes())
	if err != nil {
		return nil, err
	}
	return s.execSelected(sess, req.GetTransaction(), req.GetSql(), args)
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

// readToSQL converts a ReadRequest (table, columns, key set) into SQL with
// positional "?" placeholders.
func readToSQL(req *sppb.ReadRequest, info *tableMeta) (string, []any, error) {
	if len(req.GetColumns()) == 0 {
		return "", nil, errInvalidArgument("no columns requested")
	}
	for _, c := range req.GetColumns() {
		if !validIdent.MatchString(c) {
			return "", nil, errInvalidArgument("invalid column name %q", c)
		}
	}
	where, args, err := keySetWhere(req.GetKeySet(), info)
	if err != nil {
		return "", nil, err
	}
	if where == "" {
		where = "1=0" // empty key set matches nothing
	}
	q := fmt.Sprintf("SELECT %s FROM %s WHERE %s ORDER BY %s",
		strings.Join(quotedIdents(req.GetColumns()), ", "), quoteIdent(req.GetTable()), where,
		strings.Join(quotedIdents(info.pk), ", "))
	if req.GetLimit() > 0 {
		q += fmt.Sprintf(" LIMIT %d", req.GetLimit())
	}
	return q, args, nil
}
