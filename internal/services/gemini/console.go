package gemini

// ConsoleStatus returns the status surfaced on the /console UI. Gemini is
// a config-driven fixture service: it holds no state.
func (s *Service) ConsoleStatus() map[string]any {
	return map[string]any{
		"surface": "REST: /v1beta/models/* (generativelanguage)",
		"state":   "ready",
		"note":    "generateContent, streamGenerateContent (SSE), countTokens, and models list/get return deterministic fixture replies from config. No inference, no embeddings, no files API.",
	}
}
