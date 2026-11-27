Changed patch
- **Idle team nudge and more teams.** When every agent of a build or fix
  team is idle for 10 minutes (`FACTORY_TEAM_IDLE`), the orchestrator
  tells the boss to check the worker reports, send missed tasks again,
  or push and open the PR. Up to 6 teams run at the same time (was 2).
