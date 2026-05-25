### What's changed in v0.2.3

* fix(read): apply RemoveResource-on-empty-state.ID to security_settings + app_settings (by @patrickleet)

  Same upjet pre-Create observe bug as user_role / user — for
  singleton-IDd settings resources, the early-return on missing
  declared sub-blocks left tfstate with zero-valued id, and upjet's
  controller errored 'cannot find id in tfstate'.

  Now matches the contract documented inline in user_role_resource.go
  and user_resource.go.


See full diff: [v0.2.2...v0.2.3](https://github.com/hops-ops/terraform-provider-listmonk/compare/v0.2.2...v0.2.3)
