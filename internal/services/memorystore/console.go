package memorystore

// ConsoleStatus returns a small status summary for the /console UI:
// host:port and the current key count from the embedded miniredis.
func (s *Service) ConsoleStatus() map[string]any {
	keys := s.r.Keys() // miniredis exposes a slice of all keys
	return map[string]any{
		"host":     "127.0.0.1",
		"port":     s.port,
		"keyCount": len(keys),
		"keys":     truncate(keys, 50),
	}
}

func truncate(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
