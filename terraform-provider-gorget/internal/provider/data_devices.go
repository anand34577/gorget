package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type devicesData struct{ c *apiClient }

func newDevices() datasource.DataSource { return &devicesData{} }

type deviceItem struct {
	ID      types.String `tfsdk:"id"`
	Name    types.String `tfsdk:"name"`
	FQDN    types.String `tfsdk:"fqdn"`
	IPv4    types.String `tfsdk:"ipv4"`
	OS      types.String `tfsdk:"os"`
	Kind    types.String `tfsdk:"kind"`
	User    types.String `tfsdk:"user_email"`
	Tags    types.List   `tfsdk:"tags"`
	Online  types.Bool   `tfsdk:"online"`
	Exit    types.Bool   `tfsdk:"exit_node"`
	Posture types.List   `tfsdk:"posture_failures"`
}

type devicesModel struct {
	Kind    types.String `tfsdk:"kind"`
	Tag     types.String `tfsdk:"tag"`
	Devices []deviceItem `tfsdk:"devices"`
}

func (d *devicesData) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_devices"
}

func (d *devicesData) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	item := map[string]schema.Attribute{
		"id": schema.StringAttribute{Computed: true}, "name": schema.StringAttribute{Computed: true}, "fqdn": schema.StringAttribute{Computed: true},
		"ipv4": schema.StringAttribute{Computed: true}, "os": schema.StringAttribute{Computed: true}, "kind": schema.StringAttribute{Computed: true},
		"user_email": schema.StringAttribute{Computed: true}, "tags": schema.ListAttribute{Computed: true, ElementType: types.StringType},
		"online": schema.BoolAttribute{Computed: true}, "exit_node": schema.BoolAttribute{Computed: true},
		"posture_failures": schema.ListAttribute{Computed: true, ElementType: types.StringType},
	}
	resp.Schema = schema.Schema{
		Description: "Devices in the network, optionally filtered by kind (native, wireguard, gateway) or tag.",
		Attributes: map[string]schema.Attribute{
			"kind":    schema.StringAttribute{Optional: true},
			"tag":     schema.StringAttribute{Optional: true},
			"devices": schema.ListNestedAttribute{Computed: true, NestedObject: schema.NestedAttributeObject{Attributes: item}},
		},
	}
}

func (d *devicesData) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.c = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func (d *devicesData) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg devicesModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	path := "/devices"
	if !cfg.Kind.IsNull() && cfg.Kind.ValueString() != "" {
		path += "?kind=" + cfg.Kind.ValueString()
	}
	var list []struct {
		ID      string   `json:"id"`
		Name    string   `json:"name"`
		FQDN    string   `json:"fqdn"`
		IPv4    string   `json:"ipv4"`
		OS      string   `json:"os"`
		Kind    string   `json:"kind"`
		Email   string   `json:"user_email"`
		Tags    []string `json:"tags"`
		Online  bool     `json:"online"`
		Exit    bool     `json:"exit_advertised"`
		ExitOK  bool     `json:"exit_approved"`
		Posture []string `json:"posture"`
	}
	if err := d.c.do(ctx, "GET", path, nil, &list); err != nil {
		resp.Diagnostics.AddError("Reading devices failed", err.Error())
		return
	}
	cfg.Devices = []deviceItem{}
	for _, x := range list {
		if tag := cfg.Tag.ValueString(); tag != "" && !contains(x.Tags, tag) {
			continue
		}
		cfg.Devices = append(cfg.Devices, deviceItem{
			ID: types.StringValue(x.ID), Name: types.StringValue(x.Name), FQDN: types.StringValue(x.FQDN), IPv4: types.StringValue(x.IPv4),
			OS: types.StringValue(x.OS), Kind: types.StringValue(x.Kind), User: types.StringValue(x.Email), Tags: stringList(x.Tags),
			Online: types.BoolValue(x.Online), Exit: types.BoolValue(x.Exit && x.ExitOK), Posture: stringList(x.Posture),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}
