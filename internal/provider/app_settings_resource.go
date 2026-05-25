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
	_ resource.Resource                = &AppSettingsResource{}
	_ resource.ResourceWithImportState = &AppSettingsResource{}
)

const idAppSettings = "app"

// app_settings keys this resource owns. Each maps to its OWN row in the
// settings table (unlike `security.oidc` which is a single row holding
// a struct, `app.*` fields are each their own scalar-valued row).
const (
	keyAppRootURL     = "app.root_url"
	keyAppFromEmail   = "app.from_email"
	keyAppSiteName    = "app.site_name"
	keyAppLogoURL     = "app.logo_url"
	keyAppFaviconURL  = "app.favicon_url"
	keyAppLang        = "app.lang"
	keyAppConcurrency = "app.concurrency"
	keyAppMessageRate = "app.message_rate"
)

func NewAppSettingsResource() resource.Resource {
	return &AppSettingsResource{}
}

// AppSettingsResource manages the `app.*` rows of Listmonk's settings
// table. Unlike `security.oidc` (a single row holding a JSON struct),
// each `app.*` setting is its OWN row with a scalar JSON value. The
// resource reconciles only the fields the consumer declares — fields
// left null in HCL are not touched on the server, so operator UI edits
// to unmanaged fields are preserved across `terraform apply`.
//
// Singleton per Listmonk instance.
type AppSettingsResource struct {
	client *client.Client
}

type AppSettingsResourceModel struct {
	ID          types.String `tfsdk:"id"`
	RootURL     types.String `tfsdk:"root_url"`
	FromEmail   types.String `tfsdk:"from_email"`
	SiteName    types.String `tfsdk:"site_name"`
	LogoURL     types.String `tfsdk:"logo_url"`
	FaviconURL  types.String `tfsdk:"favicon_url"`
	Lang        types.String `tfsdk:"lang"`
	Concurrency types.Int64  `tfsdk:"concurrency"`
	MessageRate types.Int64  `tfsdk:"message_rate"`
}

func (r *AppSettingsResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_app_settings"
}

func (r *AppSettingsResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Listmonk app/general settings — the `app.*` rows of the settings table. Singleton per Listmonk instance. Each field is independently managed: a field declared in HCL becomes the source of truth; a field left null is not touched on the server, so operator UI edits to unmanaged fields are preserved.\n\nv0.1 covers the most common knobs (root URL, from-email, site name, branding, lang, concurrency, message rate). Less-common fields (`app.batch_size`, `app.max_send_errors`, etc.) can be added in later versions of the provider.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Synthetic ID for the singleton app settings group. Always `app`.",
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"root_url": schema.StringAttribute{
				MarkdownDescription: "Public root URL Listmonk uses when constructing absolute links (subscription confirm, click-tracking, OIDC redirect URI). E.g. `https://marketing.example.com`. Maps to `app.root_url`.",
				Optional:            true,
			},
			"from_email": schema.StringAttribute{
				MarkdownDescription: "Default From: address used when sending campaign mail. Maps to `app.from_email`.",
				Optional:            true,
			},
			"site_name": schema.StringAttribute{
				MarkdownDescription: "Brand name shown in the admin UI + subscriber-facing pages. Maps to `app.site_name`.",
				Optional:            true,
			},
			"logo_url": schema.StringAttribute{
				MarkdownDescription: "Public URL to a logo image used in the admin UI + subscriber pages. Maps to `app.logo_url`.",
				Optional:            true,
			},
			"favicon_url": schema.StringAttribute{
				MarkdownDescription: "Public URL to a favicon used in the admin UI + subscriber pages. Maps to `app.favicon_url`.",
				Optional:            true,
			},
			"lang": schema.StringAttribute{
				MarkdownDescription: "Default UI language code (e.g. `en`, `de`, `fr`). Maps to `app.lang`.",
				Optional:            true,
			},
			"concurrency": schema.Int64Attribute{
				MarkdownDescription: "Number of parallel worker goroutines used for campaign sending. Maps to `app.concurrency`.",
				Optional:            true,
			},
			"message_rate": schema.Int64Attribute{
				MarkdownDescription: "Max messages per second per concurrency worker. Maps to `app.message_rate`.",
				Optional:            true,
			},
		},
	}
}

