package spanner

// ConsoleStatus returns the status surfaced on the /console UI. Spanner has
// a data path backed by SQLite: sessions, ExecuteSql, Read, Commit, and the
// DatabaseAdmin DDL calls all work.
func (s *Service) ConsoleStatus() map[string]any {
	return map[string]any{
		"project": s.project,
		"surface": "gRPC: google.spanner.v1.Spanner + google.spanner.database.v1.DatabaseAdmin",
		"state":   "ready",
		"note":    "Sessions, ExecuteSql (SELECT + DML), Read (key sets), Commit/Rollback, and CREATE/ALTER/DROP TABLE are implemented. Partitioned queries, interleaved tables, and indexes are not.",
	}
}
