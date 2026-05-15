package bigtable

// ConsoleStatus returns the stub status surfaced on the /console UI.
// The Bigtable service intentionally returns UNIMPLEMENTED for data
// ops — the page just shows that fact alongside the reachable gRPC
// surface.
func (s *Service) ConsoleStatus() map[string]any {
	return map[string]any{
		"project": s.project,
		"surface": "gRPC: google.bigtable.v2.Bigtable",
		"state":   "stub",
		"note":    "All data RPCs (ReadRows, MutateRow, …) return UNIMPLEMENTED. Connect to discover the surface; no data is stored.",
	}
}
