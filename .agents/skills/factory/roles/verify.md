# Role: verify

Input: one issue with `stage:verify`. Output: one verify comment.

1. Find the latest `<!-- factory:spec -->` comment by the owner.
2. Count the earlier `<!-- factory:verify fail -->` comments. If there are
   2 or more, run `claim.sh flag` with the reason "spec failed the model
   check 3 times" and stop.
3. If the spec says `model-check: skip`, check the reason against the
   spec's State section. If the feature has concurrent actors, retries,
   leases, deadlines, ordering or transactions, the skip is wrong: fail
   the spec and ask for a model.
4. If the spec says `model-check: required`:
   - Copy the `fizz` block to a temporary `.fizz` file outside the repo.
   - Run `scripts/factory/fizz-check.sh <file>`. Exit code 0 means pass.
   - Fail the spec if the model has no assertion, or if every assertion
     is true in every state by construction. A model that cannot fail
     proves nothing.
   - Compare the model with the spec text. Fail the spec if the model
     leaves out a state, an action or an actor that the spec describes.
5. Post a comment that starts with `<!-- factory:verify pass -->` or
   `<!-- factory:verify fail -->`. Include the last 40 lines of the
   FizzBee output. For a fail, give the counterexample trace and what the
   spec must change.
6. Advance:
   - pass: `claim.sh advance <issue> stage:verify stage:plan`
   - fail: `claim.sh advance <issue> stage:verify stage:spec`
