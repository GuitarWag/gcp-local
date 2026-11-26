# Role: review

Input: one issue with `stage:review` and an open PR that closes it.
Output: one review on the PR. You have no context from the builders.
Keep it that way.

1. Find the PR:
   `gh issue view <issue> --json closedByPullRequestsReferences`.
   If there is no open PR, run `claim.sh flag <issue> "review: no open PR"`.
2. Count the earlier `<!-- factory:verdict changes` comments on the PR.
   This review is round N+1.
3. Read the spec, the plan and the full diff (`gh pr diff`). Check the
   PR out in your worktree (`gh pr checkout <pr>`) and run
   `scripts/ci/gate.sh --cover`.
4. Check, in this order:
   - Correctness against the spec, and the wire shapes against the real
     discovery document or protos.
   - Tests use the real SDK clients and fail without the change.
   - `AGENTS.md` rules: files under ~500 lines, `state.Store` only, no
     CGo, no new dependency without an ADR, tests only in `tests/<lang>/`.
   - The "Slop" list in `AGENTS.md`. CI checks the mechanical rules;
     you check what CI cannot: reuse of existing helpers, tests that
     prove behaviour, and real error shapes.
   - CI status (`gh pr checks <pr>`).
5. A blocking finding is a bug, a wire difference, a failed check or a
   broken rule. Everything else is a nit. Nits never block.
6. Post with `gh pr review <pr> --comment --body "..."`. GitHub does not
   let the owner account approve its own PR, so the marker is the verdict.
   Start the body with `<!-- factory:verdict approve round=N -->` or
   `<!-- factory:verdict changes round=N -->`. Number each finding and
   give `file:line`.
7. Advance:
   - approve: `claim.sh advance <issue> stage:review stage:accept`
   - changes and N < 3: `claim.sh advance <issue> stage:review stage:fix`
   - changes and N = 3: `claim.sh flag <issue> "3 review rounds without approval"`
