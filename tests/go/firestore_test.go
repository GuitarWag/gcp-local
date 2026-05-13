package gcplocaltest

import (
	"context"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

func newFirestore(t *testing.T, em *testutil.Emulator) *firestore.Client {
	t.Helper()
	t.Setenv("FIRESTORE_EMULATOR_HOST", em.Host)
	conn, err := grpc.NewClient(em.Host, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	client, err := firestore.NewClient(context.Background(), project,
		option.WithGRPCConn(conn),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("firestore client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestFirestoreSetGetUpdateDelete(t *testing.T) {
	em := testutil.Start(t)
	client := newFirestore(t, em)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	doc := client.Collection("users").Doc("alice")
	_, err := doc.Set(ctx, map[string]any{
		"name":  "Alice",
		"age":   30,
		"score": 99.5,
	})
	if err != nil {
		t.Fatalf("set: %v", err)
	}

	snap, err := doc.Get(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	data := snap.Data()
	if data["name"] != "Alice" {
		t.Errorf("name = %v", data["name"])
	}
	if data["age"] != int64(30) {
		t.Errorf("age = %v (%T)", data["age"], data["age"])
	}

	_, err = doc.Update(ctx, []firestore.Update{{Path: "age", Value: 31}})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	snap, _ = doc.Get(ctx)
	if snap.Data()["age"] != int64(31) {
		t.Errorf("age after update = %v", snap.Data()["age"])
	}

	if _, err := doc.Delete(ctx); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := doc.Get(ctx); err == nil {
		t.Error("expected error reading deleted doc")
	}
}

func TestFirestoreCollectionQuery(t *testing.T) {
	em := testutil.Start(t)
	client := newFirestore(t, em)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	coll := client.Collection("items")
	if _, err := coll.Doc("a").Set(ctx, map[string]any{"v": 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := coll.Doc("b").Set(ctx, map[string]any{"v": 2}); err != nil {
		t.Fatal(err)
	}

	docs, err := coll.Documents(ctx).GetAll()
	if err != nil {
		t.Fatalf("getall: %v", err)
	}
	if len(docs) != 2 {
		t.Errorf("expected 2 docs, got %d", len(docs))
	}
}
