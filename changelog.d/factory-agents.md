Changed patch
- **Autonomous agent factory.** Agent instructions moved from `CLAUDE.md`
  to `AGENTS.md`, so pi, opencode, Codex and Claude Code read the same
  file. A `factory` skill in `.agents/skills/` and the scripts in
  `scripts/factory/` run the full issue lifecycle with no human gate:
  spec, FizzBee model check, plan into sub-issues, herdr team build,
  review, fix, merge and release, plus a janitor and a feature scout.
  PRs now add a fragment to `changelog.d/` instead of editing
  `CHANGELOG.md`; the release manager cuts the version.
