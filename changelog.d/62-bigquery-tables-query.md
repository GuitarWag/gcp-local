Changed patch
- Split the remaining `internal/services/bigquery/bigquery.go` by area: table endpoints move to `tables.go` and query processing to `query.go`; `bigquery.go` is deleted. Wire shapes, error bodies and behaviour are unchanged. Part of #62.
