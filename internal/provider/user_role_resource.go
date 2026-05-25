// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hops-ops/terraform-provider-listmonk/internal/client"
)

var (
	_ resource.Resource                = &UserRoleResource{}
	_ resource.ResourceWithImportState = &UserRoleResource{}
)

func NewUserRoleResource() resource.Resource {
	return &UserRoleResource{}
}

// UserRoleResource manages a Listmonk "user"-typed Role. Permissions
// are dotted-string capabilities (e.g. `settings:manage`, `users:get`,
// `lists:manage_all`); see Listmonk's `internal/auth/auth.go::ParseRole`
// for the canonical list per app version.
type UserRoleResource struct {
	client *client.Client
}

type UserRoleResourceModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Permissions types.Set    `tfsdk:"permissions"`
	CreatedAt   types.String `tfsdk:"created_at"`
	UpdatedAt   types.String `tfsdk:"updated_at"`
}

func (r *UserRoleResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user_role"
}

func (r *UserRoleResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A Listmonk User Role — a named bundle of dotted-string permissions (`settings:manage`, `users:get`, `lists:manage_all`, etc.). Assigned to Users via the `user_role_id` field. The `Super Admin` role (id=1) is seeded by Listmonk's install migration and should not be managed by Terraform.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Numeric role ID assigned by Listmonk (stringified for TF state). Stable for the lifetime of the role.",
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Human-readable role name. Shown in the Listmonk admin UI's role picker.",
				Required:            true,
			},
			"permissions": schema.SetAttribute{
				MarkdownDescription: "Set of dotted-string permissions granted by this role. See Listmonk's source for the canonical list (e.g. `settings:get`, `settings:manage`, `users:get`, `users:manage`, `roles:get`, `roles:manage`, `lists:get_all`, `lists:manage_all`, `templates:get`, `templates:manage`, `campaigns:get`, `campaigns:manage`, `subscribers:manage`, `bounces:get`, `bounces:manage`, `media:get`, `media:manage`, `tx:send`).",
				Required:            true,
				ElementType:         types.StringType,
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

func (r *UserRoleResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *UserRoleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan UserRoleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	in := &client.UserRole{
		Name:        plan.Name.ValueString(),
		Permissions: setToStrings(ctx, plan.Permissions),
	}
	out, err := r.client.CreateUserRole(ctx, in)
	if err != nil {
		resp.Diagnostics.AddError("Listmonk CreateUserRole failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, userRoleToModel(out))...)
}

func (r *UserRoleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state UserRoleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// upjet calls Read BEFORE Create to observe whether the resource
	// already exists. For IdentifierFromProvider-style external-name
	// configs the ID is server-assigned at Create time, so on the
	// first observe the state.ID is empty. Tell Terraform the resource
	// doesn't exist (RemoveResource) so upjet's controller knows to
	// fall through to Create — bare `return` leaves an inconsistent
	// state shape and upjet errors with "cannot find id in tfstate".
	if state.ID.IsNull() || state.ID.IsUnknown() || state.ID.ValueString() == "" {
		resp.State.RemoveResource(ctx)
		return
	}

	id, err := strconv.ParseInt(state.ID.ValueString(), 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid user_role id in state", err.Error())
		return
	}
	out, err := r.client.GetUserRole(ctx, id)
	if err != nil {
		if client.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Listmonk GetUserRole failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, userRoleToModel(out))...)
}

func (r *UserRoleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan UserRoleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id, err := strconv.ParseInt(plan.ID.ValueString(), 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid user_role id in plan", err.Error())
		return
	}
	in := &client.UserRole{
		Name:        plan.Name.ValueString(),
		Permissions: setToStrings(ctx, plan.Permissions),
	}
	out, err := r.client.UpdateUserRole(ctx, id, in)
	if err != nil {
		resp.Diagnostics.AddError("Listmonk UpdateUserRole failed", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, userRoleToModel(out))...)
}

func (r *UserRoleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state UserRoleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := strconv.ParseInt(state.ID.ValueString(), 10, 64)
	if err != nil {
		resp.Diagnostics.AddError("Invalid user_role id in state", err.Error())
		return
	}
	if err := r.client.DeleteUserRole(ctx, id); err != nil {
		if client.IsNotFound(err) {
			return
		}
		resp.Diagnostics.AddError("Listmonk DeleteUserRole failed", err.Error())
	}
}

func (r *UserRoleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func userRoleToModel(r *client.UserRole) *UserRoleResourceModel {
	return &UserRoleResourceModel{
		ID:          types.StringValue(strconv.FormatInt(r.ID, 10)),
		Name:        types.StringValue(r.Name),
		Permissions: stringsToSet(r.Permissions),
		CreatedAt:   types.StringValue(r.CreatedAt),
		UpdatedAt:   types.StringValue(r.UpdatedAt),
	}
}

func setToStrings(ctx context.Context, s types.Set) []string {
	if s.IsNull() || s.IsUnknown() {
		return nil
	}
	var out []string
	_ = s.ElementsAs(ctx, &out, false)
	return out
}

func stringsToSet(ss []string) types.Set {
	if ss == nil {
		ss = []string{}
	}
	vals := make([]attr.Value, len(ss))
	for i, s := range ss {
		vals[i] = types.StringValue(s)
	}
	v, _ := types.SetValue(types.StringType, vals)
	return v
}
