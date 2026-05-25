// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hops-ops/terraform-provider-listmonk/internal/client"
)

var (
	_ resource.Resource                = &SecuritySettingsResource{}
	_ resource.ResourceWithImportState = &SecuritySettingsResource{}
)

// settings keys this resource owns. Each one maps to a row in the
// Listmonk `settings` table and is reconciled via `PUT
// /api/settings/<key>` with the typed JSON below.
const (
	keySecurityOIDC = "security.oidc"
)

// idSentinel is the synthetic ID stored in Terraform state for this
// resource. The Listmonk settings table doesn't surface a stable opaque
// ID — there's exactly one "security" section per instance — so the
// resource is effectively a singleton.
const idSentinel = "security"

func NewSecuritySettingsResource() resource.Resource {
	return &SecuritySettingsResource{}
}

// SecuritySettingsResource manages the `security.*` rows of Listmonk's
// `settings` table. v0.1 covers `security.oidc`; `security.captcha` and
// `security.basic_auth` are reserved (declared but not implemented).
//
// Behavioral guarantee per the design: only sub-blocks declared in the
// consumer's HCL are reconciled. If `oidc` is configured but the captcha
// reserved block is not, operator UI edits to CAPTCHA settings are
// preserved on every reconcile.
type SecuritySettingsResource struct {
	client *client.Client
}

// SecuritySettingsResourceModel is the Terraform state / plan shape.
type SecuritySettingsResourceModel struct {
	ID   types.String       `tfsdk:"id"`
	OIDC *OIDCSettingsModel `tfsdk:"oidc"`
}

// OIDCSettingsModel mirrors the `security.oidc` JSONB value in
// knadh/listmonk's `Settings` Go struct (cmd/settings.go). Nullable
// `default_*_role_id` use Int64 — null means "not managed".
type OIDCSettingsModel struct {
	Enabled           types.Bool   `tfsdk:"enabled"`
	ProviderURL       types.String `tfsdk:"provider_url"`
	ProviderName      types.String `tfsdk:"provider_name"`
	ClientID          types.String `tfsdk:"client_id"`
	ClientSecret      types.String `tfsdk:"client_secret"`
	AutoCreateUsers   types.Bool   `tfsdk:"auto_create_users"`
	DefaultUserRoleID types.Int64  `tfsdk:"default_user_role_id"`
	DefaultListRoleID types.Int64  `tfsdk:"default_list_role_id"`
}

// oidcWire is the JSON shape Listmonk persists under the
// `security.oidc` settings key. Field ordering / json tags exactly
// mirror the upstream struct so the round-trip is lossless.
type oidcWire struct {
	Enabled           bool   `json:"enabled"`
	ProviderURL       string `json:"provider_url"`
	ProviderName      string `json:"provider_name"`
	ClientID          string `json:"client_id"`
	ClientSecret      string `json:"client_secret"`
	AutoCreateUsers   bool   `json:"auto_create_users"`
	DefaultUserRoleID *int64 `json:"default_user_role_id"`
	DefaultListRoleID *int64 `json:"default_list_role_id"`
}

func (r *SecuritySettingsResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_security_settings"
}

