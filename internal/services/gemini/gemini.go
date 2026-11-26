// Package gemini implements the Gemini API (generativelanguage.googleapis.com)
// as a deterministic fixture emulator. Models, replies, chunk counts, token
// counts, latency, and error injection all come from config; nothing is
// stored in state.Store and no inference runs.
package gemini

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GuitarWag/gcp-local/internal/config"
	"github.com/GuitarWag/gcp-local/internal/httpresp"
)

// Service is the Gemini fixture service. Root-path surface: the gateway
// mounts /v1beta/ models paths on its mux (Gemini is not a /v1/projects/
// service).
type Service struct {
	models    map[string]*model
	order     []string // configured order, for models.list
	failRules []config.GeminiFailRule

	mu     sync.Mutex
	failed map[string]int // model → failFirst calls served so far
}

type model struct {
	name        string
	displayName string
	reply       string
	chunks      int
	latency     time.Duration
	countTokens int
}

// New builds the service from config. Enabled when at least one model is
// configured.
func New(_ any, cfg *config.Config) (*Service, error) {
	s := &Service{models: make(map[string]*model), failed: make(map[string]int)}
	g := cfg.Services.Gemini
	for i, m := range g.Models {
		if m.Name == "" {
			return nil, fmt.Errorf("gemini.models[%d]: missing name", i)
		}
		if m.Reply == "" {
			return nil, fmt.Errorf("gemini.models[%d] (%s): missing reply", i, m.Name)
		}
		mm := &model{
			name:        m.Name,
			displayName: displayName(m.Name),
			reply:       m.Reply,
			chunks:      m.Chunks,
			latency:     time.Duration(m.LatencyMs) * time.Millisecond,
			countTokens: m.CountTokens,
		}
		if _, dup := s.models[mm.name]; dup {
			return nil, fmt.Errorf("gemini.models[%d]: duplicate name %s", i, mm.name)
		}
		s.models[mm.name] = mm
		s.order = append(s.order, mm.name)
	}
	for i, f := range g.FailFirst {
		if f.Model == "" {
			return nil, fmt.Errorf("gemini.failFirst[%d]: missing model", i)
		}
		if f.Status == 0 {
			g.FailFirst[i].Status = http.StatusTooManyRequests
		}
		if f.Count == 0 {
			g.FailFirst[i].Count = 1
		}
		if _, ok := s.models[g.FailFirst[i].Model]; !ok {
			return nil, fmt.Errorf("gemini.failFirst[%d]: unknown model %s", i, f.Model)
		}
	}
	s.failRules = g.FailFirst
	return s, nil
}

// displayName derives the human label from the model id, e.g.
// "gemini-2.0-flash" → "Gemini 2.0 Flash" (matches Google's style).
func displayName(id string) string {
	parts := strings.Split(id, "-")
	for i, p := range parts {
		if p == "" {
			continue
		}
		if i == 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
			continue
		}
		if _, err := strconv.Atoi(p); err == nil {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

func (s *Service) Name() string { return "gemini" }

// Enabled reports whether at least one model is configured.
func (s *Service) Enabled() bool { return len(s.models) > 0 }

// Register mounts the root-path Gemini surface on the gateway mux. The
// real API serves v1, v1beta, and v1alpha with identical model paths, so
// all three are mounted.
func (s *Service) Register(mux *http.ServeMux) {
	for _, v := range []string{"v1", "v1beta", "v1alpha"} {
		prefix := "/" + v + "/models"
		mux.HandleFunc(prefix, s.handleModelsList)
		mux.HandleFunc(prefix+"/", s.handleModelPathFor(prefix))
	}
}

func (s *Service) handleModelsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	// Trailing slash form: GET /v1beta/models/ is the same listing.
	out := map[string]any{"models": s.listModels()}
	httpresp.JSON(w, http.StatusOK, out)
}

// handleModelPathFor routes /{prefix}/{id} and /{prefix}/{id}:{verb} for
// one API version prefix (e.g. "/v1beta/models").
func (s *Service) handleModelPathFor(prefix string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, prefix+"/")
		if rest == "" {
			s.handleModelsList(w, r)
			return
		}
		id, verb, hasVerb := strings.Cut(rest, ":")
		m, ok := s.models[id]
		if !ok {
			notFoundModel(w, id)
			return
		}
		if !hasVerb {
			if r.Method != http.MethodGet {
				methodNotAllowed(w)
				return
			}
			httpresp.JSON(w, http.StatusOK, s.modelJSON(m))
			return
		}
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		switch verb {
		case "generateContent":
			s.generate(w, r, m, false)
		case "streamGenerateContent":
			s.generate(w, r, m, true)
		case "countTokens":
			s.countTokens(w, r, m)
		default:
			notFoundModel(w, id)
		}
	}
}

