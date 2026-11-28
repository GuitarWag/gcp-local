Changed patch
- Split `internal/services/bigquery/bigquery.go` by area: wire types move to `types.go`, service wiring to `service.go`, dataset endpoints to `datasets.go`. Wire shapes, error bodies and behaviour are unchanged. Part of #62.
