Changed patch
- Split `internal/services/pubsub/pubsub.go` by area: wire types move to `types.go`, service wiring to `service.go`, topic and publish endpoints to `publisher.go`. Wire shapes, error bodies and behaviour are unchanged. Part of #62.
