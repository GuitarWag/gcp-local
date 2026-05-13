package gcplocaltest

import (
	"context"
	"testing"
	"time"

	btpb "cloud.google.com/go/bigtable/apiv2/bigtablepb"
	spb "cloud.google.com/go/spanner/apiv1/spannerpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

func dialGRPC(t *testing.T, em *testutil.Emulator) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient(em.Host, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestBigtableMethodsReturnUnimplemented(t *testing.T) {
	em := testutil.Start(t)
	conn := dialGRPC(t, em)
	c := btpb.NewBigtableClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := c.MutateRow(ctx, &btpb.MutateRowRequest{TableName: "x"})
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.Unimplemented {
		t.Errorf("expected Unimplemented, got %v", err)
	}
}

func TestSpannerCreateSessionWorksOthersUnimplemented(t *testing.T) {
	em := testutil.Start(t)
	conn := dialGRPC(t, em)
	c := spb.NewSpannerClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sess, err := c.CreateSession(ctx, &spb.CreateSessionRequest{Database: "projects/p/instances/i/databases/d"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if sess.GetName() == "" {
		t.Error("empty session name")
	}

	_, err = c.ExecuteSql(ctx, &spb.ExecuteSqlRequest{Session: sess.GetName()})
	if status.Code(err) != codes.Unimplemented {
		t.Errorf("expected Unimplemented for ExecuteSql, got %v", err)
	}
}
