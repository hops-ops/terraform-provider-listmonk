### What's changed in v0.2.2

* fix(read): RemoveResource on empty state.ID (upjet expects an empty tfstate, not a no-op) (by @patrickleet)

  Follow-up to ad3aa4d. The bare 'return' on empty state.ID left
  the Terraform Framework with the previously-zero state for the
  resource. upjet's controller then errored with 'observe failed:
  cannot set critical annotations: cannot get external name: cannot
  find id in tfstate' because the tfstate's id field was zero-valued
  rather than missing.

  Calling resp.State.RemoveResource(ctx) properly signals 'resource
  doesn't exist' so upjet falls through to Create cleanly.


See full diff: [v0.2.1...v0.2.2](https://github.com/hops-ops/terraform-provider-listmonk/compare/v0.2.1...v0.2.2)
