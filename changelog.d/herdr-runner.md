Changed minor
- **Visible factory agents in herdr.** `FACTORY_RUNNER=herdr` runs every
  stage job as an interactive agent in its own tab of a herdr workspace
  named `factory`, and runs the janitor and the scout as resident agents
  that stay open. The owner can read and chat with every agent. Tabs
  close when their job advances the issue; the watchdog stops a job whose
  screen does not change for 20 minutes. The factory pins the opencode
  model through `~/.factory/opencode.json`. The default runner stays
  `headless`.
