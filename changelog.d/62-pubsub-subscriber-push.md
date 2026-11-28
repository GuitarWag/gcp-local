Changed patch
- Split the remaining `internal/services/pubsub/pubsub.go` by area: subscription and pull endpoints move to `subscriber.go`, push delivery to `push.go`; `pubsub.go` is deleted. Wire shapes, error bodies and behaviour are unchanged. Part of #62.
