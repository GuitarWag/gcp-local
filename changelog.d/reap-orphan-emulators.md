Fixed patch
- **Orphan test emulators.** When the factory stops an agent job, the
  test emulators of that job kept running. 248 of them used memory on
  the factory Mac. The orchestrator now stops test emulators and
  `echofn` fixtures that have no parent process on each tick.
