Added minor
- **Terraform acceptance harness.** `tests/terraform/run.sh` runs the
  hashicorp/google provider (pinned = 8.10.0) against a fresh gcp-local
  emulator and applies the storage bucket, pubsub topic, pubsub push
  subscription and secret manager secret resources. The harness asserts
  each resource through the real Google SDKs, destroys cleanly, and
  reports the Cloud Run v1 gap (issue #94) without failing the run.
  See docs/terraform.md.
