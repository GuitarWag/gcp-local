package gcplocaltest

import (
	"context"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	adminpb "cloud.google.com/go/spanner/admin/database/apiv1/databasepb"
	sppb "cloud.google.com/go/spanner/apiv1/spannerpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

// songExists reports whether a songs row with the given id is committed.
func songExists(t *testing.T, client *spanner.Client, id int64) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := client.Single().ReadRow(ctx, "songs", spanner.Key{id}, []string{"title"})
	if spanner.ErrCode(err) == codes.NotFound {
		return false
	}
	if err != nil {
		t.Fatalf("ReadRow(%d): %v", id, err)
	}
	return true
}

func TestSpannerTxnDMLRollbackAndCommit(t *testing.T) {
	em := testutil.Start(t)
	createSpannerDatabase(t, newDatabaseAdminClient(t, em))
	client := newSpannerClient(t, em)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// DML then an error from the txn func: the SDK rolls back, so the
	// DML must not persist. Inside the txn the DML is visible.
	sentinel := status.Error(codes.FailedPrecondition, "rollback please")
	_, err := client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		if _, err := txn.Update(ctx, spanner.Statement{SQL: "INSERT INTO songs (songId, title) VALUES (20, 'dml rollback')"}); err != nil {
			return err
		}
		if _, err := txn.ReadRow(ctx, "songs", spanner.Key{int64(20)}, []string{"title"}); err != nil {
			t.Errorf("read-your-writes inside txn: %v", err)
		}
		return sentinel
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("txn err = %v, want FailedPrecondition", err)
	}
	if songExists(t, client, 20) {
		t.Error("row 20 persisted after rollback")
	}

	// DML then commit: the row lands.
	_, err = client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		_, err := txn.Update(ctx, spanner.Statement{SQL: "INSERT INTO songs (songId, title) VALUES (21, 'dml commit')"})
		return err
	})
	if err != nil {
		t.Fatalf("commit txn: %v", err)
	}
	if !songExists(t, client, 21) {
		t.Error("row 21 missing after commit")
	}

	// Raw gRPC: ExecuteSql under a transaction id, then Rollback.
	srpc := sppb.NewSpannerClient(dialGRPC(t, em))
	sess, err := srpc.CreateSession(ctx, &sppb.CreateSessionRequest{Database: spannerDB})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	tx, err := srpc.BeginTransaction(ctx, &sppb.BeginTransactionRequest{
		Session: sess.GetName(),
		Options: &sppb.TransactionOptions{Mode: &sppb.TransactionOptions_ReadWrite_{}},
	})
	if err != nil {
		t.Fatalf("BeginTransaction: %v", err)
	}
	_, err = srpc.ExecuteSql(ctx, &sppb.ExecuteSqlRequest{
		Session:     sess.GetName(),
		Transaction: &sppb.TransactionSelector{Selector: &sppb.TransactionSelector_Id{Id: tx.GetId()}},
		Sql:         "INSERT INTO songs (songId, title) VALUES (22, 'raw rollback')",
		Seqno:       1,
	})
	if err != nil {
		t.Fatalf("ExecuteSql DML: %v", err)
	}
	if songExists(t, client, 22) {
		t.Error("row 22 visible outside its transaction before commit")
	}
	if _, err := srpc.Rollback(ctx, &sppb.RollbackRequest{Session: sess.GetName(), TransactionId: tx.GetId()}); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if songExists(t, client, 22) {
		t.Error("row 22 persisted after Rollback")
	}
}

func TestSpannerCommitFailureDiscardsDML(t *testing.T) {
	em := testutil.Start(t)
	createSpannerDatabase(t, newDatabaseAdminClient(t, em))
	client := newSpannerClient(t, em)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := client.Apply(ctx, []*spanner.Mutation{
		spanner.Insert("songs", []string{"songId", "title"}, []any{int64(1), "existing"}),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		if _, err := txn.Update(ctx, spanner.Statement{SQL: "INSERT INTO songs (songId, title) VALUES (30, 'dml')"}); err != nil {
			return err
		}
		return txn.BufferWrite([]*spanner.Mutation{
			spanner.Insert("songs", []string{"songId", "title"}, []any{int64(1), "duplicate"}),
		})
	})
	if spanner.ErrCode(err) != codes.AlreadyExists {
		t.Fatalf("commit err = %v, want AlreadyExists", err)
	}
	if songExists(t, client, 30) {
		t.Error("DML row 30 persisted although its Commit failed")
	}
}

