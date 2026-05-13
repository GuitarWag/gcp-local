package gcplocaltest

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

func TestFirestoreNestedTypesRoundtrip(t *testing.T) {
	em := testutil.Start(t)
	client := newFirestore(t, em)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	when := time.Date(2026, 5, 12, 9, 30, 0, 0, time.UTC)
	want := map[string]any{
		"str":      "hello",
		"intval":   int64(42),
		"floatval": 3.14,
		"flag":     true,
		"none":     nil,
		"tags":     []any{"a", "b", "c"},
		"nested": map[string]any{
			"inner":  "world",
			"count":  int64(5),
			"deeper": map[string]any{"x": int64(1)},
		},
		"when": when,
	}

	doc := client.Collection("complex").Doc("d1")
	if _, err := doc.Set(ctx, want); err != nil {
		t.Fatalf("set: %v", err)
	}
	snap, err := doc.Get(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	got := snap.Data()

	// Compare without time (Firestore times come back as time.Time)
	gotWhen, ok := got["when"].(time.Time)
	if !ok {
		t.Fatalf("when type = %T", got["when"])
	}
	if !gotWhen.Equal(when) {
		t.Errorf("when = %v want %v", gotWhen, when)
	}
	delete(got, "when")
	delete(want, "when")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("data mismatch:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestFirestoreBatchCommit(t *testing.T) {
	em := testutil.Start(t)
	client := newFirestore(t, em)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	batch := client.Batch()
	for i := 0; i < 5; i++ {
		batch.Set(client.Collection("batched").Doc(string(rune('a'+i))), map[string]any{"i": i})
	}
	if _, err := batch.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	docs, err := client.Collection("batched").Documents(ctx).GetAll()
	if err != nil {
		t.Fatalf("getall: %v", err)
	}
	if len(docs) != 5 {
		t.Errorf("expected 5 docs, got %d", len(docs))
	}
}
