Added minor

- **Cloud Run v1 admin surface.** Services CRUD (create, list, get,
  replace, delete) now works on both the regional
  `/v1/projects/{p}/locations/{l}/services` paths and the namespaced
  `/v1/apis/serving.knative.dev/v1/namespaces/{p}/services` prefix that
  the pinned Terraform provider calls, plus `/v1` operations get and
  list endpoints. Service responses match the real
  serving.knative.dev/v1 API: `metadata.generation`, Ready conditions,
  `status.url`, and a verbatim spec echo. Mutations return the real
  shapes (`Service`, `Service`, empty `Status`), never an operation
  envelope. Closes #94.
