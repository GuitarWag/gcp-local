package pgwire

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgproto3/v2"
)

// session owns one client connection and the per-session prepared statement
// + portal state needed for the extended query protocol.
type session struct {
	conn     net.Conn
	be       *pgproto3.Backend
	db       *sql.DB
	conn1    *sql.Conn // single pinned connection; needed for sqlite tx + temp tables
	database string

	// extended-query state — names "" mean unnamed statement/portal.
	stmts   map[string]*preparedStmt
	portals map[string]*portal
}

type preparedStmt struct {
	rawQuery   string   // SQLite-flavoured SQL, post-translate
	paramOIDs  []uint32 // declared by client (may be 0 = unknown)
	paramOrder []int    // for each `?` in rawQuery: which 1-based pg param feeds it
}

type portal struct {
	stmt       *preparedStmt
	params     [][]byte
	paramFmts  []int16 // length 0 → all text; length 1 → applies to all; else per-param
	resultFmts []int16 // length 0 → all text; length 1 → applies to all; else per-col
}

func newSession(conn net.Conn, db *sql.DB, database string) *session {
	return &session{
		conn:     conn,
		be:       pgproto3.NewBackend(pgproto3.NewChunkReader(conn), conn),
		db:       db,
		database: database,
		stmts:    map[string]*preparedStmt{},
		portals:  map[string]*portal{},
	}
}

// serve runs the per-connection state machine: handshake, then a loop of
// frontend messages until termination or error.
func (s *session) serve(ctx context.Context) {
	if err := s.handshake(ctx); err != nil {
		return
	}
	defer func() {
		if s.conn1 != nil {
			_ = s.conn1.Close()
		}
	}()
	for {
		if ctx.Err() != nil {
			return
		}
		_ = s.conn.SetDeadline(time.Time{})
		msg, err := s.be.Receive()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return
			}
			return
		}
		if !s.dispatch(ctx, msg) {
			return
		}
	}
}

// dispatch returns false when the session should terminate.
func (s *session) dispatch(ctx context.Context, msg pgproto3.FrontendMessage) bool {
	switch m := msg.(type) {
	case *pgproto3.Query:
		s.handleQuery(ctx, m.String)
	case *pgproto3.Parse:
		s.handleParse(m)
	case *pgproto3.Bind:
		s.handleBind(m)
	case *pgproto3.Describe:
		s.handleDescribe(ctx, m)
	case *pgproto3.Execute:
		s.handleExecute(ctx, m)
	case *pgproto3.Sync:
		s.sendReadyForQuery()
	case *pgproto3.Close:
		s.handleClose(m)
	case *pgproto3.Flush:
		// no-op; pgproto3 flushes on every Send
	case *pgproto3.Terminate:
		return false
	default:
		s.sendErr("0A000", "feature not supported: "+strings.TrimSpace(typeName(msg)))
		s.sendReadyForQuery()
	}
	return true
}

func typeName(v any) string {
	if v == nil {
		return "<nil>"
	}
	t := reflectType(v)
	return t
}

// reflectType returns a short type name without importing reflect twice.
func reflectType(v any) string {
	// Cheap printf-style avoidance: cast via fmt is fine but adds an import.
	// For the unsupported-message path it doesn't matter — return a fixed
	// string. Clients will see the SQLSTATE in any case.
	_ = v
	return "frontend message"
}

// pinnedConn lazily acquires a single connection from the pool. Reusing one
// pgx connection across statements is required for sqlite session state
// (temp tables, BEGIN/COMMIT, last_insert_rowid).
func (s *session) pinnedConn(ctx context.Context) (*sql.Conn, error) {
	if s.conn1 != nil {
		return s.conn1, nil
	}
	c, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	s.conn1 = c
	return c, nil
}

func (s *session) sendReadyForQuery() {
	_ = s.be.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
}

func (s *session) sendErr(code, msg string) {
	_ = s.be.Send(&pgproto3.ErrorResponse{
		Severity: "ERROR",
		Code:     code,
		Message:  msg,
	})
}
