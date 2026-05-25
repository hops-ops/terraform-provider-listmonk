// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hops-ops/terraform-provider-listmonk/internal/client"
)

var (
	_ resource.Resource                = &UserResource{}
	_ resource.ResourceWithImportState = &UserResource{}
)

func NewUserResource() resource.Resource {
	return &UserResource{}
}

// UserResource manages a Listmonk User row. Two flavors via `type`:
//
//   - `type = "user"`: interactive admin. `password_login` is typically
//     true; `password` is the operator-chosen value (sent plaintext on
//     Create/Update; Listmonk stores it bcrypt-hashed; never returned
//     on Read). Drift detection on `password` is impossible — treat it
//     as write-only and rotate by changing the HCL value.
//
//   - `type = "api"`: machine credential. `password_login` is false;
//     `password` IS the plaintext API token that the user uses for
//     HTTP Basic-Auth against /api/. Listmonk's apiUsers cache is
//     loaded ONLY at pod startup, so a newly-created api user is not
//     immediately usable for Basic-Auth — the consumer must wait for /
//     trigger a Listmonk pod restart. See
//     reference_listmonk_apiusers_cache_at_startup.
type UserResource struct {
	client *client.Client
}

type UserResourceModel struct {
	ID            types.String `tfsdk:"id"`
	Username      types.String `tfsdk:"username"`
	Email         types.String `tfsdk:"email"`
	Name          types.String `tfsdk:"name"`
	Type          types.String `tfsdk:"type"`
	UserRoleID    types.Int64  `tfsdk:"user_role_id"`
	ListRoleID    types.Int64  `tfsdk:"list_role_id"`
	Status        types.String `tfsdk:"status"`
	PasswordLogin types.Bool   `tfsdk:"password_login"`
	Password      types.String `tfsdk:"password"`
	CreatedAt     types.String `tfsdk:"created_at"`
	UpdatedAt     types.String `tfsdk:"updated_at"`
}

func (r *UserResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user"
}

func (r *UserResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A Listmonk User. Two types: `user` (interactive admin) and `api` (machine credential). For `api` users the password IS the plaintext token used for HTTP Basic-Auth — Listmonk doesn't have a separate token-mint endpoint. After creating a new `api` user, the Listmonk pod must be restarted before the credential is recognized (the apiUsers cache is loaded only at startup).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Numeric user ID assigned by Listmonk (stringified for TF state).",
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"username": schema.StringAttribute{
				MarkdownDescription: "Unique username. Becomes the principal name for Basic-Auth on `api` users; the display name in the Listmonk admin UI for `user` users.",
				Required:            true,
			},
			"email": schema.StringAttribute{
				MarkdownDescription: "Unique email address. For `api` users this doesn't need to be deliverable but must still be unique.",
				Required:            true,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Display name shown in the Listmonk admin UI.",
				Required:            true,
			},
			"type": schema.StringAttribute{
				MarkdownDescription: "User type: `user` (interactive) or `api` (machine credential whose `password` is the plaintext Basic-Auth token).",
				Required:            true,
			},
			"user_role_id": schema.Int64Attribute{
				MarkdownDescription: "ID of the user role (`listmonk_user_role`) that defines this user's permissions. Use `1` for the Listmonk-seeded `Super Admin` role.",
				Required:            true,
			},
			"list_role_id": schema.Int64Attribute{
				MarkdownDescription: "Optional ID of a list role (`listmonk_list_role`) that defines this user's per-list access. Null leaves it unset.",
				Optional:            true,
			},
			"status": schema.StringAttribute{
				MarkdownDescription: "Lifecycle status: `enabled` or `disabled`. Disabled users can't authenticate but their row is preserved.",
				Required:            true,
			},
			"password_login": schema.BoolAttribute{
				MarkdownDescription: "Whether this user can sign in via the Listmonk admin UI form. For `api` users set false. For `user` users typically true.",
				Required:            true,
			},
			"password": schema.StringAttribute{
				MarkdownDescription: "For `type = api`: the plaintext token used for HTTP Basic-Auth. For `type = user`: the operator-chosen password (Listmonk stores it bcrypt-hashed; never returned on Read so drift on it is undetectable — rotate by changing the HCL value and applying).",
				Optional:            true,
				Sensitive:           true,
			},
			"created_at": schema.StringAttribute{
				MarkdownDescription: "RFC 3339 timestamp.",
				Computed:            true,
			},
			"updated_at": schema.StringAttribute{
				MarkdownDescription: "RFC 3339 timestamp.",
				Computed:            true,
			},
		},
	}
}

