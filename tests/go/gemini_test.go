package gcplocaltest

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

// geminiConfig returns a config file body that registers two fixture
// models plus one failFirst error-injection rule.
func geminiConfig(withFail bool) string {
	cfg := "project: local-project\nstate: memory\nservices:\n" +
		"  gemini:\n" +
		"    models:\n" +
		"      - name: gemini-2.0-flash\n" +
		"        reply: \"Hello from the fixture model\"\n" +
		"        chunks: 3\n" +
		"        countTokens: 42\n" +
		"      - name: gemini-1.5-pro\n" +
		"        reply: \"Pro reply words here\"\n"
	if withFail {
		cfg += "    failFirst:\n" +
			"      - model: gemini-2.0-flash\n" +
			"        status: 429\n" +
			"        count: 1\n" +
			"        retryDelay: 5s\n"
	}
	return cfg
}

// startGemini boots an emulator whose config registers the gemini models.
func startGemini(t *testing.T, withFail bool) *testutil.Emulator {
	t.Helper()
	return startWithConfig(t, writeTempConfig(t, geminiConfig(withFail)))
}

func writeTempConfig(t *testing.T, body string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "gemini-cfg-*.yaml")
	if err != nil {
		t.Fatalf("temp config: %v", err)
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		t.Fatalf("close temp: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func geminiPost(t *testing.T, em *testutil.Emulator, path, body, apiKey string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, em.URL(path), strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("x-goog-api-key", apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp, out
}

type geminiResp struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
			Role string `json:"role"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
	ModelVersion string `json:"modelVersion"`
}

type geminiError struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
		Details []struct {
			Type       string `json:"@type"`
			RetryDelay string `json:"retryDelay"`
		} `json:"details"`
	} `json:"error"`
}

func TestGeminiGenerateContent(t *testing.T) {
	em := startGemini(t, false)
	resp, body := geminiPost(t, em, "/v1beta/models/gemini-2.0-flash:generateContent",
		`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`, "test-key")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	var out geminiResp
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(out.Candidates))
	}
	c := out.Candidates[0]
	if c.Content.Role != "model" {
		t.Errorf("role = %q, want model", c.Content.Role)
	}
	if len(c.Content.Parts) != 1 || c.Content.Parts[0].Text != "Hello from the fixture model" {
		t.Errorf("parts = %+v, want fixture reply", c.Content.Parts)
	}
	if c.FinishReason != "STOP" {
		t.Errorf("finishReason = %q, want STOP", c.FinishReason)
	}
	if out.ModelVersion != "gemini-2.0-flash" {
		t.Errorf("modelVersion = %q", out.ModelVersion)
	}
	if out.UsageMetadata.TotalTokenCount == 0 {
		t.Errorf("usageMetadata.totalTokenCount = 0, want > 0")
	}
}

func TestGeminiGenerateContentKeyParam(t *testing.T) {
	em := startGemini(t, false)
	// Auth via ?key= query parameter instead of the header.
	resp, body := geminiPost(t, em, "/v1beta/models/gemini-1.5-pro:generateContent?key=another-key",
		`{"contents":[{"parts":[{"text":"hi"}]}]}`, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	var out geminiResp
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Candidates) != 1 || out.Candidates[0].Content.Parts[0].Text != "Pro reply words here" {
		t.Fatalf("reply = %s", body)
	}
}

// readSSEFrames reads all "data: ..." frames from an SSE response body.
func readSSEFrames(t *testing.T, resp *http.Response) []string {
	t.Helper()
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var frames []string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			frames = append(frames, strings.TrimPrefix(line, "data: "))
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan sse: %v", err)
	}
	return frames
}

func TestGeminiStreamGenerateContent(t *testing.T) {
	em := startGemini(t, false)
	resp, err := http.Post(em.URL("/v1beta/models/gemini-2.0-flash:streamGenerateContent"),
		"application/json", strings.NewReader(`{"contents":[{"parts":[{"text":"hi"}]}]}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("content-type = %q, want text/event-stream", ct)
	}
	frames := readSSEFrames(t, resp)
	if len(frames) != 3 {
		t.Fatalf("frames = %d, want 3", len(frames))
	}
	var texts []string
	for i, f := range frames {
		var out geminiResp
		if err := json.Unmarshal([]byte(f), &out); err != nil {
			t.Fatalf("decode frame %d: %v", i, err)
		}
		if out.ModelVersion != "gemini-2.0-flash" {
			t.Errorf("frame %d modelVersion = %q", i, out.ModelVersion)
		}
		if len(out.Candidates) != 1 {
			t.Fatalf("frame %d candidates = %d", i, len(out.Candidates))
		}
		c := out.Candidates[0]
		last := i == len(frames)-1
		if last {
			if c.FinishReason != "STOP" {
				t.Errorf("last frame finishReason = %q, want STOP", c.FinishReason)
			}
			if out.UsageMetadata.TotalTokenCount == 0 {
				t.Errorf("last frame usageMetadata.totalTokenCount = 0")
			}
		} else if c.FinishReason != "" {
			t.Errorf("frame %d finishReason = %q, want empty", i, c.FinishReason)
		}
		if len(c.Content.Parts) > 0 {
			texts = append(texts, c.Content.Parts[0].Text)
		}
	}
	if got := strings.Join(texts, ""); got != "Hello from the fixture model" {
		t.Errorf("concatenated stream = %q, want full reply", got)
	}
}

