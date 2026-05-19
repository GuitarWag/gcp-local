package gcplocaltest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/pubsub"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

// TestOTLPTraceExportPropagatesTraceparent boots the emulator with
// OTEL_EXPORTER_OTLP_ENDPOINT pointing at a stub OTLP HTTP receiver,
// fires a request carrying a known W3C traceparent, and verifies that
// the stub receiver got a /v1/traces export whose raw protobuf body
// contains the trace-id bytes from the incoming traceparent. That's
// enough to prove (a) the exporter is wired up when the env var is set
// and (b) tracecontext propagation parents the emulator's server span
// onto the caller's trace.
//
// We avoid pulling otlp/proto bindings into tests/go by doing a raw
// byte search: the trace id appears verbatim as a 16-byte bytes field
// in ResourceSpans → ScopeSpans → Span.trace_id.
func TestOTLPTraceExportPropagatesTraceparent(t *testing.T) {
	traceIDBytes := make([]byte, 16)
	if _, err := rand.Read(traceIDBytes); err != nil {
		t.Fatalf("rand: %v", err)
	}
	spanIDBytes := make([]byte, 8)
	if _, err := rand.Read(spanIDBytes); err != nil {
		t.Fatalf("rand: %v", err)
	}
	traceparent := "00-" + hex.EncodeToString(traceIDBytes) + "-" + hex.EncodeToString(spanIDBytes) + "-01"

	rec := newOTLPReceiver(t)
	defer rec.Close()

	em := testutil.StartArgsEnv(t, []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT=" + rec.URL,
		"OTEL_EXPORTER_OTLP_INSECURE=true",
		"OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
		"OTEL_BSP_SCHEDULE_DELAY=200",
		"OTEL_SERVICE_NAME=gcp-local-test",
	})

	req, err := http.NewRequest(http.MethodGet, em.URL("/healthz"), nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("traceparent", traceparent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get /healthz: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz: %d", resp.StatusCode)
	}

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if rec.containsTraceID(traceIDBytes) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("OTLP receiver did not record exported span with trace id %s within timeout; %d bodies seen, first body=%s",
		hex.EncodeToString(traceIDBytes), rec.bodyCount(), rec.firstBodyHex(256))
}

type otlpReceiver struct {
	URL    string
	srv    *httptest.Server
	mu     sync.Mutex
	bodies [][]byte
}

func newOTLPReceiver(t *testing.T) *otlpReceiver {
	t.Helper()
	r := &otlpReceiver{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/traces", func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		r.mu.Lock()
		r.bodies = append(r.bodies, body)
		r.mu.Unlock()
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte{}) // empty ExportTraceServiceResponse
	})
	// httptest binds to a random loopback port; that's exactly what we
	// want and avoids racing with the emulator's own port picker.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	r.srv = &httptest.Server{Listener: ln, Config: &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second}}
	r.srv.Start()
	r.URL = r.srv.URL
	return r
}

func (r *otlpReceiver) Close() {
	if r.srv != nil {
		r.srv.Close()
	}
}

func (r *otlpReceiver) containsTraceID(id []byte) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range r.bodies {
		if bytes.Contains(b, id) {
			return true
		}
	}
	return false
}

func (r *otlpReceiver) bodyCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.bodies)
}

func (r *otlpReceiver) firstBodyHex(max int) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.bodies) == 0 {
		return "<none>"
	}
	b := r.bodies[0]
	if len(b) > max {
		return hex.EncodeToString(b[:max]) + "...(truncated)"
	}
	return hex.EncodeToString(b)
}

// TestOTLPDisabledByDefault asserts that without OTEL_EXPORTER_OTLP_ENDPOINT
// the emulator exports nothing, even if a downstream OTLP receiver
// happens to be listening. This is the "no measurable overhead when
// OTel is disabled" half of the acceptance criteria — no requests at
// all means no exporter goroutine and no background flush traffic.
func TestOTLPDisabledByDefault(t *testing.T) {
	rec := newOTLPReceiver(t)
	defer rec.Close()

	em := testutil.Start(t)

	resp, err := http.Get(em.URL("/healthz"))
	if err != nil {
		t.Fatalf("get /healthz: %v", err)
	}
	_ = resp.Body.Close()

	// With tracing disabled there is no exporter goroutine at all, so
	// any non-zero body count below proves a regression rather than a
	// timing race. The wait just gives the (non-existent) batcher a
	// chance to misbehave.
	time.Sleep(1500 * time.Millisecond)
	if n := rec.bodyCount(); n != 0 {
		t.Fatalf("expected zero OTLP exports with tracing disabled, got %d", n)
	}
}

// TestOTLPGRPCExport boots the emulator with tracing on, makes a gRPC
// Pub/Sub call (CreateTopic), and asserts the stub receiver gets at
// least one /v1/traces export afterwards. This covers the otelgrpc
// stats handler wired onto the shared grpc.Server, which the HTTP-only
// test doesn't exercise.
func TestOTLPGRPCExport(t *testing.T) {
	rec := newOTLPReceiver(t)
	defer rec.Close()

	em := testutil.StartArgsEnv(t, []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT=" + rec.URL,
		"OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
		"OTEL_BSP_SCHEDULE_DELAY=200",
		"OTEL_SERVICE_NAME=gcp-local-test",
	})

	t.Setenv("PUBSUB_EMULATOR_HOST", em.Host)
	t.Setenv("PUBSUB_PROJECT_ID", "local-project")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cli, err := pubsub.NewClient(ctx, "local-project",
		option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("pubsub client: %v", err)
	}
	defer cli.Close()

	topic, err := cli.CreateTopic(ctx, "otel-qa-topic")
	if err != nil {
		t.Fatalf("create topic: %v", err)
	}
	_ = topic // creating it is the traced event we care about.

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if rec.bodyCount() > 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("OTLP receiver got 0 exports after a gRPC Pub/Sub call")
}
