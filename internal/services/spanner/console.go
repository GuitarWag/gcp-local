package spanner

// ConsoleStatus returns the stub status surfaced on the /console UI.
// Spanner implements CreateSession / DeleteSession so client libraries
// can connect; data RPCs return UNIMPLEMENTED.
func (s *Service) ConsoleStatus() map[string]any {
	return map[string]any{
		"project": s.project,
		"surface": "gRPC: google.spanner.v1.Spanner",
		"state":   "stub",
		"note":    "CreateSession / DeleteSession are implemented. ExecuteSql, BeginTransaction, etc. return UNIMPLEMENTED.",
	}
}
