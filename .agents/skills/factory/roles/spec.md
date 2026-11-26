# Role: spec

Input: one issue with `stage:spec`. Output: one spec comment on the issue.

1. Read the issue body and all owner comments. If a
   `<!-- factory:verify fail -->` comment exists, the previous spec failed
   the model check. Fix the design so that the counterexample cannot happen.
2. Read the code that the feature touches. Read the real API surface:
   the discovery document
   (`https://<service>.googleapis.com/$discovery/rest?version=<v>`) for
   REST, or the protos in `github.com/googleapis/googleapis` for gRPC.
3. Check `docs/adr/` for accepted decisions. Do not contradict an
   accepted ADR. If the feature needs a new architecture, engine, wire or
   dependency decision, say so in the spec. The plan role then adds an
   ADR subtask.
4. Post the spec comment. Start it with `<!-- factory:spec -->`. Include:
   - **Surface**: the exact REST paths or gRPC methods, with the real
     request and response JSON shapes.
   - **State**: what the service stores in `state.Store`, the states of
     each resource, and the transitions between them.
   - **Errors**: the real GCP error code for each failure.
   - **Config**: new YAML keys, if any.
   - **Tests**: which real SDK calls in `tests/go`, `tests/python` and
     `tests/typescript` prove the feature.
   - **Out of scope**: what the emulator does not do.
   - **Files**: the packages and files that change.
   - **ADR**: `needed: <title>` or `none`.
   - **Model check**: `model-check: required` or `model-check: skip`,
     with one sentence of reason.
5. Use `model-check: required` when the feature has concurrent actors,
   retries, leases, deadlines, ordering, or transactions. Examples:
   Pub/Sub ack deadlines, Tasks retries, Spanner transactions, Eventarc
   delivery, Workflows steps. Then add a fenced `fizz` block with a
   FizzBee model of the state machine. Model the actions that can
   interleave. Write `always assertion` blocks for the safety rules, and
   `eventually` assertions for the liveness rules. Reference:
   https://fizzbee.io/design/tutorials/getting-started/
6. Use `model-check: skip` only for stateless or strictly sequential
   features, for example a fixture reply or a read-only list.
7. Write the holdout scenarios in the holdout folder that your prompt
   names, as `scenarios/<issue>/S<n>.md`. The format is in that folder's
   `README.md`. Write 3 to 8 scenarios for each issue:
   - Each scenario checks one behaviour that a real SDK user sees, with a
     real SDK call. Mix Go, Python and TypeScript when the SDKs differ.
   - Cover the acceptance criteria from a different angle than the
     **Tests** section of the public spec. Include at least one error
     case with the real GCP error code.
   - Mark the core acceptance criteria `P0`.
   Commit and push them to the holdout repo (`git pull --rebase` first).
   Never put scenario text in the public spec comment. Write only
   `holdout: <N> scenarios` there.
8. Run `scripts/factory/claim.sh advance <issue> stage:spec stage:verify`.
