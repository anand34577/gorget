package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type setupKeyResource struct{ c *apiClient }

func newSetupKey() resource.Resource { return &setupKeyResource{} }

type setupKeyModel struct {
	ID            types.String `tfsdk:"id"`
	Name          types.String `tfsdk:"name"`
	Reusable      types.Bool   `tfsdk:"reusable"`
	Ephemeral     types.Bool   `tfsdk:"ephemeral"`
	AutoApprove   types.Bool   `tfsdk:"auto_approve"`
	Tags          types.List   `tfsdk:"tags"`
	MaxUses       types.Int64  `tfsdk:"max_uses"`
	ExpiresInDays types.Int64  `tfsdk:"expires_in_days"`
	Key           types.String `tfsdk:"key"`
	KeyPrefix     types.String `tfsdk:"key_prefix"`
}

func (r *setupKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_setup_key"
}

func (r *setupKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		Description: "A setup key that registers devices without a browser sign-in. Changing any argument creates a new key.",
		Attributes: map[string]schema.Attribute{
			"id":              schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"name":            schema.StringAttribute{Required: true, PlanModifiers: replace},
			"reusable":        schema.BoolAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.Bool{boolplanmodifier.RequiresReplace(), boolplanmodifier.UseStateForUnknown()}},
			"ephemeral":       schema.BoolAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.Bool{boolplanmodifier.RequiresReplace(), boolplanmodifier.UseStateForUnknown()}},
			"auto_approve":    schema.BoolAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.Bool{boolplanmodifier.RequiresReplace(), boolplanmodifier.UseStateForUnknown()}},
			"tags":            schema.ListAttribute{Optional: true, Computed: true, ElementType: types.StringType, PlanModifiers: []planmodifier.List{listplanmodifier.RequiresReplace(), listplanmodifier.UseStateForUnknown()}, Description: "Tags like tag:server given to devices that register with this key."},
			"max_uses":        schema.Int64Attribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace(), int64planmodifier.UseStateForUnknown()}},
			"expires_in_days": schema.Int64Attribute{Optional: true, PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()}},
			"key":             schema.StringAttribute{Computed: true, Sensitive: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, Description: "The secret key, available only after creation."},
			"key_prefix":      schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		},
	}
}

func (r *setupKeyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.c = clientFrom(req.ProviderData, &resp.Diagnostics)
}

type setupKeyAPI struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	KeyPrefix string   `json:"key_prefix"`
	Reusable  bool     `json:"reusable"`
	Ephemeral bool     `json:"ephemeral"`
	Approve   bool     `json:"auto_approve"`
	Tags      []string `json:"tags"`
	MaxUses   int64    `json:"max_uses"`
}

func (r *setupKeyResource) fill(ctx context.Context, m *setupKeyModel, k setupKeyAPI) {
	m.ID, m.Name, m.KeyPrefix = types.StringValue(k.ID), types.StringValue(k.Name), types.StringValue(k.KeyPrefix)
	m.Reusable, m.Ephemeral, m.AutoApprove, m.MaxUses = types.BoolValue(k.Reusable), types.BoolValue(k.Ephemeral), types.BoolValue(k.Approve), types.Int64Value(k.MaxUses)
	m.Tags = stringList(k.Tags)
}

func (r *setupKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan setupKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := map[string]any{"name": plan.Name.ValueString(), "reusable": plan.Reusable.ValueBool(), "ephemeral": plan.Ephemeral.ValueBool(),
		"tags": listStrings(plan.Tags), "max_uses": plan.MaxUses.ValueInt64(), "expires_in_days": plan.ExpiresInDays.ValueInt64()}
	if !plan.AutoApprove.IsNull() && !plan.AutoApprove.IsUnknown() {
		body["auto_approve"] = plan.AutoApprove.ValueBool()
	}
	var out struct {
		Key      string      `json:"key"`
		SetupKey setupKeyAPI `json:"setup_key"`
	}
	if err := r.c.do(ctx, "POST", "/setup-keys", body, &out); err != nil {
		resp.Diagnostics.AddError("Creating the setup key failed", err.Error())
		return
	}
	r.fill(ctx, &plan, out.SetupKey)
	plan.Key = types.StringValue(out.Key)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *setupKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state setupKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var list []setupKeyAPI
	if err := r.c.do(ctx, "GET", "/setup-keys", nil, &list); err != nil {
		resp.Diagnostics.AddError("Reading setup keys failed", err.Error())
		return
	}
	for _, k := range list {
		if k.ID == state.ID.ValueString() {
			r.fill(ctx, &state, k)
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *setupKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// Every argument forces a new key, so an update only happens for computed values.
	var plan setupKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *setupKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state setupKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if err := r.c.do(ctx, "DELETE", "/setup-keys/"+state.ID.ValueString(), nil, nil); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Deleting the setup key failed", err.Error())
	}
}
