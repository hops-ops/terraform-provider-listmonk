#!/usr/bin/env bash
# The Listmonk Security settings section is a singleton — there is
# exactly one `security.*` group of rows per Listmonk instance. Import
# uses the sentinel ID `security`.
#
# IMPORTANT: Listmonk masks `oidc.client_secret` on read with bullet
# characters, so on import the client_secret in Terraform state is
# empty. Re-set it in your HCL and apply to push the real value.
terraform import listmonk_security_settings.main security