func (r *UserResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *UserResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan UserResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	in := planToUser(plan)
	out, err := r.client.CreateUser(ctx, in)
	if err != nil {
		resp.Diagnostics.AddError("Listmonk CreateUser failed", err.Error())
		return
	}
	// Preserve plan's password in state — for type=user Listmonk
	// returns the bcrypt hash on subsequent reads (drift-undetectable);
	// for type=api Listmonk returns the plaintext on Create but might
	// also send a confusing value on later reads. Keep plan-of-record.
	resp.Diagnostics.Append(resp.State.Set(ctx, userToModel(out, plan.Password))...)
}

func (r *UserResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state UserResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := strconv.ParseInt(state.ID.ValueString(), 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid user id in state", err.Error())
		return
	}
	out, err := r.client.GetUser(ctx, id)
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Listmonk GetUser failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, userToModel(out, state.Password))...)
}

func (r *UserResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan UserResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := strconv.ParseInt(plan.ID.ValueString(), 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid user id in plan", err.Error())
		return
	}
	in := planToUser(plan)
	out, err := r.client.UpdateUser(ctx, id, in)
	if err != nil {
		resp.Diagnostics.AddError("Listmonk UpdateUser failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, userToModel(out, plan.Password))...)
}

func (r *UserResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state UserResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := strconv.ParseInt(state.ID.ValueString(), 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid user id in state", err.Error())
		return
	}
	if err := r.client.DeleteUser(ctx, id); err != nil {
		if client.IsNotFound(err) {
			return
		}
		resp.Diagnostics.AddError("Listmonk DeleteUser failed", err.Error())
	}
}

func (r *UserResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func planToUser(p UserResourceModel) *client.User {
	u := &client.User{
		Username:      p.Username.ValueString(),
		Email:         p.Email.ValueString(),
		Name:          p.Name.ValueString(),
		Type:          client.UserType(p.Type.ValueString()),
		UserRoleID:    p.UserRoleID.ValueInt64(),
		Status:        client.UserStatus(p.Status.ValueString()),
		PasswordLogin: p.PasswordLogin.ValueBool(),
		Password:      p.Password.ValueString(),
	}
	if !p.ListRoleID.IsNull() && !p.ListRoleID.IsUnknown() {
		v := p.ListRoleID.ValueInt64()
		u.ListRoleID = &v
	}
	return u
}

// userToModel converts a wire User into resource state. The password
// arg is the value we want to KEEP in state (typically the plan or
// prior-state value) — Listmonk doesn't return a usable plaintext
// password on read for type=user, and the bullet-mask / bcrypt
// shenanigans aren't useful to surface. For type=api: at Create time
// the server echoes back the plaintext we just sent, but Read returns
// the bcrypt-ish stored value; preserving the prior state keeps the
// resource stable.
func userToModel(u *client.User, preservedPassword types.String) *UserResourceModel {
	m := &UserResourceModel{
		ID:            types.StringValue(strconv.FormatInt(u.ID, 10)),
		Username:      types.StringValue(u.Username),
		Email:         types.StringValue(u.Email),
		Name:          types.StringValue(u.Name),
		Type:          types.StringValue(string(u.Type)),
		UserRoleID:    types.Int64Value(u.UserRoleID),
		Status:        types.StringValue(string(u.Status)),
		PasswordLogin: types.BoolValue(u.PasswordLogin),
		Password:      preservedPassword,
		CreatedAt:     types.StringValue(u.CreatedAt),
		UpdatedAt:     types.StringValue(u.UpdatedAt),
	}
	if u.ListRoleID != nil {
		m.ListRoleID = types.Int64Value(*u.ListRoleID)
	} else {
		m.ListRoleID = types.Int64Null()
	}
	return m
}
