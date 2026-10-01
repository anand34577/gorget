// Package provider implements the Gorget Terraform provider.
package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type gorgetProvider struct{ version string }

// New returns the provider factory.
func New(version string) func() provider.Provider {
	return func() provider.Provider { return &gorgetProvider{version: version} }
}

type providerModel struct {
	URL      types.String `tfsdk:"url"`
	Token    types.String `tfsdk:"token"`
	Insecure types.Bool   `tfsdk:"insecure_skip_verify"`
}

func (p *gorgetProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "gorget"
	resp.Version = p.version
}

func (p *gorgetProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage a self-hosted Gorget network as code.",
		Attributes: map[string]schema.Attribute{
			"url":                  schema.StringAttribute{Optional: true, Description: "Server URL, e.g. https://vpn.example.com (or GORGET_URL)."},
			"token":                schema.StringAttribute{Optional: true, Sensitive: true, Description: "API token with read/write access (or GORGET_TOKEN). Create it under Account > API tokens."},
			"insecure_skip_verify": schema.BoolAttribute{Optional: true, Description: "Skip TLS verification (only for lab servers with self-signed certificates)."},
		},
	}
}

func (p *gorgetProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var m providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	url, token := m.URL.ValueString(), m.Token.ValueString()
	if url == "" {
		url = os.Getenv("GORGET_URL")
	}
	if token == "" {
		token = os.Getenv("GORGET_TOKEN")
	}
	if url == "" || token == "" {
		resp.Diagnostics.AddError("Missing configuration", "Set the provider arguments url and token, or the environment variables GORGET_URL and GORGET_TOKEN.")
		return
	}
	c := newClient(url, token, m.Insecure.ValueBool())
	resp.ResourceData, resp.DataSourceData = c, c
}

func (p *gorgetProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{newSetupKey, newPolicy, newGroup, newUser, newWebhook, newSettings}
}

func (p *gorgetProvider) DataSources(context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{newDevices}
}

// clientFrom extracts the API client given to resources and data sources.
func clientFrom(data any, diags interface{ AddError(string, string) }) *apiClient {
	if data == nil {
		return nil
	}
	c, ok := data.(*apiClient)
	if !ok {
		diags.AddError("Unexpected provider data", "expected the Gorget API client")
		return nil
	}
	return c
}
