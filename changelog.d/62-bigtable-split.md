Changed patch
- Split `internal/services/bigtable/bigtable.go` by area: service wiring moves to `service.go`, the data plane to `data.go`, and table admin to `admin.go`. Wire shapes, error bodies and behaviour are unchanged. Part of #62.
