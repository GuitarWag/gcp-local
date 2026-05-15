package monitoring

import (
	"encoding/json"
	"sort"
)

// ConsoleSeries returns up to `limit` recent time series for the
// configured project. Each row carries the metric type, resource type,
// the most recent point's value, and an end-time string.
func (s *Service) ConsoleSeries(limit int) ([]map[string]any, error) {
	all, err := s.store.List(nsSeries, "")
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(all))
	for _, v := range all {
		var ts timeSeries
		if json.Unmarshal(v, &ts) != nil {
			continue
		}
		metricType, _ := ts.Metric["type"].(string)
		resourceType, _ := ts.Resource["type"].(string)
		row := map[string]any{
			"metricType":   metricType,
			"resourceType": resourceType,
			"metricKind":   ts.MetricKind,
			"valueType":    ts.ValueType,
			"points":       len(ts.Points),
		}
		if len(ts.Points) > 0 {
			last := ts.Points[len(ts.Points)-1]
			row["lastEndTime"] = last.Interval.EndTime
			row["lastValue"] = last.Value
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := out[i]["metricType"].(string)
		b, _ := out[j]["metricType"].(string)
		return a < b
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
