Fixed patch
- **Factory watchdog for hung agents.** An agent whose model request got
  no answer hung forever and kept its job slot, so the factory stopped
  starting work. The orchestrator now stops an agent after 20 minutes
  with no log output or 90 minutes in total, and the stage restarts on
  the next tick. pi jobs stream JSON events so that progress shows in
  the log.
