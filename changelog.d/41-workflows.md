Added minor
- **Workflows API emulator.** The emulator now serves the Workflows
  `v1` REST surface: workflow definitions CRUD per location, with every
  mutating call returning an immediately-done Operation that
  `operations.get` retrieves. Executions and the interpreter land in a
  follow-up.