func TestGeminiStreamGenerateContentAltSSE(t *testing.T) {
	em := startGemini(t, false)
	resp, err := http.Post(em.URL("/v1beta/models/gemini-1.5-pro:streamGenerateContent?alt=sse"),
		"application/json", strings.NewReader(`{"contents":[{"parts":[{"text":"hi"}]}]}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	frames := readSSEFrames(t, resp)
	if len(frames) < 1 {
		t.Fatalf("frames = %d, want >= 1", len(frames))
	}
	var out geminiResp
	if err := json.Unmarshal([]byte(frames[len(frames)-1]), &out); err != nil {
		t.Fatalf("decode last frame: %v", err)
	}
	if out.Candidates[0].FinishReason != "STOP" {
		t.Errorf("last frame finishReason = %q, want STOP", out.Candidates[0].FinishReason)
	}
}

func TestGeminiCountTokens(t *testing.T) {
	em := startGemini(t, false)
	// Explicit countTokens override on gemini-2.0-flash.
	resp, body := geminiPost(t, em, "/v1beta/models/gemini-2.0-flash:countTokens",
		`{"contents":[{"parts":[{"text":"count me"}]}]}`, "k")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	var out struct {
		TotalTokens int `json:"totalTokens"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.TotalTokens != 42 {
		t.Errorf("totalTokens = %d, want 42", out.TotalTokens)
	}

	// Default = whitespace word count of the reply.
	resp, body = geminiPost(t, em, "/v1beta/models/gemini-1.5-pro:countTokens",
		`{"contents":[{"parts":[{"text":"count me"}]}]}`, "k")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.TotalTokens != 4 {
		t.Errorf("totalTokens = %d, want 4 (word count of reply)", out.TotalTokens)
	}
}

func TestGeminiModelsListAndGet(t *testing.T) {
	em := startGemini(t, false)
	resp, err := http.Get(em.URL("/v1beta/models"))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	var list struct {
		Models []struct {
			Name                      string   `json:"name"`
			DisplayName               string   `json:"displayName"`
			SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list.Models) != 2 {
		t.Fatalf("models = %d, want 2", len(list.Models))
	}
	first := list.Models[0]
	if first.Name != "models/gemini-2.0-flash" {
		t.Errorf("name = %q, want models/gemini-2.0-flash", first.Name)
	}
	if first.DisplayName == "" {
		t.Errorf("displayName empty")
	}
	want := map[string]bool{"generateContent": false, "streamGenerateContent": false, "countTokens": false}
	for _, m := range first.SupportedGenerationMethods {
		if _, ok := want[m]; ok {
			want[m] = true
		}
	}
	for m, seen := range want {
		if !seen {
			t.Errorf("supportedGenerationMethods missing %q", m)
		}
	}

	// Single model GET.
	resp2, err := http.Get(em.URL("/v1beta/models/gemini-1.5-pro"))
	if err != nil {
		t.Fatalf("get model: %v", err)
	}
	defer resp2.Body.Close()
	body2, _ := io.ReadAll(resp2.Body)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("model status = %d, body = %s", resp2.StatusCode, body2)
	}
	var one struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body2, &one); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if one.Name != "models/gemini-1.5-pro" {
		t.Errorf("name = %q, want models/gemini-1.5-pro", one.Name)
	}
}

func TestGeminiUnknownModel(t *testing.T) {
	em := startGemini(t, false)
	resp, body := geminiPost(t, em, "/v1beta/models/gemini-nope:generateContent",
		`{"contents":[{"parts":[{"text":"hi"}]}]}`, "k")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", resp.StatusCode, body)
	}
	var e geminiError
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if e.Error.Code != 404 || e.Error.Status != "NOT_FOUND" || e.Error.Message == "" {
		t.Errorf("error = %+v, want 404/NOT_FOUND with message", e.Error)
	}
}