func (r *SecuritySettingsResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Listmonk Security settings — the `security.*` rows of the `settings` table. Singleton resource per Listmonk instance (one `security` section per server). v0.1 manages the `security.oidc` sub-block; future versions will add `security.captcha` and `security.basic_auth`.\n\n**Drift behavior**: only sub-blocks declared in this resource are reconciled. Operator UI edits to undeclared sub-blocks (e.g. CAPTCHA) are preserved. Operator UI edits to *managed* sub-blocks are reverted on the next `terraform apply`.\n\n**Client secret**: Listmonk masks `oidc.client_secret` with bullet characters on `GET /api/settings`, so the provider keeps the value Terraform last wrote authoritative in state — drift on `client_secret` cannot be detected and will only re-converge on the next apply. Rotate by changing the HCL value and applying.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Synthetic ID for the singleton settings section. Always `security`.",
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
		Blocks: map[string]schema.Block{
			"oidc": schema.SingleNestedBlock{
				MarkdownDescription: "OpenID Connect SSO configuration (the `security.oidc` row). When this block is omitted the provider does not touch the server-side OIDC settings.",
				Attributes: map[string]schema.Attribute{
					"enabled": schema.BoolAttribute{
						MarkdownDescription: "Enable OIDC sign-in.",
						Required:            true,
					},
					"provider_url": schema.StringAttribute{
						MarkdownDescription: "OIDC issuer base URL (e.g. `https://auth.example.com`). Listmonk fetches `<provider_url>/.well-known/openid-configuration` to discover endpoints.",
						Optional:            true,
					},
					"provider_name": schema.StringAttribute{
						MarkdownDescription: "Display name shown on the sign-in page (e.g. `Zitadel`).",
						Optional:            true,
					},
					"client_id": schema.StringAttribute{
						MarkdownDescription: "OIDC application client ID.",
						Optional:            true,
					},
					"client_secret": schema.StringAttribute{
						MarkdownDescription: "OIDC application client secret. Write-only — Listmonk masks the value on read.",
						Optional:            true,
						Sensitive:           true,
					},
					"auto_create_users": schema.BoolAttribute{
						MarkdownDescription: "When true, sign-in by an unknown OIDC subject creates a Listmonk user assigned the role identified by `default_user_role_id`. Listmonk's OIDC subject-to-role mapping does not consume `groups`/`roles` claims — see the provider README for the explicit-allowlist alternative.",
						Optional:            true,
					},
					"default_user_role_id": schema.Int64Attribute{
						MarkdownDescription: "User role ID assigned to auto-created users (typically a `listmonk_user_role` ID). Leave null to leave Listmonk's default in place.",
						Optional:            true,
					},
					"default_list_role_id": schema.Int64Attribute{
						MarkdownDescription: "List role ID assigned to auto-created users. Leave null to leave Listmonk's default in place.",
						Optional:            true,
					},
				},
			},
		},
	}
}

func (r *SecuritySettingsResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data type",
			fmt.Sprintf("Expected *client.Client, got %T", req.ProviderData))
		return
	}
	r.client = c
}

func (r *SecuritySettingsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan SecuritySettingsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.reconcile(ctx, plan); err != nil {
		resp.Diagnostics.AddError("Listmonk PUT /api/settings/security.oidc failed", err.Error())
		return
	}

	plan.ID = types.StringValue(idSentinel)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *SecuritySettingsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state SecuritySettingsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// If state has no ID yet (upjet's pre-Create observe) OR no
	// declared sub-blocks, the resource doesn't exist yet — tell
	// Terraform via RemoveResource so upjet falls through to Create.
	if state.ID.IsNull() || state.ID.IsUnknown() || state.ID.ValueString() == "" {
		resp.State.RemoveResource(ctx)
		return
	}
	if state.OIDC == nil {
		// Nothing this resource manages — leave state alone.
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		return
	}

	bag, err := r.client.GetSettings(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Listmonk GET /api/settings failed", err.Error())
		return
	}

	raw, ok := bag[keySecurityOIDC]
	if !ok {
		// Row missing — shouldn't happen on a normal install, but if it
		// does, drop from state so the next apply re-creates.
		resp.State.RemoveResource(ctx)
		return
	}

	var wire oidcWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		resp.Diagnostics.AddError("decode security.oidc from /api/settings", err.Error())
		return
	}

	// Listmonk masks `client_secret` with U+2022 (bullet) on read. We
	// preserve the value Terraform last wrote so drift detection on
	// non-secret fields keeps working and the secret round-trips cleanly.
	preservedSecret := state.OIDC.ClientSecret
	state.OIDC = oidcWireToModel(wire, preservedSecret)
	state.ID = types.StringValue(idSentinel)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *SecuritySettingsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan SecuritySettingsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.reconcile(ctx, plan); err != nil {
		resp.Diagnostics.AddError("Listmonk PUT /api/settings/security.oidc failed", err.Error())
		return
	}

	plan.ID = types.StringValue(idSentinel)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *SecuritySettingsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	// The `security.oidc` row is part of Listmonk's seeded schema — it
	// always exists. Deleting the resource releases TF management
	// without mutating the server. To fully disable OIDC, set
	// `oidc.enabled = false` and apply before destroy.
}

