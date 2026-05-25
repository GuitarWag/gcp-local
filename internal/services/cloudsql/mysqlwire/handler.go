package mysqlwire

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/go-mysql-org/go-mysql/mysql"
)

// handler is one MySQL Conn's worth of state. It owns a pinned *sql.Conn
// so session-scoped things in SQLite (transactions, last_insert_rowid)
// behave consistently across packets.
type handler struct {
	ctx      context.Context
	db       *sql.DB
	database string

	mu    sync.Mutex
	conn  *sql.Conn // lazy; one per session
	stmts map[uintptr]*preparedStmt
}

type preparedStmt struct {
	translated string // sqlite-flavoured
	params     int
}

func newHandler(ctx context.Context, db *sql.DB, database string) *handler {
	return &handler{
		ctx:      ctx,
		db:       db,
		database: database,
		stmts:    map[uintptr]*preparedStmt{},
	}
}

func (h *handler) close() {
	h.mu.Lock()
	c := h.conn
	h.conn = nil
	h.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

func (h *handler) pinned() (*sql.Conn, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conn != nil {
		return h.conn, nil
	}
	c, err := h.db.Conn(h.ctx)
	if err != nil {
		return nil, err
	}
	h.conn = c
	return c, nil
}

// UseDB is called for COM_INIT_DB. We don't enforce database name matching;
// SQLite has one database and the admin layer already knows which one this
// listener serves.
func (h *handler) UseDB(_ string) error { return nil }

// HandleQuery handles COM_QUERY (text protocol, no params).
func (h *handler) HandleQuery(query string) (*mysql.Result, error) {
	return h.runQuery(query, nil, false)
}

// HandleStmtPrepare is called for COM_STMT_PREPARE.
//
// We return columns=0 — clients learn the actual column count from the
// COM_STMT_EXECUTE response. Params is computed by counting `?` outside
// of strings and identifier quotes.
func (h *handler) HandleStmtPrepare(query string) (int, int, any, error) {
	translated := translateSQL(query)
	params := countParams(translated)
	ps := &preparedStmt{translated: translated, params: params}
	return params, 0, ps, nil
}

// HandleStmtExecute is called for COM_STMT_EXECUTE.
func (h *handler) HandleStmtExecute(ctx any, query string, args []any) (*mysql.Result, error) {
	var q string
	if ps, ok := ctx.(*preparedStmt); ok {
		q = ps.translated
	} else {
		q = translateSQL(query)
	}
	return h.runQuery(q, args, true)
}

func (h *handler) HandleStmtClose(_ any) error { return nil }

func (h *handler) HandleFieldList(_ string, _ string) ([]*mysql.Field, error) {
	return nil, fmt.Errorf("COM_FIELD_LIST not supported")
}

func (h *handler) HandleOtherCommand(cmd byte, _ []byte) error {
	return mysql.NewError(mysql.ER_UNKNOWN_ERROR, fmt.Sprintf("command %d not supported", cmd))
}

// runQuery handles both text and binary protocol queries. If binary is true,
// resultsets are encoded for binary protocol consumption (COM_STMT_EXECUTE).
func (h *handler) runQuery(query string, args []any, binary bool) (*mysql.Result, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return mysql.NewResultReserveResultset(0), nil
	}

	// Built-in fast paths for queries the driver issues before the user's SQL.
	if r, ok, err := h.tryBuiltin(q, binary); ok {
		return r, err
	}

	tq := translateSQL(q)
	if binary {
		// Already translated upstream by HandleStmtExecute; avoid double work.
		tq = q
	}
	// Convert any TypedBytes binary-protocol params to plain Go types sqlite
	// can bind.
	driverArgs := convertArgs(args)

	if isSelectish(tq) {
		return h.runSelect(tq, driverArgs, binary)
	}
	return h.runExec(tq, driverArgs)
}

func (h *handler) runSelect(query string, args []any, binary bool) (*mysql.Result, error) {
	conn, err := h.pinned()
	if err != nil {
		return nil, err
	}
	rows, err := conn.QueryContext(h.ctx, query, args...)
	if err != nil {
		return nil, mysqlErr(err)
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return nil, mysqlErr(err)
	}
	values := make([][]any, 0)
	for rows.Next() {
		holders := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range holders {
			ptrs[i] = &holders[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, mysqlErr(err)
		}
		row := make([]any, len(cols))
		for i, v := range holders {
			row[i] = normaliseScan(v)
		}
		values = append(values, row)
	}
	if err := rows.Err(); err != nil {
		return nil, mysqlErr(err)
	}
	rs, err := mysql.BuildSimpleResultset(cols, values, binary)
	if err != nil {
		return nil, err
	}
	return mysql.NewResult(rs), nil
}

func (h *handler) runExec(query string, args []any) (*mysql.Result, error) {
	conn, err := h.pinned()
	if err != nil {
		return nil, err
	}
	res, err := conn.ExecContext(h.ctx, query, args...)
	if err != nil {
		return nil, mysqlErr(err)
	}
	r := mysql.NewResultReserveResultset(0)
	if id, err := res.LastInsertId(); err == nil && id > 0 {
		r.InsertId = uint64(id)
	}
	if n, err := res.RowsAffected(); err == nil && n >= 0 {
		r.AffectedRows = uint64(n)
	}
	return r, nil
}

// normaliseScan reduces driver-returned interface values to a small set of
// Go types BuildSimpleResultset knows how to encode.
func normaliseScan(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case []byte:
		// SQLite returns text columns as []byte; pass through. Callers
		// looking at "TEXT" affinity get a string when they Scan into one.
		return string(x)
	case bool:
		if x {
			return int64(1)
		}
		return int64(0)
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64, string:
		return v
	default:
		return fmt.Sprint(v)
	}
}

// mysqlErr wraps a sqlite/driver error in a MySQL ER_UNKNOWN_ERROR so the
// client sees something useful. The library writes it back as an ErrPacket.
func mysqlErr(err error) error {
	if err == nil {
		return nil
	}
	// Already a MySQL error: pass through unchanged.
	var me *mysql.MyError
	if errors.As(err, &me) {
		return err
	}
	return mysql.NewError(mysql.ER_UNKNOWN_ERROR, err.Error())
}