func TestGeminiMalformedJSON(t *testing.T) {
	em := startGemini(t, false)
	resp, body := geminiPost(t, em, "/v1beta/models/gemini-2.0-flash:generateContent",
		`{not json`, "k")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", resp.StatusCode, body)
	}
	var e geminiError
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if e.Error.Code != 400 || e.Error.Status != "INVALID_ARGUMENT" {
		t.Errorf("error = %+v, want 400/INVALID_ARGUMENT", e.Error)
	}
}

func TestGeminiFailFirstRetryInfo(t *testing.T) {
	em := startGemini(t, true)
	// First call fails with 429 + RetryInfo.retryDelay.
	resp, body := geminiPost(t, em, "/v1beta/models/gemini-2.0-flash:generateContent",
		`{"contents":[{"parts":[{"text":"hi"}]}]}`, "k")
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429, body = %s", resp.StatusCode, body)
	}
	var e geminiError
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if e.Error.Code != 429 || e.Error.Status != "RESOURCE_EXHAUSTED" {
		t.Errorf("error = %+v, want 429/RESOURCE_EXHAUSTED", e.Error)
	}
	foundRetry := false
	for _, d := range e.Error.Details {
		if strings.Contains(d.Type, "google.rpc.RetryInfo") && d.RetryDelay == "5s" {
			foundRetry = true
		}
	}
	if !foundRetry {
		t.Errorf("details missing RetryInfo retryDelay 5s: %+v", e.Error.Details)
	}

	// Retry after the injected failure succeeds.
	resp2, body2 := geminiPost(t, em, "/v1beta/models/gemini-2.0-flash:generateContent",
		`{"contents":[{"parts":[{"text":"hi"}]}]}`, "k")
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("retry status = %d, body = %s", resp2.StatusCode, body2)
	}
}

func TestGeminiConsoleStatus(t *testing.T) {
	em := startGemini(t, false)
	resp, err := http.Get(em.URL("/console/api/gemini/status"))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	var st map[string]any
	if err := json.Unmarshal(body, &st); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if st["state"] != "ready" {
		t.Errorf("state = %v, want ready", st["state"])
	}
	// Console page renders.
	page, err := http.Get(em.URL("/console/gemini"))
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	defer page.Body.Close()
	if page.StatusCode != http.StatusOK {
		t.Errorf("page status = %d", page.StatusCode)
	}
}

// TestGeminiLatencyAndDefaultClientCompat checks latencyMs is honoured
// (bounded) and that requests without any API key still work (lenient
// default; enforcement knob is deferred).
func TestGeminiLatencyAndNoKey(t *testing.T) {
	cfg := strings.Replace(geminiConfig(false), "chunks: 3\n", "chunks: 3\n        latencyMs: 150\n", 1)
	em := startWithConfig(t, writeTempConfig(t, cfg))
	start := time.Now()
	resp, body := geminiPost(t, em, "/v1beta/models/gemini-2.0-flash:generateContent",
		`{"contents":[{"parts":[{"text":"hi"}]}]}`, "")
	elapsed := time.Since(start)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	if elapsed < 100*time.Millisecond {
		t.Errorf("latencyMs not honoured, elapsed = %v", elapsed)
	}
}
