Changed patch
- Split `internal/gateway/gateway.go` by area: per-service construction moves to `services.go` as one `init` helper per service. Construction order, health registry order and error wrapping are unchanged.
