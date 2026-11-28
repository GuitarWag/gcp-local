Fixed patch
- **Pub/Sub: gRPC `GetSubscription` returns `pushConfig`.** A push
  subscription created over REST kept its push endpoint only in the REST
  body. The gRPC response dropped it, so SDK clients that read
  subscription metadata over gRPC saw a subscription without a push
  config. Closes #163.
