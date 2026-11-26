package bigtable

// ConsoleStatus returns the service status surfaced on the /console UI.
func (s *Service) ConsoleStatus() map[string]any {
	return map[string]any{
		"project": s.project,
		"surface": "gRPC: google.bigtable.v2.Bigtable + admin.v2 BigtableTableAdmin",
		"state":   "ready",
		"note":    "ReadRows, MutateRow(s), SampleRowKeys, CheckAndMutateRow and table admin are implemented. ReadModifyWriteRow and query/change-stream RPCs return UNIMPLEMENTED.",
	}
}
