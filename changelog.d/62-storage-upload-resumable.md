Changed patch
- **Storage internals regrouped.** No behaviour change. The upload and
  resumable-session code moved from `objects.go` into `upload.go` and
  `resumable.go` to keep every file in the package under the size limit.
