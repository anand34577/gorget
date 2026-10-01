package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type groupResource struct{ c *apiClient }

func newGroup() resource.Resource { return &groupResource{} }

type groupModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Members     types.Set    `tfsdk:"members"`
}

func (r *groupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_group"
}

func (r *groupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A group of users, usable in the access policy as group:<name>.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"name":        schema.StringAttribute{Required: true, Description: "Lowercase letters, digits and dashes."},
			"description": schema.StringAttribute{Optional: true},
			"members":     schema.SetAttribute{Optional: true, Computed: true, ElementType: types.StringType, Description: "User ids (gorget_user.id)."},
		},
	}
}

func (r *groupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.c = clientFrom(req.ProviderData, &resp.Diagnostics)
}

type groupAPI struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Members     []string `json:"members"`
}

func (m *groupModel) fill(g groupAPI) {
	m.ID, m.Name, m.Members = types.StringValue(g.ID), types.StringValue(g.Name), stringSet(g.Members)
	if g.Description != "" || !m.Description.IsNull() {
		m.Description = types.StringValue(g.Description)
	}
}

func (r *groupResource) body(m groupModel) map[string]any {
	return map[string]any{"name": m.Name.ValueString(), "description": m.Description.ValueString(), "members": setStrings(m.Members)}
}

func (r *groupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan groupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var out groupAPI
	if err := r.c.do(ctx, "POST", "/groups", r.body(plan), &out); err != nil {
		resp.Diagnostics.AddError("Creating the group failed", err.Error())
		return
	}
	plan.fill(out)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *groupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state groupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	var list []groupAPI
	if err := r.c.do(ctx, "GET", "/groups", nil, &list); err != nil {
		resp.Diagnostics.AddError("Reading groups failed", err.Error())
		return
	}
	for _, g := range list {
		if g.ID == state.ID.ValueString() {
			state.fill(g)
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *groupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state groupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var out groupAPI
	if err := r.c.do(ctx, "PATCH", "/groups/"+state.ID.ValueString(), r.body(plan), &out); err != nil {
		resp.Diagnostics.AddError("Updating the group failed", err.Error())
		return
	}
	plan.fill(out)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *groupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state groupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if err := r.c.do(ctx, "DELETE", "/groups/"+state.ID.ValueString(), nil, nil); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Deleting the group failed", err.Error())
	}
}
