Fixed patch
- **Release cut after a closed release PR.** The release manager now
  resets the local `release/vX.Y.Z` branch and force-pushes it. Before,
  a cut stopped with "a branch named 'release/v0.13.3' already exists"
  after the release manager closed a broken release PR, so no release
  shipped.
