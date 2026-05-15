package scheduler

import (
	"encoding/json"
	"sort"
	"time"
)

// ConsoleJobs returns every job with its schedule and a computed
// next-fire time. Last-fire isn't currently tracked, so it's omitted.
func (s *Service) ConsoleJobs() ([]map[string]any, error) {
	all, err := s.store.List(nsJobs, "")
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	out := make([]map[string]any, 0, len(all))
	for _, v := range all {
		var j jobResource
		if json.Unmarshal(v, &j) != nil {
			continue
		}
		row := map[string]any{
			"name":     j.Name,
			"schedule": j.Schedule,
			"state":    j.State,
		}
		if j.HTTPTarget != nil {
			row["uri"] = j.HTTPTarget.URI
			row["method"] = j.HTTPTarget.HTTPMethod
		}
		if sched, err := parseSchedule(j.Schedule); err == nil {
			row["nextFire"] = sched.Next(now)
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
