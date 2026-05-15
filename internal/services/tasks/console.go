package tasks

import (
	"encoding/json"
	"sort"
	"strings"
)

// ConsoleQueues returns one row per queue, sorted by name.
func (s *Service) ConsoleQueues() ([]map[string]any, error) {
	all, err := s.store.List(nsQueues, "")
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(all))
	for _, v := range all {
		var q queueResource
		if json.Unmarshal(v, &q) != nil {
			continue
		}
		out = append(out, map[string]any{"name": q.Name, "id": lastSegment(q.Name)})
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := out[i]["name"].(string)
		b, _ := out[j]["name"].(string)
		return a < b
	})
	return out, nil
}

// ConsoleTasks lists tasks under the given queue. `queue` is the full
// queue name. The console shows scheduleTime and the dispatch URL — the
// per-attempt counter isn't currently tracked through the store, so it
// isn't surfaced here.
func (s *Service) ConsoleTasks(queue string) ([]map[string]any, error) {
	prefix := queue + "/tasks/"
	all, err := s.store.List(nsTasks, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(all))
	for _, v := range all {
		var t taskResource
		if json.Unmarshal(v, &t) != nil {
			continue
		}
		row := map[string]any{
			"name":         t.Name,
			"id":           lastSegment(t.Name),
			"createTime":   t.CreateTime,
			"scheduleTime": t.ScheduleTime,
		}
		if t.HTTPRequest != nil {
			row["url"] = t.HTTPRequest.URL
			row["method"] = t.HTTPRequest.HTTPMethod
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := out[i]["name"].(string)
		b, _ := out[j]["name"].(string)
		return a < b
	})
	return out, nil
}

func lastSegment(name string) string {
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		return name[i+1:]
	}
	return name
}