func TestSpannerBatchDmlUnderTxn(t *testing.T) {
	em := testutil.Start(t)
	createSpannerDatabase(t, newDatabaseAdminClient(t, em))
	client := newSpannerClient(t, em)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := client.Apply(ctx, []*spanner.Mutation{
		spanner.Insert("songs", []string{"songId", "title"}, []any{int64(1), "existing"}),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Second statement fails: BatchUpdate errors, the txn rolls back, and
	// the first statement's row must not persist.
	_, err := client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		_, err := txn.BatchUpdate(ctx, []spanner.Statement{
			{SQL: "INSERT INTO songs (songId, title) VALUES (40, 'batch ok')"},
			{SQL: "INSERT INTO songs (songId, title) VALUES (1, 'batch dup')"},
		})
		return err
	})
	if spanner.ErrCode(err) != codes.AlreadyExists {
		t.Fatalf("batch txn err = %v, want AlreadyExists", err)
	}
	if songExists(t, client, 40) {
		t.Error("row 40 from a failed batch persisted")
	}

	// A clean batch commits both statements together.
	_, err = client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		counts, err := txn.BatchUpdate(ctx, []spanner.Statement{
			{SQL: "INSERT INTO songs (songId, title) VALUES (41, 'a')"},
			{SQL: "INSERT INTO songs (songId, title) VALUES (42, 'b')"},
		})
		if err == nil && (len(counts) != 2 || counts[0] != 1 || counts[1] != 1) {
			t.Errorf("batch counts = %v, want [1 1]", counts)
		}
		return err
	})
	if err != nil {
		t.Fatalf("clean batch: %v", err)
	}
	if !songExists(t, client, 41) || !songExists(t, client, 42) {
		t.Error("clean batch rows missing after commit")
	}
}

func TestSpannerDropUnknownDatabase(t *testing.T) {
	em := testutil.Start(t)
	admin := newDatabaseAdminClient(t, em)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := admin.DropDatabase(ctx, &adminpb.DropDatabaseRequest{
		Database: "projects/local-project/instances/test/databases/missing",
	})
	if status.Code(err) != codes.NotFound {
		t.Errorf("DropDatabase(unknown) code = %v, want NotFound (err: %v)", status.Code(err), err)
	}
}

func TestSpannerMalformedValues(t *testing.T) {
	em := testutil.Start(t)
	createSpannerDatabase(t, newDatabaseAdminClient(t, em))
	srpc := sppb.NewSpannerClient(dialGRPC(t, em))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sess, err := srpc.CreateSession(ctx, &sppb.CreateSessionRequest{Database: spannerDB})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	_, err = srpc.Commit(ctx, &sppb.CommitRequest{
		Session:     sess.GetName(),
		Transaction: &sppb.CommitRequest_SingleUseTransaction{SingleUseTransaction: &sppb.TransactionOptions{Mode: &sppb.TransactionOptions_ReadWrite_{}}},
		Mutations:   []*sppb.Mutation{grpcInsert("songs", []string{"songId", "title"}, "not-a-number", "x")},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("malformed INT64 mutation code = %v, want InvalidArgument (err: %v)", status.Code(err), err)
	}

	_, err = srpc.Read(ctx, &sppb.ReadRequest{
		Session: sess.GetName(),
		Table:   "songs",
		Columns: []string{"title"},
		KeySet:  &sppb.KeySet{Keys: []*structpb.ListValue{{Values: []*structpb.Value{structpb.NewStringValue("abc")}}}},
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("malformed INT64 key code = %v, want InvalidArgument (err: %v)", status.Code(err), err)
	}
}
