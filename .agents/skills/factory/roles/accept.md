# Role: accept (holdout evaluator)

Input: one issue with `stage:accept`, an approved PR, and the holdout
folder that your prompt names. Output: one public accept comment on the
issue, and one full report in the holdout folder.

The builders never see the scenarios. Keep it that way: no scenario
text, title or detail goes into a public comment, a commit in gcp-local,
or a PR.

1. Read the scenarios in `<holdout>/scenarios/<issue>/`. If there are
   none (the issue was specified before the holdout stage existed),
   write them first, as the spec role describes, from the issue body and
   the spec comment only. Do not read the PR before you write them.
   Commit and push them to the holdout repo.
2. Count the earlier `<!-- factory:accept fail` comments on the issue.
   If there are 3 or more, run `claim.sh flag <issue> "holdout failed 3 times"`
   and stop.
3. Find the PR (`gh issue view <issue> --json closedByPullRequestsReferences`)
   and check it out in your worktree (`gh pr checkout <pr>`).
4. For each scenario, write one short program with the real SDK named in
   the scenario. Put it in the test folder of that language, in a file
   whose name starts with `zz_holdout_`:
   - Go: `tests/go/zz_holdout_<id>_test.go`, run with `go test -run`.
     Use `testutil.Start` to start the emulator from the PR code.
   - Python: `tests/python/test_zz_holdout_<id>.py`, run with pytest.
   - TypeScript: `tests/typescript/zz_holdout_<id>.test.ts`, run with vitest.
   The program asserts exactly the "Then" lines. Do not change any other
   file. Never commit or push these files.
5. Run each program 3 times. A scenario passes when 2 of the 3 runs pass.
   If a program fails because of a mistake in the program itself, fix the
   program, not the verdict.
6. The gate passes when every P0 scenario passes and at least 90% of all
   scenarios pass.
7. Write the full report to `<holdout>/reports/<issue>/<UTC time>.md`:
   each scenario, its 3 results and the output of failed runs. Commit and
   push it (`git pull --rebase` first).
8. Post a comment on the issue that starts with
   `<!-- factory:accept pass -->` or `<!-- factory:accept fail -->`.
   Give the pass rate. For each failed scenario, give its id and one line
   about the observed behaviour, for example
   `S3: pull returned the message with an empty attributes map.`
   Do not quote the scenario.
9. Delete the `zz_holdout_` files. Advance:
   - pass: `claim.sh advance <issue> stage:accept stage:merge`
   - fail: `claim.sh advance <issue> stage:accept stage:fix`
