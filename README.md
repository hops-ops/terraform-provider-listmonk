# Terraform Provider: Listmonk

A Terraform provider for [Listmonk](https://listmonk.app) — the self-hosted
newsletter and mailing-list manager. Manages the per-section `settings`
rows (`security.oidc`, `app.root_url`, ...), user roles, API users,
lists, list roles, and templates via Listmonk's REST API.

Use this provider to:

- Close the declarative gap left by Listmonk's `settings` table — the
  runtime source of truth for OIDC config, root URL, from-address, etc.
  (Listmonk's `koanf`-loaded env vars and `config.toml` are overridden
  at startup by the DB row, so envs alone cannot enable OIDC).
- Pre-provision Listmonk admin users when running with
  `auto_create_users = false` (Listmonk's OIDC subject-to-role mapping
  does not consume `groups` / `roles` claims natively).
- Drive Listmonk from the upjet-generated Crossplane provider
  [`hops-ops/provider-listmonk`](https://github.com/hops-ops/provider-listmonk),
  which is generated from this Terraform provider.

## Status

| | |
|---|---|
| Tracks Listmonk | knadh/listmonk ≥ v6.x |
| Auth mode | HTTP Basic-Auth against an `api`-typed Listmonk user |
| v0.1 resources | `listmonk_security_settings` (OIDC sub-block) |
| Planned for v0.1 | `listmonk_app_settings`, `listmonk_user_role`, `listmonk_user`, `listmonk_list`, `listmonk_list_role`, `listmonk_template` |
| Latest release | See [Releases](https://github.com/hops-ops/terraform-provider-listmonk/releases) |

## Installing

### From the Terraform Registry

```hcl
terraform {
  required_providers {
    listmonk = {
      source  = "hops-ops/listmonk"
      version = "~> 0.1"
    }
  }
}
```

### Locally (for development)

Set up [`dev_overrides`][dev-overrides] in `~/.terraformrc`:

```hcl
provider_installation {
  dev_overrides {
    "hops-ops/listmonk" = "/path/to/your/$GOPATH/bin"
  }
  direct {}
}
```

Then `go install` from a clone of this repo.

[dev-overrides]: https://developer.hashicorp.com/terraform/cli/config/config-file#development-overrides-for-provider-developers

## Authentication

The provider uses HTTP Basic-Auth against any Listmonk user with
sufficient permissions. For automation, mint a user with `type = api`
in the Listmonk admin UI (Users > New) — the value entered as the
user's "password" IS the API token.

```hcl
provider "listmonk" {
  endpoint = "https://marketing.example.com"
  username = "crossplane-provider"
  token    = var.listmonk_token
}
```

Environment fallback: `LISTMONK_ENDPOINT`, `LISTMONK_USERNAME`,
`LISTMONK_TOKEN`.

### Required user permissions

The `api`-typed user driving the provider needs role permissions for
every resource it touches. For the full v0.1 scope:

- `settings:get`, `settings:manage`
- `users:get`, `users:manage`
- `roles:get`, `roles:manage`
- `lists:get`, `lists:manage_all`
- `templates:get`, `templates:manage`

The companion [EmailMarketingStack v3+][stack] composition mints a
`crossplane-provider` user wired to a `crossplane-provider` role
carrying exactly these perms.

[stack]: https://github.com/hops-ops/hops-ops/tree/main/xrs/stacks/k8s/email-marketing

## Resources

### `listmonk_security_settings`

Manages the `security.*` rows of Listmonk's settings table. Singleton
per Listmonk instance.

```hcl
resource "listmonk_security_settings" "main" {
  oidc {
    enabled              = true
    provider_url         = "https://auth.example.com"
    provider_name        = "Zitadel"
    client_id            = var.oidc_client_id
    client_secret        = var.oidc_client_secret
    auto_create_users    = false
    default_user_role_id = null
    default_list_role_id = null
  }
}
```

**Drift behavior**: only sub-blocks declared in HCL are reconciled.
Operator UI edits to *undeclared* sub-blocks (e.g. CAPTCHA when only
`oidc` is configured) are preserved. Operator UI edits to *declared*
sub-blocks are reverted on the next `terraform apply`.

**Client secret**: Listmonk masks `oidc.client_secret` with bullet
characters on read, so the provider treats it as write-only — drift on
the secret cannot be detected. Rotate by changing the HCL value and
applying.

## Roadmap

| Resource | Status |
|---|---|
| `listmonk_security_settings` (oidc) | ✅ v0.1 |
| `listmonk_security_settings` (captcha, basic_auth) | reserved |
| `listmonk_app_settings` | planned v0.1 |
| `listmonk_user_role` | planned v0.1 |
| `listmonk_user` | planned v0.1 |
| `listmonk_list` | planned v0.1 |
| `listmonk_list_role` | planned v0.1 |
| `listmonk_template` | planned v0.1 |
| `listmonk_campaign` | planned v0.2 (lifecycle-aware) |

No `Sequence` / Automation resource — Listmonk has no top-level
Automation API endpoint; drip sequences are series of Campaigns with
`send_at` schedules. No `Subscriber` resource either — high-cardinality
event-driven data lives outside declarative IaC.

## Development

### Requirements

- Go ≥ 1.25
- Terraform ≥ 1.13

### Build & generate

```shell
go install
make generate    # regenerates docs/ from the provider schema
```

### Tests

```shell
make test     # unit
make testacc  # acceptance — needs LISTMONK_ENDPOINT + LISTMONK_USERNAME + LISTMONK_TOKEN
```

### Release

Releases are driven by [vnext][vnext] from conventional commits on
`main`. Pushing a `feat:` / `fix:` commit to main:

1. CI runs build + lint + docs-up-to-date checks.
2. `vnext` calculates the next semver and pushes a tag.
3. The `on-version-tagged` workflow runs goreleaser, signs the
   binaries with the repo's GPG key, and publishes them as a GitHub
   Release. The Terraform Registry picks the release up automatically.

[vnext]: https://github.com/unbounded-tech/vnext

## Related repos

| Repo | Purpose |
|---|---|
| [`hops-ops/provider-listmonk`](https://github.com/hops-ops/provider-listmonk) | Crossplane provider generated from this Terraform provider via upjet |
| [`hops-ops/listmonk-chart`](https://github.com/hops-ops/listmonk-chart) | Helm chart used by EmailMarketingStack |
| [`hops-ops/hops-ops`](https://github.com/hops-ops/hops-ops) (`xrs/stacks/k8s/email-marketing`) | Crossplane stack that installs Listmonk + composes this provider's MRs |

## Acknowledgements

The HTTP-client + Template-resource shape is influenced by
[`Muravlev/terraform-provider-listmonk`](https://github.com/Muravlev/terraform-provider-listmonk).
We do not fork — v0.1's surface is several net-new resources — but we
intend to open upstream PRs for the resources we add as a goodwill
contribution to the Terraform community.

## License

MPL-2.0
