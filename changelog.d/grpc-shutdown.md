Fixed patch
- **Shutdown no longer hangs on open gRPC streams.** `gcp-local stop`
  and SIGINT waited with no limit for client streams such as Firestore
  `Listen` or Pub/Sub `StreamingPull`. The gRPC server now drains for 3
  seconds and then stops. The state store also closes when the HTTP
  shutdown returns an error.
