package spanner

import (
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"sync"

	_ "modernc.org/sqlite" // sqlite driver registered as "sqlite" with database/sql
)

// engine manages one in-memory SQLite database per Spanner database path
// (projects/P/instances/I/databases/D). It mirrors bigquery's ":memory:"
// pattern: a single connection per database so in-memory state survives
// across queries on the same handle.
type engine struct {
	mu   sync.Mutex
	dbs  map[string]*sql.DB
	ddls map[string][]string // DDL log per database
}

var validIdent = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func newEngine() *engine {
	return &engine{dbs: make(map[string]*sql.DB), ddls: make(map[string][]string)}
}

// dbFor returns the SQLite handle for a Spanner database path, creating it
// if needed. Unknown databases are created on demand (matching the emulator,
// which auto-creates on first use) except where callers check first.
func (e *engine) dbFor(database string) (*sql.DB, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if db, ok := e.dbs[database]; ok {
		return db, nil
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, err
	}
	// One connection per in-memory database keeps all tables and rows in
	// the same shared state (":memory:" is per-connection otherwise).
	db.SetMaxOpenConns(1)
	e.dbs[database] = db
	return db, nil
}

// hasDatabase reports whether the database was explicitly created.
func (e *engine) hasDatabase(database string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.dbs[database]
	return ok
}

// createDatabase registers a new database. Returns AlreadyExists-shaped
// error if present.
func (e *engine) createDatabase(database string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.dbs[database]; ok {
		return errAlreadyExists("Database already exists: %s", database)
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return errInternal("%v", err)
	}
	db.SetMaxOpenConns(1)
	e.dbs[database] = db
	return nil
}

// execDDL applies a parsed DDL statement to the database's SQLite schema.
func (e *engine) execDDL(database string, stmt ddlStatement) error {
	db, err := e.dbFor(database)
	if err != nil {
		return err
	}
	switch stmt.kind {
	case ddlCreateTable:
		if err := e.checkTableExists(db, stmt.table, true); err != nil {
			return err
		}
		cols, pk := sqliteColumns(stmt)
		// SQLite requires the PRIMARY KEY table-constraint inside the parens.
		q := fmt.Sprintf("CREATE TABLE %s (%s)", quoteIdent(stmt.table), strings.Join(cols, ", "))
		if len(pk) > 0 {
			q = fmt.Sprintf("CREATE TABLE %s (%s, PRIMARY KEY (%s))", quoteIdent(stmt.table), strings.Join(cols, ", "), strings.Join(pk, ", "))
		}
		if _, err := db.Exec(q); err != nil {
			return errInternal("creating table %s: %v", stmt.table, err)
		}
		return nil
	case ddlAlterTable:
		for _, col := range stmt.addColumn {
			q := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", quoteIdent(stmt.table), col)
			if _, err := db.Exec(q); err != nil {
				return errInternal("alter table %s: %v", stmt.table, err)
			}
		}
		return nil
	case ddlDropTable:
		if err := e.checkTableExists(db, stmt.table, false); err != nil {
			return err
		}
		if _, err := db.Exec(fmt.Sprintf("DROP TABLE %s", quoteIdent(stmt.table))); err != nil {
			return errInternal("drop table %s: %v", stmt.table, err)
		}
		return nil
	}
	return errInvalidArgument("unsupported DDL kind")
}

func (e *engine) checkTableExists(db *sql.DB, table string, wantMissing bool) error {
	row := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", strings.ToLower(table))
	var name string
	err := row.Scan(&name)
	exists := err == nil
	if wantMissing && exists {
		return errAlreadyExists("Table already exists: %s", table)
	}
	if !wantMissing && !exists {
		return errNotFound("Table not found: %s", table)
	}
	return nil
}

// dropDatabase removes a database and its DDL log. An unknown database is
// NotFound, matching the real service.
func (e *engine) dropDatabase(database string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	db, ok := e.dbs[database]
	if !ok {
		return errNotFound("Database not found: %s", database)
	}
	_ = db.Close()
	delete(e.dbs, database)
	delete(e.ddls, database)
	return nil
}

// recordDDL appends a successful DDL statement to the database's log.
func (e *engine) recordDDL(database, stmt string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ddls[database] = append(e.ddls[database], stmt)
}

// ddlLog returns the recorded DDL statements for a database.
func (e *engine) ddlLog(database string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.ddls[database]))
	copy(out, e.ddls[database])
	return out
}
