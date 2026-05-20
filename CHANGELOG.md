### What's changed in v0.1.0

* feat: initial release — terraform-provider-listmonk v0.1 (by @patrickleet)

  Terraform provider for Listmonk (https://listmonk.app), the self-hosted
  newsletter and mailing-list manager. Manages the per-section settings
  rows (security.oidc, …), with v0.1 shipping listmonk_security_settings
  covering the OIDC sub-block.

  v0.1 resource scope:
  - listmonk_security_settings (security.oidc sub-block; captcha + basic_auth reserved)

  Auth:
  - HTTP Basic-Auth against an api-typed Listmonk user (the user's password
    IS the token, per Listmonk v5.0.0+ semantics).
  - OIDC client_credentials mode will land in v0.2 once the matching
    upstream Listmonk feature is in place — see specs/listmonk-admin-jwt-auth
    for the design.

  Behavioral guarantees:
  - security.oidc client_secret is treated as write-only: Listmonk masks
    it on GET with bullet characters, so state preserves the plan value
    rather than detecting drift on the masked response.
  - Only declared sub-blocks are reconciled; operator UI edits to
    unmanaged sub-blocks (e.g. CAPTCHA when only OIDC is configured)
    survive every terraform apply.

  Pattern mirrors hops-ops/terraform-provider-openpanel layout (META.d/,
  .copywrite.hcl, .goreleaser.yml, GNUmakefile, internal/{client,provider}/,
  docs/, examples/, tfplugindocs + vnext CI).

  Verified end-to-end on pat-local: terraform apply against the live
  Listmonk security.oidc row produces byte-identical pre/post DB JSONB
  diff; second-run plan is No changes.

  Implements [[tasks/provider-listmonk]]


