package pgwire

import (
	"context"

	"github.com/jackc/pgproto3/v2"
)

// Extended Query Protocol: Parse / Bind / Describe / Execute / Sync.
// We translate the SQL once at Parse time, bind parameters at Bind, and
// run the query at Execute. Statements/portals named "" are the unnamed
// (per-message) slots clients reuse for one-off queries.

func (s *session) handleParse(m *pgproto3.Parse) {
	translated, order := translate(m.Query)
	s.stmts[m.Name] = &preparedStmt{
		rawQuery:   translated,
		paramOIDs:  append([]uint32{}, m.ParameterOIDs...),
		paramOrder: order,
	}
	_ = s.be.Send(&pgproto3.ParseComplete{})
}

func (s *session) handleBind(m *pgproto3.Bind) {
	stmt, ok := s.stmts[m.PreparedStatement]
	if !ok {
		s.sendErr("26000", "prepared statement does not exist: "+m.PreparedStatement)
		return
	}
	s.portals[m.DestinationPortal] = &portal{
		stmt:       stmt,
		params:     m.Parameters,
		paramFmts:  m.ParameterFormatCodes,
		resultFmts: m.ResultFormatCodes,
	}
	_ = s.be.Send(&pgproto3.BindComplete{})
}

func (s *session) handleDescribe(ctx context.Context, m *pgproto3.Describe) {
	switch m.ObjectType {
	case 'S':
		stmt, ok := s.stmts[m.Name]
		if !ok {
			s.sendErr("26000", "prepared statement does not exist: "+m.Name)
			return
		}
		oids := inferParamOIDs(stmt)
		_ = s.be.Send(&pgproto3.ParameterDescription{ParameterOIDs: oids})
		s.describeRowOrNoData(ctx, stmt)
	case 'P':
		p, ok := s.portals[m.Name]
		if !ok {
			s.sendErr("26000", "portal does not exist: "+m.Name)
			return
		}
		s.describeRowOrNoData(ctx, p.stmt)
	}
}

func (s *session) describeRowOrNoData(_ context.Context, stmt *preparedStmt) {
	if !isSelectish(stmt.rawQuery) {
		_ = s.be.Send(&pgproto3.NoData{})
		return
	}
	// We don't have column types until Execute (sqlite gives them via Rows).
	// Send a NoData here; clients that need typed columns (pgx with
	// QueryPreparedStatement) will accept a RowDescription emitted later as
	// part of the Execute response. This matches how the simple flow works.
	_ = s.be.Send(&pgproto3.NoData{})
}

func (s *session) handleExecute(ctx context.Context, m *pgproto3.Execute) {
	p, ok := s.portals[m.Portal]
	if !ok {
		s.sendErr("26000", "portal does not exist: "+m.Portal)
		return
	}
	// First decode each pg parameter once.
	decoded := make([]any, len(p.params))
	for i, raw := range p.params {
		var fmtCode int16
		switch {
		case len(p.paramFmts) == 0:
			fmtCode = 0
		case len(p.paramFmts) == 1:
			fmtCode = p.paramFmts[0]
		case i < len(p.paramFmts):
			fmtCode = p.paramFmts[i]
		}
		var oid uint32
		if i < len(p.stmt.paramOIDs) {
			oid = p.stmt.paramOIDs[i]
		}
		decoded[i] = decodeParam(raw, fmtCode, oid)
	}
	// Then expand them in the order the translated SQL expects.
	order := p.stmt.paramOrder
	if len(order) == 0 {
		order = make([]int, len(decoded))
		for i := range order {
			order[i] = i + 1
		}
	}
	args := make([]any, len(order))
	for i, idx := range order {
		if idx < 1 || idx > len(decoded) {
			s.sendErr("42P02", "bind parameter index out of range")
			return
		}
		args[i] = decoded[idx-1]
	}
	conn, err := s.pinnedConn(ctx)
	if err != nil {
		s.sendErr("08000", "cannot acquire connection: "+err.Error())
		return
	}
	if isSelectish(p.stmt.rawQuery) {
		s.execSelect(ctx, conn, p.stmt.rawQuery, args, p.resultFmts)
		return
	}
	s.execNonSelect(ctx, conn, p.stmt.rawQuery, args)
}

// inferParamOIDs returns one OID per pg parameter index referenced by the
// prepared statement, honouring any OIDs the client declared in Parse and
// leaving the rest as 0 (= unspecified). With OID 0, clients like pgx pick
// the type based on the Go value they're encoding, which is exactly what we
// want since SQLite is dynamically typed.
func inferParamOIDs(stmt *preparedStmt) []uint32 {
	max := 0
	for _, idx := range stmt.paramOrder {
		if idx > max {
			max = idx
		}
	}
	if max < len(stmt.paramOIDs) {
		max = len(stmt.paramOIDs)
	}
	out := make([]uint32, max)
	for i := 0; i < max; i++ {
		if i < len(stmt.paramOIDs) {
			out[i] = stmt.paramOIDs[i]
		}
	}
	return out
}

func (s *session) handleClose(m *pgproto3.Close) {
	switch m.ObjectType {
	case 'S':
		delete(s.stmts, m.Name)
	case 'P':
		delete(s.portals, m.Name)
	}
	_ = s.be.Send(&pgproto3.CloseComplete{})
}
