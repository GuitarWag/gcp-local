Added minor
- **Service Usage API emulator.** The emulator now serves the Service Usage API
  at `/v1/projects/{project}/services`: list with `filter=state:ENABLED|DISABLED`
  plus `pageSize` and `pageToken`, get, and `:enable` / `:disable` verbs that
  return completed long-running operations. `services:batchEnable` enables up
  to 20 services in one call. A static catalog covers the 19 emulated services,
  and per-project enable/disable state persists through the shared state store.
  Closes #40.
