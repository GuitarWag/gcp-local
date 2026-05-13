package gcplocaltest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

// TestStorageXMLPutGetRoundTrip exercises the XML-API upload + read path used
// by the Python google-cloud-storage SDK in some configurations.
func TestStorageXMLPutGetRoundTrip(t *testing.T) {
	em := testutil.Start(t)

	if resp, body := doJSON(t, http.MethodPost, em.URL("/storage/v1/b?project="+project),
		map[string]any{"name": "xml-bucket"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("create bucket: %d %s", resp.StatusCode, body)
	}

	payload := []byte("hello from XML API")
	req, _ := http.NewRequest(http.MethodPut, em.URL("/xml-bucket/hello.txt"), bytes.NewReader(payload))
	req.Header.Set("Content-Type", "text/plain")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("xml put: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("xml put status: %d", resp.StatusCode)
	}
	if resp.Header.Get("ETag") == "" {
		t.Error("expected ETag header on XML PUT response")
	}

	// Read back via the XML GET path.
	getResp, err := http.Get(em.URL("/xml-bucket/hello.txt"))
	if err != nil {
		t.Fatalf("xml get: %v", err)
	}
	defer getResp.Body.Close()
	body, _ := io.ReadAll(getResp.Body)
	if string(body) != string(payload) {
		t.Errorf("xml get body = %q, want %q", string(body), string(payload))
	}
	if ct := getResp.Header.Get("Content-Type"); ct != "text/plain" {
		t.Errorf("xml get Content-Type = %q, want text/plain", ct)
	}

	// And via the JSON API to confirm metadata wrote through.
	mResp, mBody := doJSON(t, http.MethodGet, em.URL("/storage/v1/b/xml-bucket/o/hello.txt"), nil)
	if mResp.StatusCode != http.StatusOK {
		t.Fatalf("json metadata: %d %s", mResp.StatusCode, mBody)
	}
	var meta struct {
		Size        string `json:"size"`
		ContentType string `json:"contentType"`
	}
	_ = json.Unmarshal(mBody, &meta)
	if meta.ContentType != "text/plain" {
		t.Errorf("json metadata ContentType = %q", meta.ContentType)
	}
	if meta.Size != fmt.Sprintf("%d", len(payload)) {
		t.Errorf("json metadata Size = %q, want %d", meta.Size, len(payload))
	}
}

// TestStorageResumableMultiChunk drives the resumable upload session through
// two chunks with the standard Content-Range protocol, asserts the 308
// intermediate status, and verifies the assembled object is correct.
func TestStorageResumableMultiChunk(t *testing.T) {
	em := testutil.Start(t)

	if resp, body := doJSON(t, http.MethodPost, em.URL("/storage/v1/b?project="+project),
		map[string]any{"name": "chunk-bucket"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("create bucket: %d %s", resp.StatusCode, body)
	}

	// 1. Start the session.
	startReq, _ := http.NewRequest(http.MethodPost,
		em.URL("/upload/storage/v1/b/chunk-bucket/o?uploadType=resumable&name=big.bin"), nil)
	startReq.Header.Set("X-Upload-Content-Type", "application/octet-stream")
	startResp, err := http.DefaultClient.Do(startReq)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	startResp.Body.Close()
	if startResp.StatusCode != http.StatusOK {
		t.Fatalf("start session status: %d", startResp.StatusCode)
	}
	loc := startResp.Header.Get("Location")
	if loc == "" {
		t.Fatal("session Location header missing")
	}
	// Strip scheme/host so we can hit the emulator directly.
	idx := bytes.Index([]byte(loc), []byte("/upload/"))
	if idx < 0 {
		t.Fatalf("unexpected Location: %s", loc)
	}
	uploadURL := em.URL(loc[idx:])

	// 2. Build a payload bigger than one chunk and send two halves.
	full := bytes.Repeat([]byte("X"), 1024)
	half := len(full) / 2

	// Chunk 1: bytes 0-511/1024 (total known, not final)
	c1Req, _ := http.NewRequest(http.MethodPut, uploadURL, bytes.NewReader(full[:half]))
	c1Req.Header.Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", half-1, len(full)))
	c1Resp, err := http.DefaultClient.Do(c1Req)
	if err != nil {
		t.Fatalf("chunk 1: %v", err)
	}
	c1Resp.Body.Close()
	if c1Resp.StatusCode != http.StatusPermanentRedirect {
		t.Fatalf("chunk 1 status: %d (want 308)", c1Resp.StatusCode)
	}
	if got, want := c1Resp.Header.Get("Range"), fmt.Sprintf("bytes=0-%d", half-1); got != want {
		t.Errorf("chunk 1 Range header = %q, want %q", got, want)
	}

	// 3. Status query: PUT */1024 with empty body.
	statusReq, _ := http.NewRequest(http.MethodPut, uploadURL, nil)
	statusReq.Header.Set("Content-Range", fmt.Sprintf("bytes */%d", len(full)))
	statusResp, err := http.DefaultClient.Do(statusReq)
	if err != nil {
		t.Fatalf("status query: %v", err)
	}
	statusResp.Body.Close()
	if statusResp.StatusCode != http.StatusPermanentRedirect {
		t.Fatalf("status query: %d (want 308)", statusResp.StatusCode)
	}

	// 4. Chunk 2 (final): bytes 512-1023/1024.
	c2Req, _ := http.NewRequest(http.MethodPut, uploadURL, bytes.NewReader(full[half:]))
	c2Req.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", half, len(full)-1, len(full)))
	c2Resp, err := http.DefaultClient.Do(c2Req)
	if err != nil {
		t.Fatalf("chunk 2: %v", err)
	}
	defer c2Resp.Body.Close()
	if c2Resp.StatusCode != http.StatusOK {
		t.Fatalf("chunk 2 status: %d (want 200)", c2Resp.StatusCode)
	}

	// 5. Read object back and verify.
	getResp, err := http.Get(em.URL("/storage/v1/b/chunk-bucket/o/big.bin?alt=media"))
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer getResp.Body.Close()
	got, _ := io.ReadAll(getResp.Body)
	if !bytes.Equal(got, full) {
		t.Errorf("reassembled object differs (len=%d, want %d)", len(got), len(full))
	}
}

// TestStorageResumableOutOfOrderChunkReturns308 verifies the emulator rejects
// an out-of-order chunk by returning 308 with the current accepted byte range
// rather than corrupting the buffer.
func TestStorageResumableOutOfOrderChunkReturns308(t *testing.T) {
	em := testutil.Start(t)

	if resp, _ := doJSON(t, http.MethodPost, em.URL("/storage/v1/b?project="+project),
		map[string]any{"name": "ooo-bucket"}); resp.StatusCode != http.StatusOK {
		t.Fatalf("bucket")
	}
	startReq, _ := http.NewRequest(http.MethodPost,
		em.URL("/upload/storage/v1/b/ooo-bucket/o?uploadType=resumable&name=ooo.bin"), nil)
	startResp, _ := http.DefaultClient.Do(startReq)
	startResp.Body.Close()
	loc := startResp.Header.Get("Location")
	idx := bytes.Index([]byte(loc), []byte("/upload/"))
	uploadURL := em.URL(loc[idx:])

	// Skip ahead — send "bytes 512-1023/1024" before sending 0-511.
	req, _ := http.NewRequest(http.MethodPut, uploadURL, bytes.NewReader(bytes.Repeat([]byte("Y"), 512)))
	req.Header.Set("Content-Range", "bytes 512-1023/1024")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ooo put: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusPermanentRedirect {
		t.Errorf("expected 308 on out-of-order chunk, got %d", resp.StatusCode)
	}
	// And no Range header since no bytes accepted yet.
	if r := resp.Header.Get("Range"); r != "" {
		t.Errorf("expected no Range header (zero bytes accepted), got %q", r)
	}
}
