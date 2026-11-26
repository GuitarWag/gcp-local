package gemini

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/GuitarWag/gcp-local/internal/httpresp"
)

// generateRequest is the subset of GenerateContentRequest the fixture
// engine reads: only the prompt text (for usageMetadata accounting).
type generateRequest struct {
	Contents []struct {
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	} `json:"contents"`
}

// promptText concatenates all text parts across contents.
func (r *generateRequest) promptText() string {
	var b strings.Builder
	for _, c := range r.Contents {
		for _, p := range c.Parts {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// usageFor mirrors the real service's usageMetadata shape. Token counts
// are deterministic: prompt counts as whitespace words (matching the
// countTokens default), the reply counts its configured/derived tokens.
func usageFor(prompt, reply string) map[string]any {
	promptTokens := len(strings.Fields(prompt))
	replyTokens := len(strings.Fields(reply))
	if replyTokens == 0 {
		replyTokens = 1
	}
	return map[string]any{
		"promptTokenCount":     promptTokens,
		"candidatesTokenCount": replyTokens,
		"totalTokenCount":      promptTokens + replyTokens,
	}
}

// modelJSON renders the models.get / models.list entry shape.
func (s *Service) modelJSON(m *model) map[string]any {
	return map[string]any{
		"name":        "models/" + m.name,
		"displayName": m.displayName,
		"description": "Fixture model served by gcp-local (no inference).",
		"supportedGenerationMethods": []string{
			"generateContent",
			"streamGenerateContent",
			"countTokens",
		},
	}
}

func (s *Service) listModels() []map[string]any {
	out := make([]map[string]any, 0, len(s.order))
	for _, name := range s.order {
		out = append(out, s.modelJSON(s.models[name]))
	}
	return out
}

// writeError renders the Gemini-shaped error body. 429 carries a
// google.rpc.RetryInfo detail with retryDelay, matching the real
// RESOURCE_EXHAUSTED response.
func (s *Service) writeError(w http.ResponseWriter, status int, modelName string) {
	msg := fmt.Sprintf("model %s is unavailable (injected failure)", modelName)
	body := map[string]any{
		"error": map[string]any{
			"code":    status,
			"message": msg,
			"status":  statusText(status),
		},
	}
	if status == http.StatusTooManyRequests {
		errBody, _ := body["error"].(map[string]any)
		errBody["details"] = []map[string]any{{
			"@type":      "type.googleapis.com/google.rpc.RetryInfo",
			"retryDelay": s.retryDelayFor(modelName),
		}}
	}
	httpresp.JSON(w, status, body)
}

// retryDelayFor returns the configured retryDelay string for the model's
// failFirst rule (default "5s").
func (s *Service) retryDelayFor(modelName string) string {
	for i := range s.failRules {
		if s.failRules[i].Model == modelName {
			if s.failRules[i].RetryDelay != "" {
				return s.failRules[i].RetryDelay
			}
			return "5s"
		}
	}
	return "5s"
}

// statusText maps an HTTP status to its Gemini error status string.
func statusText(code int) string {
	switch code {
	case http.StatusBadRequest:
		return "INVALID_ARGUMENT"
	case http.StatusUnauthorized:
		return "UNAUTHENTICATED"
	case http.StatusForbidden:
		return "PERMISSION_DENIED"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusTooManyRequests:
		return "RESOURCE_EXHAUSTED"
	case http.StatusInternalServerError:
		return "INTERNAL"
	case http.StatusServiceUnavailable:
		return "UNAVAILABLE"
	default:
		return "UNKNOWN"
	}
}
