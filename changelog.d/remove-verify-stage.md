Removed patch
- **FizzBee verify stage.** The factory no longer runs a FizzBee model
  check between spec and plan, because FizzBee used too much memory.
  The spec role now advances an issue to `stage:plan`.
