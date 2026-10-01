package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type policyResource struct{ c *apiClient }

func newPolicy() resource.Resource { return &policyResource{} }

type policyModel struct {
	ID       types.String `tfsdk:"id"`
	Document types.String `tfsdk:"document"`
	Comment  types.String `tfsdk:"comment"`
	Version  types.Int64  `tfsdk:"version"`
}

func (r *policyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_policy"
}

func (r *policyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The network access policy (HuJSON). Every change is validated, its tests must pass, and it becomes a new version in the history. There is always exactly one policy; destroying this resource leaves the current one in place.",
		Attributes: map[string]schema.Attribute{
			"id":       schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"document": schema.StringAttribute{Required: true, Description: "The policy document."},
			"comment":  schema.StringAttribute{Optional: true, Description: "Shown in the version history."},
			"version":  schema.Int64Attribute{Computed: true},
		},
	}
}

func (r *policyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.c = clientFrom(req.ProviderData, &resp.Diagnostics)
}

type policyAPI struct {
	Version  int64  `json:"version"`
	Document string `json:"document"`
}

func (r *policyResource) save(ctx context.Context, plan *policyModel, diags interface{ AddError(string, string) }) bool {
	var out policyAPI
	err := r.c.do(ctx, "PUT", "/policy", map[string]any{"document": plan.Document.ValueString(), "comment": plan.Comment.ValueString()}, &out)
	if err != nil {
		diags.AddError("Saving the policy failed", err.Error())
		return false
	}
	plan.ID, plan.Version = types.StringValue("policy"), types.Int64Value(out.Version)
	return true
}

func (r *policyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan policyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || !r.save(ctx, &plan, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *policyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state policyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	var cur policyAPI
	if err := r.c.do(ctx, "GET", "/policy", nil, &cur); err != nil {
		resp.Diagnostics.AddError("Reading the policy failed", err.Error())
		return
	}
	state.Document, state.Version = types.StringValue(cur.Document), types.Int64Value(cur.Version)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *policyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan policyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || !r.save(ctx, &plan, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *policyResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {}
