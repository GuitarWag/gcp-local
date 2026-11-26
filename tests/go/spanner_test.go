package gcplocaltest

import (
	"context"
	"errors"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"cloud.google.com/go/spanner/admin/database/apiv1"
	adminpb "cloud.google.com/go/spanner/admin/database/apiv1/databasepb"
	sppb "cloud.google.com/go/spanner/apiv1/spannerpb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

const spannerDB = "projects/local-project/instances/test/databases/local-db"

// newSpannerClient connects a real cloud.google.com/go/spanner client to the
// emulator via SPANNER_EMULATOR_HOST (picked up by the SDK automatically).
func newSpannerClient(t *testing.T, em *testutil.Emulator) *spanner.Client {
	t.Helper()
	t.Setenv("SPANNER_EMULATOR_HOST", em.Host)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := spanner.NewClient(ctx, spannerDB)
	if err != nil {
		t.Fatalf("spanner client: %v", err)
	}
	t.Cleanup(client.Close)
	return client
}

func newDatabaseAdminClient(t *testing.T, em *testutil.Emulator) *database.DatabaseAdminClient {
	t.Helper()
	t.Setenv("SPANNER_EMULATOR_HOST", em.Host)
	conn, err := grpc.NewClient(em.Host, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := database.NewDatabaseAdminClient(ctx,
		option.WithGRPCConn(conn),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("database admin client: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// createSpannerDatabase creates the database plus the songs DDL and waits for
// both LROs to complete. Panics-equivalent: fails the test.
func createSpannerDatabase(t *testing.T, admin *database.DatabaseAdminClient, extra ...string) *adminpb.Database {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stmts := append([]string{`CREATE TABLE songs (
		songId INT64 NOT NULL,
		title STRING(1024),
		artist STRING(1024),
		released TIMESTAMP,
		duration INT64,
		playCount INT64,
		rating FLOAT64,
		liked BOOL,
		data BYTES(MAX),
		lyrics NUMERIC
	) PRIMARY KEY (songId)`}, extra...)
	op, err := admin.CreateDatabase(ctx, &adminpb.CreateDatabaseRequest{
		Parent:          "projects/local-project/instances/test",
		CreateStatement: "CREATE DATABASE `local-db`",
		ExtraStatements: stmts,
	})
	if err != nil {
		t.Fatalf("CreateDatabase: %v", err)
	}
	db, err := op.Wait(ctx)
	if err != nil {
		t.Fatalf("CreateDatabase wait: %v", err)
	}
	if db == nil || db.GetName() != spannerDB {
		t.Fatalf("unexpected database in LRO response: %+v", db)
	}
	return db
}

// ---------------------------------------------------------------------------
// DDL / database admin
// ---------------------------------------------------------------------------

func TestSpannerCreateDatabaseAndDDL(t *testing.T) {
	em := testutil.Start(t)
	admin := newDatabaseAdminClient(t, em)
	db := createSpannerDatabase(t, admin)
	if db.GetState() != adminpb.Database_READY {
		t.Errorf("database state = %v, want READY", db.GetState())
	}

	// Duplicate CREATE DATABASE → AlreadyExists.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := admin.CreateDatabase(ctx, &adminpb.CreateDatabaseRequest{
		Parent:          "projects/local-project/instances/test",
		CreateStatement: "CREATE DATABASE `local-db`",
	})
	if status.Code(err) != codes.AlreadyExists {
		t.Errorf("duplicate CreateDatabase code = %v, want AlreadyExists (err: %v)", status.Code(err), err)
	}

	// Second table via UpdateDatabaseDdl.
	op, err := admin.UpdateDatabaseDdl(ctx, &adminpb.UpdateDatabaseDdlRequest{
		Database: spannerDB,
		Statements: []string{`CREATE TABLE singers (
			singerId INT64 NOT NULL,
			name STRING(1024)
		) PRIMARY KEY (singerId)`},
	})
	if err != nil {
		t.Fatalf("UpdateDatabaseDdl: %v", err)
	}
	if err := op.Wait(ctx); err != nil {
		t.Fatalf("UpdateDatabaseDdl wait: %v", err)
	}

	// Duplicate CREATE TABLE → AlreadyExists.
	_, err = admin.UpdateDatabaseDdl(ctx, &adminpb.UpdateDatabaseDdlRequest{
		Database: spannerDB,
		Statements: []string{`CREATE TABLE singers (
			singerId INT64 NOT NULL,
			name STRING(1024)
		) PRIMARY KEY (singerId)`},
	})
	if status.Code(err) != codes.AlreadyExists {
		t.Errorf("duplicate table code = %v, want AlreadyExists (err: %v)", status.Code(err), err)
	}

	// DDL against a missing database → NotFound.
	_, err = admin.UpdateDatabaseDdl(ctx, &adminpb.UpdateDatabaseDdlRequest{
		Database:   "projects/local-project/instances/test/databases/nope",
		Statements: []string{"CREATE TABLE t (id INT64) PRIMARY KEY (id)"},
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("missing db DDL code = %v, want NotFound (err: %v)", status.Code(err), err)
	}
}

// ---------------------------------------------------------------------------
// Mutations: Insert / Update / Replace / InsertOrUpdate / Delete
// ---------------------------------------------------------------------------

func TestSpannerMutations(t *testing.T) {
	em := testutil.Start(t)
	admin := newDatabaseAdminClient(t, em)
	createSpannerDatabase(t, admin)
	client := newSpannerClient(t, em)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Insert.
	_, err := client.Apply(ctx, []*spanner.Mutation{
		spanner.Insert("songs",
			[]string{"songId", "title", "artist", "rating", "liked", "playCount"},
			[]any{int64(1), "Song One", "The Emulators", 4.5, true, int64(100)}),
		spanner.Insert("songs",
			[]string{"songId", "title", "artist"},
			[]any{int64(2), "Song Two", "The Emulators"}),
		spanner.Insert("songs",
			[]string{"songId", "title", "artist"},
			[]any{int64(3), "Song Three", "Third Band"}),
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Duplicate Insert → AlreadyExists.
	_, err = client.Apply(ctx, []*spanner.Mutation{
		spanner.Insert("songs", []string{"songId"}, []any{int64(1)}),
	})
	if status.Code(err) != codes.AlreadyExists {
		t.Errorf("dup insert code = %v, want AlreadyExists (err: %v)", status.Code(err), err)
	}

	// Update changes a column.
	_, err = client.Apply(ctx, []*spanner.Mutation{
		spanner.Update("songs", []string{"songId", "title"}, []any{int64(1), "Song One (remix)"}),
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	// Update of a missing row fails (real Spanner: NotFound).
	_, err = client.Apply(ctx, []*spanner.Mutation{
		spanner.Update("songs", []string{"songId", "title"}, []any{int64(999), "ghost"}),
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("update missing row code = %v, want NotFound (err: %v)", status.Code(err), err)
	}

	// InsertOrUpdate preserves non-listed columns.
	_, err = client.Apply(ctx, []*spanner.Mutation{
		spanner.InsertOrUpdate("songs", []string{"songId", "title"}, []any{int64(2), "Song Two (redux)"}),
	})
	if err != nil {
		t.Fatalf("insertOrUpdate: %v", err)
	}

	// Replace wipes unspecified columns.
	_, err = client.Apply(ctx, []*spanner.Mutation{
		spanner.Replace("songs", []string{"songId", "title"}, []any{int64(2), "Song Two (replaced)"}),
	})
	if err != nil {
		t.Fatalf("replace: %v", err)
	}

	// Delete removes rows.
	_, err = client.Apply(ctx, []*spanner.Mutation{
		spanner.Delete("songs", spanner.Key{int64(1)}),
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Verify state: song 1 gone, song 2 replaced (artist NULL), song 3 untouched.
	iter := client.Single().Query(ctx, spanner.Statement{
		SQL: "SELECT songId, title, artist FROM songs ORDER BY songId",
	})
	defer iter.Stop()
	type songRow struct {
		ID     spanner.NullInt64
		Title  spanner.NullString
		Artist spanner.NullString
	}
	got, err := readAllRows[songRow](iter, func(r *spanner.Row) (songRow, error) {
		var s songRow
		if err := r.Columns(&s.ID, &s.Title, &s.Artist); err != nil {
			return songRow{}, err
		}
		return s, nil
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	want := []songRow{
		{ID: spanner.NullInt64{Int64: 2, Valid: true}, Title: spanner.NullString{StringVal: "Song Two (replaced)", Valid: true}},
		{ID: spanner.NullInt64{Int64: 3, Valid: true}, Title: spanner.NullString{StringVal: "Song Three", Valid: true}, Artist: spanner.NullString{StringVal: "Third Band", Valid: true}},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].ID != w.ID || got[i].Title != w.Title || got[i].Artist != w.Artist {
			t.Errorf("row %d = %+v, want %+v", i, got[i], w)
		}
	}
}

// ---------------------------------------------------------------------------
// Read: key set + column list
// ---------------------------------------------------------------------------

func TestSpannerRead(t *testing.T) {
	em := testutil.Start(t)
	admin := newDatabaseAdminClient(t, em)
	createSpannerDatabase(t, admin)
	client := newSpannerClient(t, em)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	seed := []*spanner.Mutation{}
	for i := int64(1); i <= 5; i++ {
		seed = append(seed, spanner.Insert("songs",
			[]string{"songId", "title", "playCount"},
			[]any{i, timeForRowTitle(i), i * 10}))
	}
	if _, err := client.Apply(ctx, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Single-key read with explicit column list.
	row, err := client.Single().ReadRow(ctx, "songs", spanner.Key{int64(3)}, []string{"title", "playCount"})
	if err != nil {
		t.Fatalf("ReadRow: %v", err)
	}
	var title string
	var pc int64
	if err := row.Columns(&title, &pc); err != nil {
		t.Fatalf("columns: %v", err)
	}
	if title != "Song 3" || pc != 30 {
		t.Errorf("read row = (%q, %d), want (Song 3, 30)", title, pc)
	}

	// Key range read, ordered by key.
	iter := client.Single().Read(ctx, "songs", spanner.KeyRange{
		Start: spanner.Key{int64(2)},
		End:   spanner.Key{int64(4)},
		Kind:  spanner.ClosedClosed,
	}, []string{"songId"})
	defer iter.Stop()
	var ids []int64
	for {
		row, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		var id int64
		if err := row.Columns(&id); err != nil {
			t.Fatalf("columns: %v", err)
		}
		ids = append(ids, id)
	}
	want := []int64{2, 3, 4}
	if len(ids) != len(want) {
		t.Fatalf("key range ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Errorf("ids[%d] = %d, want %d", i, ids[i], want[i])
		}
	}

	// Read on a missing table → NotFound.
	_, err = client.Single().ReadRow(ctx, "ghosts", spanner.Key{int64(1)}, []string{"id"})
	if status.Code(err) != codes.NotFound {
		t.Errorf("missing table read code = %v, want NotFound (err: %v)", status.Code(err), err)
	}
}

func timeForRowTitle(i int64) string {
	return "Song " + string(rune('0'+i))
}

// ---------------------------------------------------------------------------
// Transactions: explicit begin/commit, rollback, session lifecycle
// ---------------------------------------------------------------------------

func TestSpannerTransactionsAndSessions(t *testing.T) {
	em := testutil.Start(t)
	admin := newDatabaseAdminClient(t, em)
	createSpannerDatabase(t, admin)
	client := newSpannerClient(t, em)
	conn := dialGRPC(t, em)
	srpc := sppb.NewSpannerClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Explicit read-write transaction: commit.
	_, err := client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		return txn.BufferWrite([]*spanner.Mutation{
			spanner.Insert("songs", []string{"songId", "title"}, []any{int64(10), "Committed"}),
		})
	})
	if err != nil {
		t.Fatalf("commit txn: %v", err)
	}

	// Explicit read-write transaction that rolls back: the function returns
	// an error, so the buffered mutations must not persist.
	sentinel := status.Error(codes.FailedPrecondition, "rollback please")
	_, err = client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		if err := txn.BufferWrite([]*spanner.Mutation{
			spanner.Insert("songs", []string{"songId", "title"}, []any{int64(11), "Rolled back"}),
		}); err != nil {
			return err
		}
		return sentinel
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("rollback txn err = %v, want FailedPrecondition", err)
	}

	// Raw gRPC: BeginTransaction → Rollback aborts the write.
	sess, err := srpc.CreateSession(ctx, &sppb.CreateSessionRequest{Database: spannerDB})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if sess.GetName() == "" || sess.GetCreateTime() == nil {
		t.Fatalf("session missing name/createTime: %+v", sess)
	}
	tx, err := srpc.BeginTransaction(ctx, &sppb.BeginTransactionRequest{
		Session: sess.GetName(),
		Options: &sppb.TransactionOptions{Mode: &sppb.TransactionOptions_ReadWrite_{}},
	})
	if err != nil {
		t.Fatalf("BeginTransaction: %v", err)
	}
	if len(tx.GetId()) == 0 {
		t.Fatal("empty transaction id")
	}
	_, err = srpc.Commit(ctx, &sppb.CommitRequest{
		Session:     sess.GetName(),
		Transaction: &sppb.CommitRequest_TransactionId{TransactionId: tx.GetId()},
		Mutations:   []*sppb.Mutation{grpcInsert("songs", []string{"songId", "title"}, "13", "After begin")},
	})
	if err != nil {
		t.Fatalf("commit after begin: %v", err)
	}

	// Rollback via raw gRPC aborts the pending write; a second Rollback of
	// the same id is still OK per the Spanner contract.
	sess2, err := srpc.CreateSession(ctx, &sppb.CreateSessionRequest{Database: spannerDB})
	if err != nil {
		t.Fatalf("CreateSession 2: %v", err)
	}
	tx2, err := srpc.BeginTransaction(ctx, &sppb.BeginTransactionRequest{
		Session: sess2.GetName(),
		Options: &sppb.TransactionOptions{Mode: &sppb.TransactionOptions_ReadWrite_{}},
	})
	if err != nil {
		t.Fatalf("BeginTransaction 2: %v", err)
	}
	if _, err := srpc.Rollback(ctx, &sppb.RollbackRequest{Session: sess2.GetName(), TransactionId: tx2.GetId()}); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if _, err := srpc.Rollback(ctx, &sppb.RollbackRequest{Session: sess2.GetName(), TransactionId: tx2.GetId()}); err != nil {
		t.Fatalf("Rollback twice: %v", err)
	}

	// DeleteSession then use → NotFound.
	_, err = srpc.DeleteSession(ctx, &sppb.DeleteSessionRequest{Name: sess.GetName()})
	if err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	_, err = srpc.GetSession(ctx, &sppb.GetSessionRequest{Name: sess.GetName()})
	if status.Code(err) != codes.NotFound {
		t.Errorf("deleted session code = %v, want NotFound (err: %v)", status.Code(err), err)
	}

	// Unknown session → NotFound.
	_, err = srpc.ExecuteSql(ctx, &sppb.ExecuteSqlRequest{
		Session: spannerDB + "/sessions/does-not-exist",
		Sql:     "SELECT 1",
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("unknown session code = %v, want NotFound (err: %v)", status.Code(err), err)
	}

	// Verify only committed rows exist: songId 10 and 13.
	iter := client.Single().Query(ctx, spanner.Statement{SQL: "SELECT songId FROM songs WHERE songId >= 10 ORDER BY songId"})
	defer iter.Stop()
	var ids []int64
	for {
		row, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		var id int64
		if err := row.Columns(&id); err != nil {
			t.Fatalf("columns: %v", err)
		}
		ids = append(ids, id)
	}
	if len(ids) != 2 || ids[0] != 10 || ids[1] != 13 {
		t.Errorf("committed ids = %v, want [10 13]", ids)
	}
}

// grpcInsert builds a raw protobuf Insert mutation whose values are all
// Spanner-encoded strings (valid for INT64 / STRING columns).
func grpcInsert(table string, cols []string, vals ...string) *sppb.Mutation {
	lv := &structpb.ListValue{}
	for _, v := range vals {
		lv.Values = append(lv.Values, &structpb.Value{Kind: &structpb.Value_StringValue{StringValue: v}})
	}
	return &sppb.Mutation{
		Operation: &sppb.Mutation_Insert{
			Insert: &sppb.Mutation_Write{Table: table, Columns: cols, Values: []*structpb.ListValue{lv}},
		},
	}
}

// ---------------------------------------------------------------------------
// ExecuteSql: SELECT with WHERE, params, DML row counts, typed columns
// ---------------------------------------------------------------------------

func TestSpannerExecuteSql(t *testing.T) {
	em := testutil.Start(t)
	admin := newDatabaseAdminClient(t, em)
	createSpannerDatabase(t, admin)
	client := newSpannerClient(t, em)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	seed := []*spanner.Mutation{}
	for i := int64(1); i <= 5; i++ {
		seed = append(seed, spanner.Insert("songs",
			[]string{"songId", "title", "artist", "playCount", "rating"},
			[]any{i, "Song " + itoa(i), "The Emulators", i * 10, float64(i) / 2}))
	}
	if _, err := client.Apply(ctx, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// SELECT with WHERE + ORDER BY, typed decode.
	iter := client.Single().Query(ctx, spanner.Statement{
		SQL: "SELECT songId, title, rating FROM songs WHERE playCount >= @min AND artist = @artist ORDER BY songId DESC",
		Params: map[string]any{
			"min":    int64(20),
			"artist": "The Emulators",
		},
	})
	defer iter.Stop()
	type row struct {
		ID     int64
		Title  string
		Rating float64
	}
	got, err := readAllRows[row](iter, func(r *spanner.Row) (row, error) {
		var x row
		if err := r.Columns(&x.ID, &x.Title, &x.Rating); err != nil {
			return row{}, err
		}
		return x, nil
	})
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d rows, want 4: %+v", len(got), got)
	}
	if got[0].ID != 5 || got[0].Rating != 2.5 || got[3].ID != 2 {
		t.Errorf("rows wrong: %+v", got)
	}

	// DML UPDATE returns an affected-row count.
	var updateCount int64
	if _, err := client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		n, err := txn.Update(ctx, spanner.Statement{
			SQL:    "UPDATE songs SET playCount = playCount + @n WHERE songId > @id",
			Params: map[string]any{"n": int64(1), "id": int64(3)},
		})
		updateCount = n
		return err
	}); err != nil {
		t.Fatalf("update DML: %v", err)
	}
	if updateCount != 2 {
		t.Errorf("update count = %d, want 2", updateCount)
	}

	// DML INSERT returns an affected-row count.
	var insertCount int64
	if _, err := client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		n, err := txn.Update(ctx, spanner.Statement{
			SQL: "INSERT INTO songs (songId, title) VALUES (100, 'DML insert')",
		})
		insertCount = n
		return err
	}); err != nil {
		t.Fatalf("insert DML: %v", err)
	}
	if insertCount != 1 {
		t.Errorf("insert count = %d, want 1", insertCount)
	}

	// Bad SQL → InvalidArgument.
	iter = client.Single().Query(ctx, spanner.Statement{SQL: "SELEC * FROM songs"})
	defer iter.Stop()
	_, err = iter.Next()
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("bad sql code = %v, want InvalidArgument (err: %v)", status.Code(err), err)
	}

	// Query against a missing table → NotFound.
	iter = client.Single().Query(ctx, spanner.Statement{SQL: "SELECT * FROM missing_table"})
	defer iter.Stop()
	_, err = iter.Next()
	if status.Code(err) != codes.NotFound {
		t.Errorf("missing table query code = %v, want NotFound (err: %v)", status.Code(err), err)
	}
}

func itoa(i int64) string {
	return string(rune('0' + i))
}

// ---------------------------------------------------------------------------
// ExecuteStreamingSql / StreamingRead via the real client iterator paths
// ---------------------------------------------------------------------------

func TestSpannerStreaming(t *testing.T) {
	em := testutil.Start(t)
	admin := newDatabaseAdminClient(t, em)
	createSpannerDatabase(t, admin)
	client := newSpannerClient(t, em)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	seed := []*spanner.Mutation{}
	for i := int64(1); i <= 50; i++ {
		seed = append(seed, spanner.Insert("songs",
			[]string{"songId", "title"}, []any{i, "Song " + itoa(i)}))
	}
	if _, err := client.Apply(ctx, seed); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Streaming path: client.Single().Query uses ExecuteStreamingSql under
	// the hood; count rows through the iterator.
	iter := client.Single().Query(ctx, spanner.Statement{SQL: "SELECT songId FROM songs ORDER BY songId"})
	defer iter.Stop()
	count := 0
	last := int64(0)
	for {
		row, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		var id int64
		if err := row.Columns(&id); err != nil {
			t.Fatalf("columns: %v", err)
		}
		if id <= last {
			t.Errorf("streaming order broken: %d after %d", id, last)
		}
		last = id
		count++
	}
	if count != 50 {
		t.Errorf("streaming count = %d, want 50", count)
	}

	// StreamingRead: client.Single().Read with AllKeys.
	iter = client.Single().Read(ctx, "songs", spanner.AllKeys(), []string{"songId", "title"})
	defer iter.Stop()
	count = 0
	for {
		_, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			t.Fatalf("next: %v", err)
		}
		count++
	}
	if count != 50 {
		t.Errorf("streaming read count = %d, want 50", count)
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// readAllRows drains a RowIterator into a slice via fn.
func readAllRows[T any](iter *spanner.RowIterator, fn func(*spanner.Row) (T, error)) ([]T, error) {
	defer iter.Stop()
	var out []T
	for {
		row, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		v, err := fn(row)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
}
