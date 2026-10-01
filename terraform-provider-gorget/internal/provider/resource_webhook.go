package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type webhookResource struct{ c *apiClient }

func newWebhook() resource.Resource { return &webhookResource{} }

type webhookModel struct {
	ID      types.String `tfsdk:"id"`
	Name    types.String `tfsdk:"name"`
	URL     types.String `tfsdk:"url"`
	Events  types.Set    `tfsdk:"events"`
	Enabled types.Bool   `tfsdk:"enabled"`
	Secret  types.String `tfsdk:"secret"`
}

func (r *webhookResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_webhook"
}

func (r *webhookResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A signed webhook that receives events such as device.created.",
		Attributes: map[string]schema.Attribute{
			"id":      schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"name":    schema.StringAttribute{Optional: true, Computed: true},
			"url":     schema.StringAttribute{Required: true},
			"events":  schema.SetAttribute{Optional: true, Computed: true, ElementType: types.StringType, Description: "Event names, or \"*\"; empty means all."},
			"enabled": schema.BoolAttribute{Optional: true, Computed: true},
			"secret":  schema.StringAttribute{Computed: true, Sensitive: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, Description: "Signing secret, available only after creation."},
		},
	}
}

func (r *webhookResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.c = clientFrom(req.ProviderData, &resp.Diagnostics)
}

type webhookAPI struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	URL     string   `json:"url"`
	Events  []string `json:"events"`
	Enabled bool     `json:"enabled"`
}

func (m *webhookModel) fill(h webhookAPI) {
	m.ID, m.Name, m.URL, m.Events, m.Enabled = types.StringValue(h.ID), types.StringValue(h.Name), types.StringValue(h.URL), stringSet(h.Events), types.BoolValue(h.Enabled)
}

func (r *webhookResource) body(m webhookModel) map[string]any {
	b := map[string]any{"url": m.URL.ValueString(), "events": setStrings(m.Events)}
	if !m.Name.IsUnknown() {
		b["name"] = m.Name.ValueString()
	}
	if !m.Enabled.IsUnknown() {
		b["enabled"] = m.Enabled.ValueBool()
	}
	return b
}

func (r *webhookResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan webhookModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var out struct {
		Webhook webhookAPI `json:"webhook"`
		Secret  string     `json:"secret"`
	}
	if err := r.c.do(ctx, "POST", "/webhooks", r.body(plan), &out); err != nil {
		resp.Diagnostics.AddError("Creating the webhook failed", err.Error())
		return
	}
	enabled := plan.Enabled
	plan.fill(out.Webhook)
	plan.Secret = types.StringValue(out.Secret)
	if !enabled.IsUnknown() && !enabled.IsNull() && enabled.ValueBool() != out.Webhook.Enabled { // created enabled; apply the requested state
		if err := r.c.do(ctx, "PATCH", "/webhooks/"+out.Webhook.ID, map[string]any{"enabled": enabled.ValueBool()}, nil); err != nil {
			resp.Diagnostics.AddError("Updating the webhook failed", err.Error())
			return
		}
		plan.Enabled = enabled
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *webhookResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state webhookModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	var out struct {
		Webhooks []webhookAPI `json:"webhooks"`
	}
	if err := r.c.do(ctx, "GET", "/webhooks", nil, &out); err != nil {
		resp.Diagnostics.AddError("Reading webhooks failed", err.Error())
		return
	}
	for _, h := range out.Webhooks {
		if h.ID == state.ID.ValueString() {
			state.fill(h)
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx)
}

func (r *webhookResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state webhookModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var out webhookAPI
	if err := r.c.do(ctx, "PATCH", "/webhooks/"+state.ID.ValueString(), r.body(plan), &out); err != nil {
		resp.Diagnostics.AddError("Updating the webhook failed", err.Error())
		return
	}
	plan.fill(out)
	plan.Secret = state.Secret
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *webhookResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state webhookModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if err := r.c.do(ctx, "DELETE", "/webhooks/"+state.ID.ValueString(), nil, nil); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("Deleting the webhook failed", err.Error())
	}
}
