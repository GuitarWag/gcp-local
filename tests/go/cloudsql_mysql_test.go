package gcplocaltest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
	_ "github.com/go-sql-driver/mysql"
)

// TestCloudSQLMySQLWire exercises the MySQL wire shim end to end: create a
// mysql-engine instance via the admin API, then drive CRUD + a transaction
// over the real mysql wire protocol with go-sql-driver.
func TestCloudSQLMySQLWire(t *testing.T) {
	em := testutil.Start(t)

	base := "http://" + em.Host + "/sql/v1beta4/projects/" + project + "/instances"
	resp, body := doJSON(t, http.MethodPost, base, map[string]any{
		"name":     "mysql-wire-test",
		"engine":   "mysql",
		"database": "appdb",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create: %d %s", resp.StatusCode, body)
	}
	var inst struct {
		Name string `json:"name"`
		Port int    `json:"port"`
		Host string `json:"host"`
	}
	if err := json.Unmarshal(body, &inst); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if inst.Port == 0 || inst.Host == "" {
		t.Fatalf("instance missing host/port: %+v", inst)
	}

	dsn := fmt.Sprintf("gcp-local:local@tcp(%s:%d)/%s?parseTime=true", inst.Host, inst.Port, "appdb")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}

	if _, err := db.ExecContext(ctx, `CREATE TABLE users (id INT PRIMARY KEY, name VARCHAR(100), age INT) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO users (id, name, age) VALUES (?, ?, ?)`, 1, "alice", 30); err != nil {
		t.Fatalf("insert 1: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO users (id, name, age) VALUES (?, ?, ?)`, 2, "bob", 25); err != nil {
		t.Fatalf("insert 2: %v", err)
	}

	var got struct {
		ID   int64
		Name string
		Age  int64
	}
	if err := db.QueryRowContext(ctx, `SELECT id, name, age FROM users WHERE id = ?`, 1).Scan(&got.ID, &got.Name, &got.Age); err != nil {
		t.Fatalf("select: %v", err)
	}
	if got.ID != 1 || got.Name != "alice" || got.Age != 30 {
		t.Errorf("row mismatch: %+v", got)
	}

	res, err := db.ExecContext(ctx, `UPDATE users SET age = ? WHERE name = ?`, 31, "alice")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Errorf("update rows = %d, want 1", n)
	}

	rows, err := db.QueryContext(ctx, `SELECT name FROM users ORDER BY id`)
	if err != nil {
		t.Fatalf("select all: %v", err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
		names = append(names, n)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}
	if len(names) != 2 || names[0] != "alice" || names[1] != "bob" {
		t.Errorf("names = %v", names)
	}

	res, err = db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, 2)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Errorf("delete rows = %d, want 1", n)
	}

	// Transaction: insert two rows, then roll back, then verify they're gone.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, name, age) VALUES (?, ?, ?)`, 10, "carol", 40); err != nil {
		_ = tx.Rollback()
		t.Fatalf("tx insert: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE id = ?`, 10).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("rollback ineffective, count = %d", count)
	}

	// Translation must skip string literals: a value containing `ENGINE=`
	// should land in the row verbatim instead of being stripped.
	if _, err := db.ExecContext(ctx, `CREATE TABLE logs (id INT PRIMARY KEY, msg VARCHAR(200))`); err != nil {
		t.Fatalf("create logs: %v", err)
	}
	const literal = "ENGINE=InnoDB failed: BIGINT overflow"
	if _, err := db.ExecContext(ctx, `INSERT INTO logs (id, msg) VALUES (?, ?)`, 1, literal); err != nil {
		t.Fatalf("insert literal: %v", err)
	}
	var roundTrip string
	if err := db.QueryRowContext(ctx, `SELECT msg FROM logs WHERE id = ?`, 1).Scan(&roundTrip); err != nil {
		t.Fatalf("select literal: %v", err)
	}
	if roundTrip != literal {
		t.Errorf("literal round-trip mismatch: got %q want %q", roundTrip, literal)
	}

	// And SQL comments: a `-- BIGINT ...` comment in the middle of a CREATE
	// must not have its `BIGINT` rewritten — sqlite happily ignores it.
	if _, err := db.ExecContext(ctx, "CREATE TABLE notes (\n  id INT PRIMARY KEY,\n  -- BIGINT not yet supported here\n  body /* keep BIGINT untouched */ TEXT\n)"); err != nil {
		t.Fatalf("create notes with comments: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO notes (id, body) VALUES (?, ?)`, 1, "hello"); err != nil {
		t.Fatalf("insert into notes: %v", err)
	}

	// `AUTO_INCREMENT=N` is a table-option (start value). It must be
	// stripped — leaving it as `AUTOINCREMENT=100` would be a sqlite parse
	// error. Real-world mysqldump output emits this on every table.
	if _, err := db.ExecContext(ctx, `CREATE TABLE counters (id INT PRIMARY KEY, label VARCHAR(50)) ENGINE=InnoDB AUTO_INCREMENT=100`); err != nil {
		t.Fatalf("create counters with AUTO_INCREMENT table option: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO counters (id, label) VALUES (?, ?)`, 100, "first"); err != nil {
		t.Fatalf("insert into counters: %v", err)
	}

	// Inline `INT AUTO_INCREMENT PRIMARY KEY` is the canonical MySQL idiom
	// for an auto-incrementing primary key. SQLite needs the equivalent
	// `INTEGER PRIMARY KEY AUTOINCREMENT`; the translator must reorder.
	// Verify by inserting without specifying id and seeing it assigned.
	if _, err := db.ExecContext(ctx, `CREATE TABLE seq_inline (id INT AUTO_INCREMENT PRIMARY KEY, label VARCHAR(50))`); err != nil {
		t.Fatalf("create seq_inline: %v", err)
	}
	res, insertErr := db.ExecContext(ctx, `INSERT INTO seq_inline (label) VALUES (?)`, "alpha")
	if insertErr != nil {
		t.Fatalf("insert seq_inline alpha: %v", insertErr)
	}
	firstID, lidErr := res.LastInsertId()
	if lidErr != nil || firstID == 0 {
		t.Fatalf("seq_inline LastInsertId: id=%d err=%v", firstID, lidErr)
	}

	// The reverse word order `PRIMARY KEY AUTO_INCREMENT` must also work.
	if _, err := db.ExecContext(ctx, `CREATE TABLE seq_reverse (id INT PRIMARY KEY AUTO_INCREMENT, label VARCHAR(50))`); err != nil {
		t.Fatalf("create seq_reverse: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO seq_reverse (label) VALUES (?)`, "beta"); err != nil {
		t.Fatalf("insert seq_reverse: %v", err)
	}

	// mysqldump style: AUTO_INCREMENT on a column with PRIMARY KEY as a
	// separate constraint. SQLite can't auto-increment without inline PK,
	// but the table must at least be creatable; the translator drops the
	// orphan AUTO_INCREMENT so the CREATE doesn't error out.
	if _, err := db.ExecContext(ctx, `CREATE TABLE dump_style (id INT NOT NULL AUTO_INCREMENT, label VARCHAR(50), PRIMARY KEY (id)) ENGINE=InnoDB`); err != nil {
		t.Fatalf("create dump_style: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO dump_style (id, label) VALUES (?, ?)`, 1, "gamma"); err != nil {
		t.Fatalf("insert dump_style: %v", err)
	}

	// Inner parens (DECIMAL precision) must not reset the per-column
	// state — `id INT AUTO_INCREMENT` followed by another column with a
	// parenthesised type, then PRIMARY KEY on a third column, must not
	// confuse the depth tracking.
	if _, err := db.ExecContext(ctx, `CREATE TABLE seq_with_decimal (id INT AUTO_INCREMENT PRIMARY KEY, amount DECIMAL(10,2), label VARCHAR(50))`); err != nil {
		t.Fatalf("create seq_with_decimal: %v", err)
	}

	// BIGINT is rewritten to INTEGER by typeReplacements; the AUTO_INCREMENT
	// reorder has to play nicely with the type rewrite for the canonical
	// `BIGINT AUTO_INCREMENT PRIMARY KEY` to land as `INTEGER PRIMARY KEY
	// AUTOINCREMENT`. Without that, the dominant MySQL idiom for large
	// primary keys would fail.
	if _, err := db.ExecContext(ctx, `CREATE TABLE seq_big (id BIGINT AUTO_INCREMENT PRIMARY KEY, label VARCHAR(50))`); err != nil {
		t.Fatalf("create seq_big: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO seq_big (label) VALUES (?)`, "delta"); err != nil {
		t.Fatalf("insert seq_big: %v", err)
	}

	// Verify the postgres and mysql engines coexist in the same emulator.
	resp2, body2 := doJSON(t, http.MethodPost, base, map[string]any{
		"name":     "pg-coexist",
		"engine":   "sqlite",
		"database": "pgdb",
	})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("create coexisting postgres-engine instance: %d %s", resp2.StatusCode, body2)
	}

	// The `postgres` engine is documented as unimplemented and must be
	// rejected — silently routing it through pgwire would mislead callers.
	resp3, body3 := doJSON(t, http.MethodPost, base, map[string]any{
		"name":     "pg-real",
		"engine":   "postgres",
		"database": "pgdb",
	})
	if resp3.StatusCode == http.StatusOK {
		t.Fatalf("postgres engine should be rejected, got 200 %s", body3)
	}
}
