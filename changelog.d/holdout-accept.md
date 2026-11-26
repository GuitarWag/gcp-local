Changed patch
- **Holdout acceptance stage.** The factory adds `stage:accept` between
  review and merge. The spec agent writes plain-English acceptance
  scenarios into a private holdout repo that the build agents never get.
  After review, an evaluator agent runs each scenario 3 times with the
  real SDKs against the PR and passes the PR only when every P0 scenario
  and 90% of all scenarios pass. Failures go back to the fix team as one
  line for each scenario.
