### What's changed in v0.2.0

* feat: add listmonk_user_role, listmonk_user, listmonk_app_settings (by @patrickleet)

  Three new resources rounding out the v0.2 scope from
  [[specs/provider-listmonk]] (per spec resource matrix).

  listmonk_user_role:
  - Full CRUD against /api/roles/users (POST + PUT + DELETE) +
    list-and-filter on the unparameterized GET /api/roles/users.
  - Note: DELETE endpoint is the unsegmented /api/roles/:id (the same
    endpoint serves both user-role + list-role deletes).
  - TF ID is the numeric Listmonk ID stringified.

  listmonk_user:
  - Full CRUD against /api/users (POST, GET /:id, PUT /:id, DELETE /:id).
  - Two flavors via `type`:
      `user`  — interactive admin; password_login=true; password is the
                operator-chosen value (stored bcrypt-hashed server-side;
                not returned on Read).
      `api`   — machine credential; password_login=false; password is
                the plaintext token used for HTTP Basic-Auth on /api/.
  - Listmonk's apiUsers cache is loaded only at pod startup
    ([[reference_listmonk_apiusers_cache_at_startup]]) so freshly-
    created api users need a Listmonk pod restart before they can
    authenticate via this provider — flagged in resource docstring.
  - Asymmetric request/response wire shape: POST/PUT accepts flat
    `user_role_id` + `list_role_id` integer fields, but GET returns
    nested `user_role: {id, name, permissions}` + `list_role` objects.
    Custom (Un)MarshalJSON on the User struct bridges both; consumers
    see a flat shape.

  listmonk_app_settings:
  - Mirrors the listmonk_security_settings pattern but each declared
    field becomes its OWN PUT /api/settings/app.<key> call (Listmonk's
    app.* settings are scalar-valued rows; security.oidc was a single
    row holding a struct).
  - v0.1 covers root_url, from_email, site_name, logo_url, favicon_url,
    lang, concurrency, message_rate. Other app.* keys (batch_size,
    max_send_errors, etc.) can be added in later versions.
  - Singleton per Listmonk instance; ID is the sentinel `app`.
  - Fields left null in HCL are not touched on the server — operator
    UI edits to unmanaged keys survive every apply.

  Provider registers all three alongside listmonk_security_settings.
  Docs regenerated via tfplugindocs.

  Smoke-tested end-to-end on pat-local against marketing-listmonk:
  - cold install: user_role created (id=3) + user created (id=8) with
    user_role_id=3 + app_settings PUT 6 fields (write-back-same =
    no-op, pre/post DB diff exit=0)
  - second-run plan: No changes — all 3 resources round-trip lossless
    (the User's nested user_role response correctly normalizes to flat
    user_role_id state)
  - destroy: all 3 Delete handlers exit cleanly; DB rows for role + user
    gone; app_settings is no-op-on-Delete (rows are migration-seeded
    and persist) per the established settings-resource contract

  Bug caught during smoke test: my initial test config used permission
  "lists:get" but Listmonk's valid permission is "lists:get_all" —
  Listmonk's POST /api/roles/users correctly rejected it with HTTP 400.
  Provider surfaces the error verbatim; consumers will see the actual
  permission validation message from upstream.

  Implements [[tasks/provider-listmonk]] v0.2


See full diff: [v0.1.0...v0.2.0](https://github.com/hops-ops/terraform-provider-listmonk/compare/v0.1.0...v0.2.0)
