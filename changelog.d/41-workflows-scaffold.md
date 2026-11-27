Changed patch
- **Workflows service scaffold.** The emulator wires a new `workflows`
  service (enabled by default) into the gateway, config and health
  registry. It serves no routes yet — workflow CRUD, operations and
  executions land next. Part of #41.
