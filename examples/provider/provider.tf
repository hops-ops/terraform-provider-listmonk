terraform {
  required_providers {
    listmonk = {
      source  = "hops-ops/listmonk"
      version = "~> 0.1"
    }
  }
}

# Authenticate as an `api`-typed Listmonk user. For real deployments the
# `crossplane-provider` user is minted by EmailMarketingStack's
# bootstrap Job; for local development you can mint one in the Listmonk
# admin UI (Users > New > Type=API) — the user's "password" IS the
# token.
provider "listmonk" {
  endpoint = "http://localhost:9000"
  username = "crossplane-provider"
  token    = var.listmonk_token
}

variable "listmonk_token" {
  type      = string
  sensitive = true
}
