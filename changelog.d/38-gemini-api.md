Added minor
- **Gemini API emulator (generativelanguage).** gcp-local now serves the
  Gemini API on the real root paths `/v1beta/models/*` (plus `v1` and
  `v1alpha`): `generateContent`, `streamGenerateContent` as SSE,
  `countTokens`, and models list/get. Replies come from a config-driven
  fixture engine under `services.gemini` — per-model fixture replies,
  configurable streamed chunk counts, simulated latency, and deterministic
  token counts, with no inference and no `state.Store` persistence. Error
  injection knobs (`failFirst`) make the first N calls per model fail with
  a Gemini-shaped error body; 429 responses carry a `google.rpc.RetryInfo`
  detail with a configured `retryDelay`, so client retry and backoff paths
  are testable offline. Auth accepts any API key (`x-goog-api-key` header
  or `?key=`). A console card shows the service status at
  `/console/api/gemini/status`. No function calling, embeddings, files API,
  or model tuning. Closes #38.
