# Configures the `security.oidc` row of Listmonk's settings table. Each
# sub-block (oidc, captcha, basic_auth) is independently managed; this
# resource only reconciles the sub-blocks declared here, so an operator
# editing e.g. CAPTCHA settings in the Listmonk UI will not be overridden.
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
