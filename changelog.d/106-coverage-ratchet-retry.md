Fixed patch

- **Coverage ratchet re-measures a failing drop.** When a package fails
  the coverage comparison, `scripts/ci/coverage.sh` now re-measures head
  and base once and fails only when the drop reproduces on the second
  measurement, so a one-run coverage loss no longer fails CI.
  Closes #106.
