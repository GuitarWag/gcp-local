package cloudrun

import (
	"encoding/json"
	"sort"
)

// ConsoleResources returns every service or function this Service
// instance manages (which one depends on whether it was constructed via
// NewCloudRun or NewFunctions).
func (s *Service) ConsoleResources() ([]map[string]any, error) {
	all, err := s.store.List(s.ns, "")
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(all))
	for _, v := range all {
		var r resource
		if json.Unmarshal(v, &r) != nil {
			continue
		}
		out = append(out, map[string]any{
			"name":       r.Name,
			"backendUrl": r.BackendURL,
			"createTime": r.CreateTime,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := out[i]["name"].(string)
		b, _ := out[j]["name"].(string)
		return a < b
	})
	return out, nil
}

// ConsoleKind returns whether this is a "service" or "function" instance
// so the console page can label itself appropriately.
func (s *Service) ConsoleKind() string {
	return string(s.kind)
}