func (r *AppSettingsResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *AppSettingsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan AppSettingsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.reconcile(ctx, plan); err != nil {
		resp.Diagnostics.AddError("Listmonk app settings PUT failed", err.Error())
		return
	}
	plan.ID = types.StringValue(idAppSettings)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *AppSettingsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state AppSettingsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	bag, err := r.client.GetSettings(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Listmonk GET /api/settings failed", err.Error())
		return
	}

	// For each field DECLARED in state (non-null), refresh from server.
	// Fields not in state stay null so unmanaged keys don't suddenly
	// appear in state on next refresh.
	if !state.RootURL.IsNull() {
		state.RootURL = readSettingString(bag, keyAppRootURL, state.RootURL)
	}
	if !state.FromEmail.IsNull() {
		state.FromEmail = readSettingString(bag, keyAppFromEmail, state.FromEmail)
	}
	if !state.SiteName.IsNull() {
		state.SiteName = readSettingString(bag, keyAppSiteName, state.SiteName)
	}
	if !state.LogoURL.IsNull() {
		state.LogoURL = readSettingString(bag, keyAppLogoURL, state.LogoURL)
	}
	if !state.FaviconURL.IsNull() {
		state.FaviconURL = readSettingString(bag, keyAppFaviconURL, state.FaviconURL)
	}
	if !state.Lang.IsNull() {
		state.Lang = readSettingString(bag, keyAppLang, state.Lang)
	}
	if !state.Concurrency.IsNull() {
		state.Concurrency = readSettingInt64(bag, keyAppConcurrency, state.Concurrency)
	}
	if !state.MessageRate.IsNull() {
		state.MessageRate = readSettingInt64(bag, keyAppMessageRate, state.MessageRate)
	}
	state.ID = types.StringValue(idAppSettings)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *AppSettingsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan AppSettingsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.reconcile(ctx, plan); err != nil {
		resp.Diagnostics.AddError("Listmonk app settings PUT failed", err.Error())
		return
	}
	plan.ID = types.StringValue(idAppSettings)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *AppSettingsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	// Settings rows are migration-seeded — they always exist. Drop from
	// TF state without mutating the server, matching the
	// SecuritySettingsResource contract.
}

func (r *AppSettingsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// reconcile PUTs every declared field to its dotted-key endpoint.
// Fields left null are not touched on the server.
func (r *AppSettingsResource) reconcile(ctx context.Context, plan AppSettingsResourceModel) error {
	if err := putIfSetString(ctx, r.client, keyAppRootURL, plan.RootURL); err != nil {
		return err
	}
	if err := putIfSetString(ctx, r.client, keyAppFromEmail, plan.FromEmail); err != nil {
		return err
	}
	if err := putIfSetString(ctx, r.client, keyAppSiteName, plan.SiteName); err != nil {
		return err
	}
	if err := putIfSetString(ctx, r.client, keyAppLogoURL, plan.LogoURL); err != nil {
		return err
	}
	if err := putIfSetString(ctx, r.client, keyAppFaviconURL, plan.FaviconURL); err != nil {
		return err
	}
	if err := putIfSetString(ctx, r.client, keyAppLang, plan.Lang); err != nil {
		return err
	}
	if err := putIfSetInt64(ctx, r.client, keyAppConcurrency, plan.Concurrency); err != nil {
		return err
	}
	if err := putIfSetInt64(ctx, r.client, keyAppMessageRate, plan.MessageRate); err != nil {
		return err
	}
	return nil
}

func putIfSetString(ctx context.Context, c *client.Client, key string, v types.String) error {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	buf, err := json.Marshal(v.ValueString())
	if err != nil {
		return fmt.Errorf("marshal %s: %w", key, err)
	}
	return c.UpdateSettingByKey(ctx, key, buf)
}

func putIfSetInt64(ctx context.Context, c *client.Client, key string, v types.Int64) error {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	buf, err := json.Marshal(v.ValueInt64())
	if err != nil {
		return fmt.Errorf("marshal %s: %w", key, err)
	}
	return c.UpdateSettingByKey(ctx, key, buf)
}

func readSettingString(bag client.SettingsBag, key string, fallback types.String) types.String {
	raw, ok := bag[key]
	if !ok {
		return fallback
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return fallback
	}
	return types.StringValue(s)
}

func readSettingInt64(bag client.SettingsBag, key string, fallback types.Int64) types.Int64 {
	raw, ok := bag[key]
	if !ok {
		return fallback
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err != nil {
		return fallback
	}
	return types.Int64Value(n)
}
