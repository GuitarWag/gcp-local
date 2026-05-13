package gcplocaltest

import (
	"context"
	"testing"
	"time"

	fs "cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

func newFirestoreClient(t *testing.T, em *testutil.Emulator) *fs.Client {
	t.Helper()
	t.Setenv("FIRESTORE_EMULATOR_HOST", em.Host)
	conn, err := grpc.NewClient(em.Host, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	client, err := fs.NewClient(context.Background(), project,
		option.WithGRPCConn(conn),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("firestore client: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// TestFirestoreDocumentSnapshotReceivesWrites starts a DocumentRef.Snapshots
// listener on a doc, writes to it from a goroutine, and asserts the listener
// observes the change within a short deadline.
func TestFirestoreDocumentSnapshotReceivesWrites(t *testing.T) {
	em := testutil.Start(t)
	client := newFirestoreClient(t, em)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	docRef := client.Collection("listen-test").Doc("counter")

	iter := docRef.Snapshots(ctx)
	defer iter.Stop()

	// First Next() returns the current state. Doc doesn't exist yet — snap
	// should report Exists()=false but the listener is now live.
	snap, err := iter.Next()
	if err != nil {
		t.Fatalf("initial snapshot: %v", err)
	}
	if snap.Exists() {
		t.Errorf("expected initial snapshot to report doc missing")
	}

	// Write the doc from a goroutine.
	go func() {
		_, _ = docRef.Set(ctx, map[string]any{"v": 42})
	}()

	// Second Next() must observe the write within the test budget.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snap, err = iter.Next()
		if err != nil {
			t.Fatalf("snapshot after write: %v", err)
		}
		if snap.Exists() {
			data := snap.Data()
			if v, ok := data["v"]; ok {
				// Firestore Int64 returns int64; the emulator round-trips
				// through stored value JSON so an integer ends up int64.
				if v != int64(42) {
					t.Errorf("snapshot v = %v, want 42", v)
				}
				return
			}
		}
	}
	t.Fatal("snapshot listener never observed the write")
}

// TestFirestoreQuerySnapshotReceivesNewDoc starts a CollectionRef.Snapshots
// listener (Query Listen), creates a new doc in the collection, and asserts
// the listener picks it up.
func TestFirestoreQuerySnapshotReceivesNewDoc(t *testing.T) {
	em := testutil.Start(t)
	client := newFirestoreClient(t, em)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	coll := client.Collection("listen-query-test")
	iter := coll.Snapshots(ctx)
	defer iter.Stop()

	// Initial snapshot: collection is empty.
	qsnap, err := iter.Next()
	if err != nil {
		t.Fatalf("initial query snapshot: %v", err)
	}
	if qsnap.Size != 0 {
		t.Errorf("expected initial empty collection, got %d docs", qsnap.Size)
	}

	go func() {
		_, _ = coll.Doc("new-doc").Set(ctx, map[string]any{"label": "alpha"})
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		qsnap, err = iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			t.Fatalf("query snapshot: %v", err)
		}
		if qsnap.Size >= 1 {
			docs, _ := qsnap.Documents.GetAll()
			for _, d := range docs {
				if d.Ref.ID == "new-doc" {
					return
				}
			}
		}
	}
	t.Fatal("query snapshot listener never observed the new doc")
}

// TestFirestoreDocumentSnapshotReceivesDelete confirms that deleting the
// observed doc fires the listener with an empty snapshot.
func TestFirestoreDocumentSnapshotReceivesDelete(t *testing.T) {
	em := testutil.Start(t)
	client := newFirestoreClient(t, em)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	docRef := client.Collection("listen-delete").Doc("doomed")
	if _, err := docRef.Set(ctx, map[string]any{"v": 1}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	iter := docRef.Snapshots(ctx)
	defer iter.Stop()

	// Initial snapshot: doc exists.
	if snap, err := iter.Next(); err != nil {
		t.Fatalf("initial: %v", err)
	} else if !snap.Exists() {
		t.Fatal("expected initial snapshot to show existing doc")
	}

	go func() {
		_, _ = docRef.Delete(ctx)
	}()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snap, err := iter.Next()
		if err != nil {
			t.Fatalf("snapshot after delete: %v", err)
		}
		if !snap.Exists() {
			return
		}
	}
	t.Fatal("snapshot listener never observed the deletion")
}
