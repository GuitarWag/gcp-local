package pgwire

import (
	"context"
	"database/sql"
	"strings"

	"github.com/jackc/pgproto3/v2"
)

// handleQuery serves the Simple Query Protocol: one Query message can
// contain several `;`-separated statements; for each, we emit either a
// RowDescription+DataRows+CommandComplete sequence (SELECT-like) or just a
// CommandComplete (DML/DDL). A single ErrorResponse aborts the rest of the
// batch, matching real Postgres behaviour.
func (s *session) handleQuery(ctx context.Context, raw string) {
	stmts := splitCommands(raw)
	if len(stmts) == 0 {
		_ = s.be.Send(&pgproto3.EmptyQueryResponse{})
		s.sendReadyForQuery()
		return
	}
	conn, err := s.pinnedConn(ctx)
	if err != nil {
		s.sendErr("08000", "cannot acquire connection: "+err.Error())
		s.sendReadyForQuery()
		return
	}
	for _, stmt := range stmts {
		translated := translateSQL(stmt)
		if isSelectish(translated) {
			if !s.execSelect(ctx, conn, translated, nil, nil) {
				break
			}
		} else {
			if !s.execNonSelect(ctx, conn, translated, nil) {
				break
			}
		}
	}
	s.sendReadyForQuery()
}

// execSelect runs a query that returns rows and pushes RowDescription +
// DataRow* + CommandComplete to the client. Returns false on error (the
// caller stops the batch).
func (s *session) execSelect(ctx context.Context, conn *sql.Conn, sqlStr string, args []any, resultFmts []int16) bool {
	rows, err := conn.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		s.sendErr("42000", err.Error())
		return false
	}
	defer rows.Close()

	cols, err := rows.ColumnTypes()
	if err != nil {
		s.sendErr("42000", err.Error())
		return false
	}
	fields := make([]pgproto3.FieldDescription, len(cols))
	for i, c := range cols {
		fields[i] = pgproto3.FieldDescription{
			Name:         []byte(c.Name()),
			DataTypeOID:  sqliteTypeToOID(c.DatabaseTypeName()),
			DataTypeSize: -1,
			TypeModifier: -1,
			Format:       0,
		}
	}
	_ = s.be.Send(&pgproto3.RowDescription{Fields: fields})

	dest := make([]any, len(cols))
	scanPtrs := make([]any, len(cols))
	for i := range dest {
		scanPtrs[i] = &dest[i]
	}
	var count int64
	for rows.Next() {
		if err := rows.Scan(scanPtrs...); err != nil {
			s.sendErr("42000", err.Error())
			return false
		}
		vals := make([][]byte, len(cols))
		for i, v := range dest {
			vals[i] = encodeText(v)
		}
		_ = s.be.Send(&pgproto3.DataRow{Values: vals})
		count++
	}
	if err := rows.Err(); err != nil {
		s.sendErr("42000", err.Error())
		return false
	}
	_ = s.be.Send(&pgproto3.CommandComplete{CommandTag: []byte(commandTag(sqlStr, count))})
	_ = resultFmts // currently text-only for both protocols
	return true
}

// execNonSelect runs DDL/DML and emits a CommandComplete.
func (s *session) execNonSelect(ctx context.Context, conn *sql.Conn, sqlStr string, args []any) bool {
	res, err := conn.ExecContext(ctx, sqlStr, args...)
	if err != nil {
		s.sendErr("42000", err.Error())
		return false
	}
	var n int64
	if res != nil {
		if v, e := res.RowsAffected(); e == nil {
			n = v
		}
	}
	tag := commandTag(sqlStr, n)
	if strings.HasPrefix(tag, "INSERT") {
		if n == 0 {
			tag = "INSERT 0 0"
		}
	}
	_ = s.be.Send(&pgproto3.CommandComplete{CommandTag: []byte(tag)})
	return true
}
