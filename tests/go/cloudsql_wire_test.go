package gcplocaltest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
	"github.com/jackc/pgx/v5"
)

// TestCloudSQLPostgresWire exercises the pg wire shim end to end: create an
// instance via the admin API, read the assigned port, then drive CRUD via
// pgx (which uses the extended query protocol by default).
func TestCloudSQLPostgresWire(t *testing.T) {
	em := testutil.Start(t)

	base := "http://" + em.Host + "/sql/v1beta4/projects/" + project + "/instances"
	resp, body := doJSON(t, http.MethodPost, base, map[string]any{
		"name":            "wire-test",
		"databaseVersion": "POSTGRES_15",
		"engine":          "sqlite",
		"database":        "appdb",
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

	dsn := fmt.Sprintf("postgres://gcp-local:local@%s:%d/%s?sslmode=disable", inst.Host, inst.Port, "appdb")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("pgx connect: %v", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, `CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, age INTEGER)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO users (id, name, age) VALUES ($1, $2, $3)`, 1, "alice", 30); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO users (id, name, age) VALUES ($1, $2, $3)`, 2, "bob", 25); err != nil {
		t.Fatalf("insert 2: %v", err)
	}

	var got struct {
		ID   int64
		Name string
		Age  int64
	}
	if err := conn.QueryRow(ctx, `SELECT id, name, age FROM users WHERE id = $1`, 1).Scan(&got.ID, &got.Name, &got.Age); err != nil {
		t.Fatalf("select: %v", err)
	}
	if got.ID != 1 || got.Name != "alice" || got.Age != 30 {
		t.Errorf("row mismatch: %+v", got)
	}

	tag, err := conn.Exec(ctx, `UPDATE users SET age = $1 WHERE name = $2`, 31, "alice")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Errorf("update rows = %d, want 1", tag.RowsAffected())
	}

	rows, err := conn.Query(ctx, `SELECT name FROM users ORDER BY id`)
	if err != nil {
		t.Fatalf("select all: %v", err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("scan: %v", err)
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}
	if len(names) != 2 || names[0] != "alice" || names[1] != "bob" {
		t.Errorf("names = %v", names)
	}

	tag, err = conn.Exec(ctx, `DELETE FROM users WHERE id = $1`, 2)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Errorf("delete rows = %d, want 1", tag.RowsAffected())
	}
}
