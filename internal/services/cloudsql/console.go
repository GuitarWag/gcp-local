package cloudsql

import (
	"encoding/json"
	"sort"
)

// ConsoleInstances lists every configured CloudSQL instance with the
// engine, port, and database name. The wire listener itself is owned by
// pgwire and reachable on the returned port.
func (s *Service) ConsoleInstances() ([]map[string]any, error) {
	all, err := s.store.List(nsInstances, "")
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(all))
	for _, v := range all {
		var inst instance
		if json.Unmarshal(v, &inst) != nil {
			continue
		}
		engine := inst.Engine
		if engine == "" {
			engine = inst.DatabaseVersion
		}
		out = append(out, map[string]any{
			"name":     inst.Name,
			"engine":   engine,
			"port":     inst.Port,
			"host":     inst.Host,
			"database": inst.Database,
			"state":    inst.State,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := out[i]["name"].(string)
		b, _ := out[j]["name"].(string)
		return a < b
	})
	return out, nil
}
