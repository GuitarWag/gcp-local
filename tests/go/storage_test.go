package gcplocaltest

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

func newClient(t *testing.T, em *testutil.Emulator) *storage.Client {
	t.Helper()
	t.Setenv("STORAGE_EMULATOR_HOST", "http://"+em.Host)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := storage.NewClient(ctx,
		option.WithoutAuthentication(),
		option.WithEndpoint("http://"+em.Host+"/storage/v1/"),
	)
	if err != nil {
		t.Fatalf("storage client: %v", err)
	}
	return client
}

func TestStorageBucketAndObjectRoundTrip(t *testing.T) {
	em := testutil.Start(t)
	client := newClient(t, em)
	defer client.Close()

	ctx := context.Background()
	const bucket = "test-bucket"
	const project = "local-project"

	if err := client.Bucket(bucket).Create(ctx, project, nil); err != nil {
		t.Fatalf("create bucket: %v", err)
	}

	// upload
	w := client.Bucket(bucket).Object("hello.txt").NewWriter(ctx)
	w.ContentType = "text/plain"
	if _, err := w.Write([]byte("hello, gcp-local")); err != nil {
		t.Fatalf("write object: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	// download
	r, err := client.Bucket(bucket).Object("hello.txt").NewReader(ctx)
	if err != nil {
		t.Fatalf("new reader: %v", err)
	}
	defer r.Close()
	body, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(body) != "hello, gcp-local" {
		t.Errorf("unexpected body: %q", string(body))
	}

	// list
	it := client.Bucket(bucket).Objects(ctx, nil)
	names := []string{}
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			t.Fatalf("iterate: %v", err)
		}
		names = append(names, attrs.Name)
	}
	if len(names) != 1 || names[0] != "hello.txt" {
		t.Errorf("expected [hello.txt], got %v", names)
	}

	// delete
	if err := client.Bucket(bucket).Object("hello.txt").Delete(ctx); err != nil {
		t.Errorf("delete object: %v", err)
	}
	if err := client.Bucket(bucket).Delete(ctx); err != nil {
		t.Errorf("delete bucket: %v", err)
	}
}

func TestStorageMissingObjectIs404(t *testing.T) {
	em := testutil.Start(t)
	client := newClient(t, em)
	defer client.Close()

	ctx := context.Background()
	if err := client.Bucket("b1").Create(ctx, "local-project", nil); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	_, err := client.Bucket("b1").Object("missing").Attrs(ctx)
	if !errors.Is(err, storage.ErrObjectNotExist) {
		t.Errorf("expected ErrObjectNotExist, got %v", err)
	}
}
