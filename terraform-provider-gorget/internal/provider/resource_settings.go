package provider

import (
	"context"
	"encoding/json"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type settingsResource struct{ c *apiClient }

func newSettings() resource.Resource { return &settingsResource{} }

type settingsModel struct {
	ID      types.String `tfsdk:"id"`
	Section types.String `tfsdk:"section"`
	JSON    types.String `tfsdk:"json"`
}

func (r *settingsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_settings"
}

func (r *settingsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Replaces one settings section (network, dns, devices, client, auth, gateway, posture or routing) with the given JSON. The JSON must contain the whole section, as returned by GET /api/v1/settings. Destroying the resource leaves the settings as they are.",
		Attributes: map[string]schema.Attribute{
			"id":      schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"section": schema.StringAttribute{Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"json":    schema.StringAttribute{Required: true, Description: "The section as JSON, e.g. jsonencode({...})."},
		},
	}
}

func (r *settingsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.c = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (r *settingsResource) apply(ctx context.Context, m *settingsModel, diags interface{ AddError(string, string) }) bool {
	var body json.RawMessage = json.RawMessage(m.JSON.ValueString())
	if !json.Valid(body) {
		diags.AddError("Invalid JSON", "the json argument is not valid JSON")
		return false
	}
	if err := r.c.do(ctx, "PUT", "/settings/"+m.Section.ValueString(), body, nil); err != nil {
		diags.AddError("Saving the settings failed", err.Error())
		return false
	}
	m.ID = m.Section
	return true
}

func (r *settingsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan settingsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || !r.apply(ctx, &plan, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read keeps the configured JSON: the server normalises values (defaults, ordering), so
// comparing would show endless differences.
func (r *settingsResource) Read(context.Context, resource.ReadRequest, *resource.ReadResponse) {}

func (r *settingsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan settingsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || !r.apply(ctx, &plan, &resp.Diagnostics) {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *settingsResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}
