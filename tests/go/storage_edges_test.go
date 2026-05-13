package gcplocaltest

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"testing"
	"time"

	"cloud.google.com/go/storage"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

func TestStorageEmptyObject(t *testing.T) {
	em := testutil.Start(t)
	client := newClient(t, em)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := client.Bucket("edge").Create(ctx, project, nil); err != nil {
		t.Fatalf("bucket: %v", err)
	}
	w := client.Bucket("edge").Object("empty").NewWriter(ctx)
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	r, err := client.Bucket("edge").Object("empty").NewReader(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	defer r.Close()
	b, _ := io.ReadAll(r)
	if len(b) != 0 {
		t.Errorf("len=%d want 0", len(b))
	}
}

func TestStorageLargeObject(t *testing.T) {
	em := testutil.Start(t)
	client := newClient(t, em)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := client.Bucket("big").Create(ctx, project, nil); err != nil {
		t.Fatalf("bucket: %v", err)
	}
	// 2 MiB random
	payload := make([]byte, 2*1024*1024)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	w := client.Bucket("big").Object("blob").NewWriter(ctx)
	if _, err := io.Copy(w, bytes.NewReader(payload)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	r, err := client.Bucket("big").Object("blob").NewReader(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	defer r.Close()
	got, _ := io.ReadAll(r)
	if !bytes.Equal(got, payload) {
		t.Errorf("data mismatch (got %d / want %d bytes)", len(got), len(payload))
	}
}

func TestStorageObjectNameWithSlashesAndUnicode(t *testing.T) {
	em := testutil.Start(t)
	client := newClient(t, em)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := client.Bucket("paths").Create(ctx, project, nil); err != nil {
		t.Fatalf("bucket: %v", err)
	}
	names := []string{
		"a/b/c.txt",
		"deep/nested/path/file.json",
		"ünicode/café.txt",
	}
	for _, n := range names {
		w := client.Bucket("paths").Object(n).NewWriter(ctx)
		_, _ = w.Write([]byte(n))
		if err := w.Close(); err != nil {
			t.Fatalf("close %s: %v", n, err)
		}
	}
	for _, n := range names {
		r, err := client.Bucket("paths").Object(n).NewReader(ctx)
		if err != nil {
			t.Errorf("read %s: %v", n, err)
			continue
		}
		got, _ := io.ReadAll(r)
		r.Close()
		if string(got) != n {
			t.Errorf("read %s = %q", n, string(got))
		}
	}

	// silence unused
	_ = storage.NewClient
}
