# Using gcp-local with the `gcloud` CLI

The GCP SDK client libraries honour `STORAGE_EMULATOR_HOST`,
`PUBSUB_EMULATOR_HOST`, and friends — point them at `localhost:4443` and
they hit the emulator transparently. The `gcloud` CLI does not. It only
talks to the endpoints configured in its own `gcloud config`.

This page is how to make `gcloud storage cp ...`,
`gcloud pubsub topics publish ...`, `gcloud secrets versions access ...`,
etc. hit `gcp-local` instead of real Google Cloud.

## TL;DR

```bash
gcp-local start
eval "$(gcp-local gcloud-setup)"

# now any gcloud command in this shell hits the emulator
gcloud storage buckets create gs://my-bucket
gcloud pubsub topics create orders

# when you're done, switch back to real GCP
eval "$(gcp-local gcloud-teardown)"
```

`gcloud-setup` and `gcloud-teardown` print shell commands; `eval` runs
them. Same pattern as `gcp-local env`.

## What `gcloud-setup` does

It creates and activates a dedicated `gcp-local` gcloud configuration so
your real-GCP setup stays untouched, disables credentials (the emulator
doesn't enforce auth), and sets one `api_endpoint_overrides/<service>`
per service we emulate.

You can preview the exact commands without running them:

```bash
gcp-local gcloud-setup
```

Output (abridged):

```
gcloud config configurations create --no-activate gcp-local 2>/dev/null || true
gcloud config configurations activate gcp-local
gcloud config set auth/disable_credentials true
gcloud config set account local-dev
gcloud config set project local-project
gcloud config set api_endpoint_overrides/storage http://localhost:4443/
gcloud config set api_endpoint_overrides/pubsub http://localhost:4443/
gcloud config set api_endpoint_overrides/secretmanager http://localhost:4443/
gcloud config set api_endpoint_overrides/cloudtasks http://localhost:4443/
gcloud config set api_endpoint_overrides/cloudscheduler http://localhost:4443/
gcloud config set api_endpoint_overrides/cloudkms http://localhost:4443/
gcloud config set api_endpoint_overrides/logging http://localhost:4443/
gcloud config set api_endpoint_overrides/monitoring http://localhost:4443/
gcloud config set api_endpoint_overrides/bigquery http://localhost:4443/
gcloud config set api_endpoint_overrides/firestore http://localhost:4443/
gcloud config set api_endpoint_overrides/run http://localhost:4443/
gcloud config set api_endpoint_overrides/cloudfunctions http://localhost:4443/
gcloud config set api_endpoint_overrides/sqladmin http://localhost:4443/
```

Flags:

| Flag | Default | Purpose |
|------|---------|---------|
| `--port=N` | pidfile, else `4443` | Emulator port. |
| `--project=ID` | `local-project` | Project id the gcloud config will use. |
| `--config=NAME` | `gcp-local` | gcloud configuration name. |
| `--tls` | `false` | Use `https://` URLs (when emulator runs with `--tls`). |

## Switching back to real GCP

```bash
eval "$(gcp-local gcloud-teardown)"
```

This activates the `default` gcloud configuration. The `gcp-local`
configuration is left in place so you can re-activate it later with
`gcloud config configurations activate gcp-local`.

To remove the configuration entirely:

```bash
eval "$(gcp-local gcloud-teardown --delete)"
```

## Manual setup (no `gcp-local` binary in scope)

If for some reason you want to do it by hand:

```bash
gcloud config configurations create gcp-local
gcloud config configurations activate gcp-local
gcloud config set auth/disable_credentials true
gcloud config set account local-dev
gcloud config set project local-project

for svc in storage pubsub secretmanager cloudtasks cloudscheduler \
           cloudkms logging monitoring bigquery firestore run \
           cloudfunctions sqladmin; do
  gcloud config set "api_endpoint_overrides/$svc" "http://localhost:4443/"
done
```

To switch back:

```bash
gcloud config configurations activate default
```

## What works, what doesn't

`gcloud` uses the same REST surfaces as the SDK client libraries plus a
few extras (metadata server probes, IAM permission checks, OAuth token
refresh). The emulator covers the data-plane endpoints; the auxiliary
endpoints are bypassed by `auth/disable_credentials true`.

The table below lists one canonical command per service. "Works" means
the command completes against the emulator with the expected effect.
"Partial" means basic verbs work but some flags or subcommands fall
through to unimplemented endpoints. "Stub" means CRUD-style admin works
but data-plane operations return errors.

| Service | Status | Example |
|---------|--------|---------|
| Storage | works | `gcloud storage buckets create gs://demo` <br> `echo hi > /tmp/x && gcloud storage cp /tmp/x gs://demo/x` <br> `gcloud storage ls gs://demo` |
| Pub/Sub | works | `gcloud pubsub topics create orders` <br> `gcloud pubsub subscriptions create sub-orders --topic=orders` <br> `gcloud pubsub topics publish orders --message='hello'` |
| Secret Manager | works | `printf foo \| gcloud secrets create my-secret --data-file=-` <br> `gcloud secrets versions access latest --secret=my-secret` |
| Cloud Tasks | works | `gcloud tasks queues create my-queue --location=us-central1` <br> `gcloud tasks queues list --location=us-central1` |
| Cloud Scheduler | works | `gcloud scheduler jobs create http my-job --schedule='* * * * *' --uri=http://example/ping --location=us-central1` |
| KMS | works | `gcloud kms keyrings create my-ring --location=us-central1` <br> `gcloud kms keys create my-key --keyring=my-ring --location=us-central1 --purpose=encryption` |
| Cloud Logging | works | `gcloud logging write my-log 'hello from emulator'` <br> `gcloud logging read 'logName:my-log' --limit=10` |
| Cloud Monitoring | works | `gcloud monitoring metrics-scopes list` (read paths) |
| Firestore | partial | gRPC data-plane is what the emulator implements; most `gcloud firestore` admin verbs (database/index management) are not. |
| BigQuery | partial | `gcloud` defers heavily to the `bq` CLI; basic dataset/table commands via REST work, complex SQL surface is the same subset documented in the README. |
| Cloud Run | partial | REST CRUD works (`gcloud run services list`); deploy/invoke require subprocess execution which the emulator does not do. |
| Cloud Functions | partial | Same as Cloud Run. |
| CloudSQL | partial | `gcloud sql instances create my-db --database-version=POSTGRES_15 --tier=db-f1-micro --region=us-central1` — instance admin works; connecting via `gcloud sql connect` is not wired up (use the Postgres wire endpoint directly). |
| Bigtable | stub | Admin RPCs partially answer; data path returns `UNIMPLEMENTED`. |
| Spanner | stub | `CreateSession` works; data verbs return `UNIMPLEMENTED`. |

> **Verify locally.** `gcloud` versions move; the exact set of commands
> that round-trip cleanly varies. When in doubt, run the command, watch
> the emulator's request log on the dashboard
> (`http://localhost:4443/dashboard`), and file an issue against any
> surface that should work but doesn't.

## Caveats

- `gcloud` calls the metadata server (`metadata.google.internal`) for
  some commands. `auth/disable_credentials true` skips most of those,
  but you may still see warnings on stderr — they're cosmetic.
- Some `gcloud` subcommands invoke `bq`, `gsutil`, or `cbt` under the
  hood. Those tools have their own configuration; `api_endpoint_overrides`
  only affects `gcloud` itself.
- The emulator listens on a single host:port. If you change the port
  with `gcp-local start --port=N`, re-run `gcloud-setup` to refresh the
  overrides.
- If you start the emulator with `--tls`, pass `--tls` to
  `gcloud-setup` so the overrides use `https://`. You'll also want
  `gcp-local trust install` so `gcloud` accepts the self-signed cert.
