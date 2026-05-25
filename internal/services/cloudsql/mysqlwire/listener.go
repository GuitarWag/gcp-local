package mysqlwire

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/go-mysql-org/go-mysql/server"
)

// Listener accepts MySQL wire protocol connections for one CloudSQL instance
// and serves them against a sqlite-backed database/sql handle.
//
// Auth is fixed to user `gcp-local` / password `local`, matching the
// credentials emitted in the admin REST response, so tests don't need to
// negotiate them out of band.
type Listener struct {
	name     string
	database string
	addr     string
	db       *sql.DB

	user     string
	password string

	srv *server.Server

	mu  sync.Mutex
	ln  net.Listener
	wg  sync.WaitGroup
	ctx context.Context
	cn  context.CancelFunc
}

// User/password the emulator accepts. The pgwire shim uses the same pair.
const (
	DefaultUser     = "gcp-local"
	DefaultPassword = "local"
)

// NewListener constructs but does not start a listener. db must be ready;
// the listener takes ownership and closes it on Stop.
func NewListener(name, database, addr string, db *sql.DB) *Listener {
	return &Listener{
		name:     name,
		database: database,
		addr:     addr,
		db:       db,
		user:     DefaultUser,
		password: DefaultPassword,
		srv:      server.NewServer("8.0.11", 33, "mysql_native_password", nil, nil),
	}
}

// Start binds the TCP socket and serves connections in a background
// goroutine. Returns the actual bound address.
func (l *Listener) Start() (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ln != nil {
		return l.ln.Addr().String(), nil
	}
	ln, err := net.Listen("tcp", l.addr)
	if err != nil {
		return "", fmt.Errorf("mysqlwire listen %s: %w", l.addr, err)
	}
	l.ln = ln
	l.ctx, l.cn = context.WithCancel(context.Background())
	l.wg.Add(1)
	go l.acceptLoop()
	return ln.Addr().String(), nil
}

// Addr returns the bound address. Empty if not started.
func (l *Listener) Addr() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ln == nil {
		return ""
	}
	return l.ln.Addr().String()
}

// Stop closes the listener and waits for connection goroutines.
func (l *Listener) Stop() error {
	l.mu.Lock()
	if l.ln == nil {
		l.mu.Unlock()
		return nil
	}
	ln := l.ln
	l.ln = nil
	cn := l.cn
	l.mu.Unlock()
	cn()
	err := ln.Close()
	l.wg.Wait()
	if l.db != nil {
		_ = l.db.Close()
	}
	return err
}

func (l *Listener) acceptLoop() {
	defer l.wg.Done()
	for {
		conn, err := l.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || l.ctx.Err() != nil {
				return
			}
			continue
		}
		l.wg.Add(1)
		go l.serve(conn)
	}
}

func (l *Listener) serve(c net.Conn) {
	defer l.wg.Done()
	defer func() { _ = c.Close() }()

	h := newHandler(l.ctx, l.db, l.database)
	defer h.close()

	authH := server.NewInMemoryAuthenticationHandler("mysql_native_password")
	if err := authH.AddUser(l.user, l.password); err != nil {
		return
	}
	conn, err := l.srv.NewCustomizedConn(c, authH, h)
	if err != nil {
		return
	}
	for {
		if l.ctx.Err() != nil {
			return
		}
		if err := conn.HandleCommand(); err != nil {
			return
		}
		if conn.Closed() {
			return
		}
	}
}
