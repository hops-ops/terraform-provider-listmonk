### What's changed in v0.2.1

* fix(read): handle empty state.ID gracefully for upjet pre-Create observe (by @patrickleet)

  upjet's reconcile loop calls Read BEFORE Create to determine whether
  the resource already exists. For IdentifierFromProvider-style
  external-name configs (server-assigned IDs at Create time), the
  state.ID is empty on the first observe — strconv.ParseInt("", 10, 64)
  errors with 'invalid syntax', the reconcile fails, and the resource
  never gets created.

  Surfaced by the v0.0.1 upjet provider smoke test on pat-local: a
  fresh UserRole MR hit 'observe failed: cannot run refresh: refresh
  failed: Invalid user_role id in state' and never progressed past
  Synced=False.

  Fix: early-return from Read when state.ID is null/unknown/empty.
  Same fix applied to user_resource.go (also uses ParseInt on state.ID).

  Existing TF-only consumers aren't affected because Terraform itself
  only calls Read on resources that already have state — the empty-ID
  path is upjet-specific.


See full diff: [v0.2.0...v0.2.1](https://github.com/hops-ops/terraform-provider-listmonk/compare/v0.2.0...v0.2.1)