func (r *SecuritySettingsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// reconcile PUTs every declared sub-block to its dotted-key endpoint.
// Sub-blocks left null are not touched on the server.
func (r *SecuritySettingsResource) reconcile(ctx context.Context, plan SecuritySettingsResourceModel) error {
	if plan.OIDC != nil {
		wire := oidcModelToWire(plan.OIDC)
		buf, err := json.Marshal(wire)
		if err != nil {
			return fmt.Errorf("marshal security.oidc: %w", err)
		}
		if err := r.client.UpdateSettingByKey(ctx, keySecurityOIDC, buf); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------
// Wire <-> Model translation
// ---------------------------------------------------------------------

func oidcModelToWire(m *OIDCSettingsModel) oidcWire {
	w := oidcWire{
		Enabled:         m.Enabled.ValueBool(),
		ProviderURL:     m.ProviderURL.ValueString(),
		ProviderName:    m.ProviderName.ValueString(),
		ClientID:        m.ClientID.ValueString(),
		ClientSecret:    m.ClientSecret.ValueString(),
		AutoCreateUsers: m.AutoCreateUsers.ValueBool(),
	}
	if !m.DefaultUserRoleID.IsNull() && !m.DefaultUserRoleID.IsUnknown() {
		v := m.DefaultUserRoleID.ValueInt64()
		w.DefaultUserRoleID = &v
	}
	if !m.DefaultListRoleID.IsNull() && !m.DefaultListRoleID.IsUnknown() {
		v := m.DefaultListRoleID.ValueInt64()
		w.DefaultListRoleID = &v
	}
	return w
}

func oidcWireToModel(w oidcWire, preservedSecret types.String) *OIDCSettingsModel {
	m := &OIDCSettingsModel{
		Enabled:         types.BoolValue(w.Enabled),
		ProviderURL:     types.StringValue(w.ProviderURL),
		ProviderName:    types.StringValue(w.ProviderName),
		ClientID:        types.StringValue(w.ClientID),
		ClientSecret:    preservedSecret,
		AutoCreateUsers: types.BoolValue(w.AutoCreateUsers),
	}
	// If the server returned the masked bullet pattern *and* we have
	// no prior state (eg. import), populate state with an empty string
	// rather than the bullet mask — that way the next apply detects
	// "configured value differs from state" and re-pushes. Operators
	// must re-set client_secret in HCL after import.
	if preservedSecret.IsNull() || preservedSecret.IsUnknown() {
		if isMaskedSecret(w.ClientSecret) {
			m.ClientSecret = types.StringValue("")
		} else {
			m.ClientSecret = types.StringValue(w.ClientSecret)
		}
	}
	if w.DefaultUserRoleID != nil {
		m.DefaultUserRoleID = types.Int64Value(*w.DefaultUserRoleID)
	} else {
		m.DefaultUserRoleID = types.Int64Null()
	}
	if w.DefaultListRoleID != nil {
		m.DefaultListRoleID = types.Int64Value(*w.DefaultListRoleID)
	} else {
		m.DefaultListRoleID = types.Int64Null()
	}
	return m
}

// isMaskedSecret reports whether s is the all-bullets mask Listmonk's
// settings handler emits in place of a real secret value.
func isMaskedSecret(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r != '•' {
			return false
		}
	}
	return true
}