// takeFailSlot consumes one failFirst counter for the model and returns
// the injected status (0 = no injection).
func (s *Service) takeFailSlot(name string) int {
	var rule *config.GeminiFailRule
	for i := range s.failRules {
		if s.failRules[i].Model == name {
			rule = &s.failRules[i]
			break
		}
	}
	if rule == nil || rule.Count <= 0 {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	served := s.failed[name]
	if served >= rule.Count {
		return 0
	}
	s.failed[name] = served + 1
	return rule.Status
}

func (s *Service) generate(w http.ResponseWriter, r *http.Request, m *model, stream bool) {
	var req generateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		badRequest(w, "Invalid JSON payload received.")
		return
	}
	if status := s.takeFailSlot(m.name); status != 0 {
		s.writeError(w, status, m.name)
		return
	}
	simulateLatency(m.latency)

	prompt := req.promptText()
	if stream {
		s.streamGenerate(w, m, prompt)
		return
	}
	usage := usageFor(prompt, m.reply)
	httpresp.JSON(w, http.StatusOK, map[string]any{
		"candidates": []map[string]any{{
			"content": map[string]any{
				"parts": []map[string]any{{"text": m.reply}},
				"role":  "model",
			},
			"finishReason": "STOP",
		}},
		"usageMetadata": usage,
		"modelVersion":  m.name,
	})
}

// streamGenerate writes the reply as N SSE frames; finishReason and final
// usageMetadata appear only on the last frame.
func (s *Service) streamGenerate(w http.ResponseWriter, m *model, prompt string) {
	chunks := m.chunks
	if chunks < 1 {
		chunks = 1
	}
	if chunks > len(strings.Fields(m.reply)) {
		// Never emit empty-text frames just to hit the configured count.
		chunks = maxInt(1, len(strings.Fields(m.reply)))
	}
	parts := splitReply(m.reply, chunks)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, _ := w.(http.Flusher)

	for i, text := range parts {
		last := i == len(parts)-1
		resp := map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{
					"parts": []map[string]any{{"text": text}},
					"role":  "model",
				},
			}},
			"modelVersion": m.name,
		}
		if last {
			candidates, _ := resp["candidates"].([]map[string]any)
			candidates[0]["finishReason"] = "STOP"
			resp["usageMetadata"] = usageFor(prompt, m.reply)
		}
		frame, err := json.Marshal(resp)
		if err != nil {
			return
		}
		fmt.Fprintf(w, "data: %s\n\n", frame)
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// splitReply divides reply into n pieces whose concatenation equals the
// original, cutting at word boundaries (each piece after the first keeps
// its leading space, like real token streaming).
func splitReply(reply string, n int) []string {
	words := strings.Fields(reply)
	if n <= 1 || len(words) < n {
		return []string{reply}
	}
	// Word start offsets in the raw string.
	starts := make([]int, 0, len(words))
	for i, w := range words {
		if i == 0 {
			starts = append(starts, 0)
			continue
		}
		starts = append(starts, strings.Index(reply[starts[i-1]+len(words[i-1]):], w)+starts[i-1]+len(words[i-1]))
	}
	parts := make([]string, 0, n)
	per := len(words) / n
	extra := len(words) % n
	idx := 0
	for i := 0; i < n; i++ {
		size := per
		if i < extra {
			size++
		}
		from := starts[idx]
		to := len(reply)
		if idx+size < len(words) {
			to = starts[idx+size]
		}
		parts = append(parts, reply[from:to])
		idx += size
	}
	return parts
}

func (s *Service) countTokens(w http.ResponseWriter, r *http.Request, m *model) {
	var req generateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		badRequest(w, "Invalid JSON payload received.")
		return
	}
	if status := s.takeFailSlot(m.name); status != 0 {
		s.writeError(w, status, m.name)
		return
	}
	simulateLatency(m.latency)
	total := m.countTokens
	if total == 0 {
		total = len(strings.Fields(m.reply))
	}
	httpresp.JSON(w, http.StatusOK, map[string]any{"totalTokens": total})
}

// simulateLatency sleeps for the configured duration when positive.
func simulateLatency(d time.Duration) {
	if d > 0 {
		time.Sleep(d)
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func methodNotAllowed(w http.ResponseWriter) {
	httpresp.Err(w, http.StatusMethodNotAllowed, "method not allowed")
}

func notFoundModel(w http.ResponseWriter, id string) {
	httpresp.JSON(w, http.StatusNotFound, map[string]any{
		"error": map[string]any{
			"code":    404,
			"message": fmt.Sprintf("models/%s is not found for API version v1beta, or is not supported for generateContent.", id),
			"status":  "NOT_FOUND",
		},
	})
}

func badRequest(w http.ResponseWriter, msg string) {
	httpresp.JSON(w, http.StatusBadRequest, map[string]any{
		"error": map[string]any{
			"code":    400,
			"message": msg,
			"status":  "INVALID_ARGUMENT",
		},
	})
}
