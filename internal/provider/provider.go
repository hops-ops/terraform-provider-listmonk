// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hops-ops/terraform-provider-listmonk/internal/client"
)

// Ensure ListmonkProvider satisfies the provider interface.
var _ provider.Provider = &ListmonkProvider{}

// ListmonkProvider is the Listmonk Terraform provider. It wraps Listmonk's
// REST API (see https://listmonk.app/docs/apis/apis/) to expose the
// per-section settings rows, user roles, API users, lists, list roles
// and templates as Terraform resources.
//
// Auth is HTTP Basic-Auth against an `api`-typed Listmonk user — the
// user's password IS the token. A `crossplane-provider` machine user
// minted by EmailMarketingStack's bootstrap Job is the intended
// principal; any sufficiently privileged user works for local
// development.
type ListmonkProvider struct {
	version string
}

// ListmonkProviderModel describes the provider HCL block.
type ListmonkProviderModel struct {
	Endpoint types.String `tfsdk:"endpoint"`
	Username types.String `tfsdk:"username"`
	Token    types.String `tfsdk:"token"`
}

func (p *ListmonkProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "listmonk"
	resp.Version = p.version
}

func (p *ListmonkProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Terraform provider for Listmonk (https://listmonk.app). Manages per-section settings rows (e.g. `security.oidc`), user roles, API users, lists, list roles, and templates via the Listmonk REST API.",
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				MarkdownDescription: "Base URL of the Listmonk API (e.g. `http://marketing-listmonk.marketing.svc.cluster.local:9000`). The provider appends `/api/...` to each request. Falls back to the `LISTMONK_ENDPOINT` environment variable.",
				Optional:            true,
			},
			"username": schema.StringAttribute{
				MarkdownDescription: "Username of the Listmonk API user (an `api`-typed User, ideally `crossplane-provider`). Falls back to `LISTMONK_USERNAME`.",
				Optional:            true,
			},
			"token": schema.StringAttribute{
				MarkdownDescription: "Password / token of the Listmonk API user. For `api`-typed users this is the value returned at User creation. Falls back to `LISTMONK_TOKEN`.",
				Optional:            true,
				Sensitive:           true,
			},
		},
	}
}

func (p *ListmonkProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data ListmonkProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := stringOrEnv(data.Endpoint, "LISTMONK_ENDPOINT")
	username := stringOrEnv(data.Username, "LISTMONK_USERNAME")
	token := stringOrEnv(data.Token, "LISTMONK_TOKEN")

	if endpoint == "" {
		resp.Diagnostics.AddError("Listmonk endpoint is required",
			"Set `endpoint` in the provider block or the LISTMONK_ENDPOINT environment variable.")
		return
	}
	if username == "" || token == "" {
		resp.Diagnostics.AddError("Listmonk credentials are required",
			fmt.Sprintf("Set both `username` + `token` in the provider block (or LISTMONK_USERNAME + LISTMONK_TOKEN). Got username=%q token-set=%t", username, token != ""))
		return
	}

	c := client.New(endpoint, &client.BasicAuth{Username: username, Token: token}, p.version)
	resp.ResourceData = c
	resp.DataSourceData = c
}

func (p *ListmonkProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewSecuritySettingsResource,
		NewAppSettingsResource,
		NewUserRoleResource,
		NewUserResource,
	}
}

func (p *ListmonkProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{}
}

func (p *ListmonkProvider) Functions(ctx context.Context) []func() function.Function {
	return []func() function.Function{}
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &ListmonkProvider{
			version: version,
		}
	}
}

// stringOrEnv returns the configured value when known, else the env
// fallback, else "".
func stringOrEnv(v types.String, envKey string) string {
	if !v.IsNull() && !v.IsUnknown() {
		return v.ValueString()
	}
	return os.Getenv(envKey)
}
