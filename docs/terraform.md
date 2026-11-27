# Using gcp-local with Terraform

The `hashicorp/google` provider accepts a custom endpoint per service and a
pre-minted OAuth access token. That is enough to point every Terraform
resource at `gcp-local` instead of real Google Cloud.

This page documents the harness under `tests/terraform/`. It pins one
provider version, sets four endpoint overrides, and runs `terraform` against
the emulator end to end.

## Versions

The harness pins and is tested against:

- Terraform CLI: `v1.17.2` (darwin/arm64).
- Provider: `hashicorp/google` `= 8.10.0` (see `tests/terraform/versions.tf`).

Other versions probably work, but the harness does not verify them.

## Provider configuration

Two things make the provider talk to the emulator and nowhere else: a fake
access token and a custom endpoint per service.

### Auth

```bash
export GOOGLE_OAUTH_ACCESS_TOKEN=fake-token
```

The provider sends this token as a bearer token and never contacts
`oauth2.googleapis.com`. The emulator ignores the `Authorization` header, so
the value does not matter. Set it before `terraform init`.

### Endpoint overrides

The provider builds each service URL from a `*_custom_endpoint` attribute.
The harness sets one per emulated service, all on the emulator port:

```hcl
provider "google" {
  project = "local-project"
  region  = "us-central1"

  storage_custom_endpoint         = "http://localhost:<port>/storage/v1/"
  pubsub_custom_endpoint          = "http://localhost:<port>/v1/"
  secret_manager_custom_endpoint  = "http://localhost:<port>/v1/"
  cloud_run_custom_endpoint       = "http://localhost:<port>/v1/"
}
```

Note the shapes: the storage override includes `/storage/v1/`, the other
three include `/v1/`. Each ends with a slash. A missing or wrong path makes
the provider call real Google Cloud or a dead URL.

## Run the harness

Start the emulator on a known port:

```bash
gcp-local start --port=<port>
```

Then run the harness:

```bash
tests/terraform/run.sh
```

`run.sh` performs these steps in order:

1. `terraform init` — download and pin the provider.
2. `terraform plan` — confirm the config parses against the provider schema.
3. `terraform apply` — create the resources through the emulator.
4. `tests/terraform/verify.py` — check each resource against the emulator's
   own API, independent of Terraform.
5. `terraform destroy` — delete the resources through the emulator.
6. `tests/terraform/verify.py` again — check the destroy-time state.

A non-zero exit from any step fails the harness.

## Known gaps

### Cloud Run

The emulator serves Cloud Run only under `/v2/` and returns a bare `Service`
object with no operation wrapper. The provider's `google_cloud_run_service`
resource uses the v1 surface and expects an operation object, so its apply
fails.

The harness keeps that config in `tests/terraform/cloudrun/` and reports it
as a known gap. It does not fail the run. The gap is tracked as follow-up
issue #94.
