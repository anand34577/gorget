package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type userResource struct{ c *apiClient }

func newUser() resource.Resource { return &userResource{} }

type userModel struct {
	ID                types.String `tfsdk:"id"`
	Email             types.String `tfsdk:"email"`
	Name              types.String `tfsdk:"name"`
	Role              types.String `tfsdk:"role"`
	Disabled          types.Bool   `tfsdk:"disabled"`
	TemporaryPassword types.String `tfsdk:"temporary_password"`
}

func (r *userResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user"
}

func (r *userResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A person who can sign in. Created with a temporary password they must change (users that sign in with single sign-on never use it).",
		Attributes: map[string]schema.Attribute{
			"id":                 schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"email":              schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"name":               schema.StringAttribute{Optional: true, Computed: true},
			"role":               schema.StringAttribute{Optional: true, Computed: true, Description: "owner, admin, network_admin, auditor or user (default)."},
			"disabled":           schema.BoolAttribute{Optional: true, Computed: true},
			"temporary_password": schema.StringAttribute{Computed: true, Sensitive: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		},
	}
}

func (r *userResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.c = clientFrom(req.ProviderData, &resp.Diagnostics)
}

type userAPI struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	Disabled bool   `json:"disabled"`
}

func (m *userModel) fill(u userAPI) {
	m.ID, m.Email, m.Name, m.Role, m.Disabled = types.StringValue(u.ID), types.StringValue(u.Email), types.StringValue(u.Name), types.StringValue(u.Role), types.BoolValue(u.Disabled)
}

func (r *userResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan userModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := map[string]any{"email": plan.Email.ValueString(), "name": plan.Name.ValueString(), "role": plan.Role.ValueString()}
	var out struct {
		User     userAPI `json:"user"`
		Password string  `json:"temporary_password"`
	}
	if err := r.c.do(ctx, "POST", "/users", body, &out); err != nil {
		resp.Diagnostics.AddError("Creating the user failed", err.Error())
		return
	}
	disabled := plan.Disabled.ValueBool()
	plan.fill(out.User)
	plan.TemporaryPassword = types.StringValue(out.Password)
	if disabled { // users are created enabled; apply the requested state
		if err := r.c.do(ctx, "PATCH", "/users/"+out.User.ID, map[string]any{"disabled": true}, nil); err != nil {
			resp.Diagnostics.AddError("Disabling the user failed", err.Error())
			return
		}
		plan.Disabled = types.BoolValue(true)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *userResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state userModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	var list []userAPI
	if err := r.c.do(ctx, "GET", "/users", nil, &list); err != nil {
		resp.Diagnostics.AddError("Reading users failed", err.Error())
		return
	}
	for _, u := range list {
		if u.ID == state.ID.ValueString() {
			state.fill(u)
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *userResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state userModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := map[string]any{}
	if !plan.Name.IsUnknown() {
		body["name"] = plan.Name.ValueString()
	}
	if !plan.Role.IsUnknown() && plan.Role.ValueString() != "" {
		body["role"] = plan.Role.ValueString()
	}
	if !plan.Disabled.IsUnknown() {
		body["disabled"] = plan.Disabled.ValueBool()
	}
	var out userAPI
	if err := r.c.do(ctx, "PATCH", "/users/"+state.ID.ValueString(), body, &out); err != nil {
		resp.Diagnostics.AddError("Updating the user failed", err.Error())
		return
	}
	plan.fill(out)
	plan.TemporaryPassword = state.TemporaryPassword
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *userResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state userModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if err := r.c.do(ctx, "DELETE", "/users/"+state.ID.ValueString(), nil, nil); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Deleting the user failed", err.Error())
	}
}
