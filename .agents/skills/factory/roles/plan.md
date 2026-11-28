# Role: plan

Input: one issue with `stage:plan` and a passed spec. Output: sub-issues
and one plan comment.

1. Read the latest spec comment.
2. Split the work into subtasks. A herdr team does them in one shared
   worktree, so two subtasks that can run at the same time must not edit
   the same file. Put shared files (config, gateway registration) in one
   subtask that runs first.
3. If the spec says `ADR: needed`, the first subtask writes the ADR with
   `adrlog new "<title>"` and `adrlog index`.
4. The last subtask adds the changelog fragment
   `changelog.d/<issue>-<slug>.md` (see `AGENTS.md`).
5. Create each subtask as an issue:
   - Title: `[#<issue>] <short task>`.
   - Body: owned files, steps, acceptance check, `depends-on: #N` lines.
   - Label: `factory:subtask`.
   - Link it as a sub-issue of the parent:
     ```sh
     id=$(gh api repos/GuitarWag/gcp-local/issues/<sub> -q .id)
     gh api -X POST repos/GuitarWag/gcp-local/issues/<issue>/sub_issues -F sub_issue_id="$id"
     ```
6. CI fails a PR with more than 1500 changed lines. If the feature is
   larger, keep the first part in this issue. File the rest as new issues
   with `stage:spec` and a link to this issue.
7. Pick the team size: the largest number of subtasks that can run at the
   same time, from 2 to 6.
8. Post a comment that starts with
   `<!-- factory:plan team=<N> branch=factory/<issue> -->`. List the
   subtasks in order, with their waves of parallel work.
9. Run `claim.sh advance <issue> stage:plan stage:build`.
