// SPDX-License-Identifier: AGPL-3.0-or-later

// Package provider implements the openwrt-ubus OpenTofu/Terraform provider: a
// native ubus-over-HTTP manager for OpenWrt configuration, with no dependency
// on LuCI RPC.
package provider

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/JamesonRGrieve/tofu-openwrt-ubus/internal/ubus"
)

// Ensure the provider satisfies the framework interface.
var _ provider.Provider = &openwrtProvider{}

type openwrtProvider struct {
	version string
}

// New returns a provider constructor for the given build version.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &openwrtProvider{version: version}
	}
}

type providerModel struct {
	Host     types.String `tfsdk:"host"`
	Scheme   types.String `tfsdk:"scheme"`
	Port     types.Int64  `tfsdk:"port"`
	Username types.String `tfsdk:"username"`
	Password types.String `tfsdk:"password"`
	Insecure types.Bool   `tfsdk:"insecure"`
	Timeout  types.Int64  `tfsdk:"timeout_seconds"`
}

func (p *openwrtProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "openwrt"
	resp.Version = p.version
}

func (p *openwrtProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage OpenWrt configuration over the native ubus-over-HTTP JSON-RPC API " +
			"(rpcd + uhttpd-mod-ubus). No LuCI RPC (`luci-mod-rpc`) required.",
		Attributes: map[string]schema.Attribute{
			"host": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "OpenWrt host or IP address.",
			},
			"scheme": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "`http` or `https`. Defaults to `https`.",
			},
			"port": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "ubus port. Defaults to the scheme default (443/80).",
			},
			"username": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "ubus login username. Defaults to `root`.",
			},
			"password": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "ubus login password.",
			},
			"insecure": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Skip TLS certificate verification. Defaults to `true` (OpenWrt ships a self-signed cert).",
			},
			"timeout_seconds": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Per-request HTTP timeout in seconds. Defaults to `30`.",
			},
		},
	}
}

func (p *openwrtProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if cfg.Host.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("host"), "Unknown host",
			"The provider cannot be configured with an unknown host value.")
		return
	}
	host := strings.TrimSpace(cfg.Host.ValueString())
	if host == "" {
		resp.Diagnostics.AddAttributeError(path.Root("host"), "Missing host",
			"The provider requires a non-empty host.")
		return
	}

	scheme := "https"
	if !cfg.Scheme.IsNull() && cfg.Scheme.ValueString() != "" {
		scheme = strings.ToLower(cfg.Scheme.ValueString())
	}
	if scheme != "http" && scheme != "https" {
		resp.Diagnostics.AddAttributeError(path.Root("scheme"), "Invalid scheme",
			fmt.Sprintf("scheme must be http or https, got %q", scheme))
		return
	}

	hostport := host
	if !cfg.Port.IsNull() && cfg.Port.ValueInt64() != 0 {
		hostport = net.JoinHostPort(host, strconv.FormatInt(cfg.Port.ValueInt64(), 10))
	}
	endpoint := (&url.URL{Scheme: scheme, Host: hostport, Path: "/ubus"}).String()

	username := "root"
	if !cfg.Username.IsNull() && cfg.Username.ValueString() != "" {
		username = cfg.Username.ValueString()
	}
	insecure := true
	if !cfg.Insecure.IsNull() {
		insecure = cfg.Insecure.ValueBool()
	}
	timeout := 30 * time.Second
	if !cfg.Timeout.IsNull() && cfg.Timeout.ValueInt64() > 0 {
		timeout = time.Duration(cfg.Timeout.ValueInt64()) * time.Second
	}

	client := ubus.NewClient(ubus.Config{
		Endpoint: endpoint,
		Username: username,
		Password: cfg.Password.ValueString(),
		Insecure: insecure,
		Timeout:  timeout,
	})
	resp.ResourceData = client
	resp.DataSourceData = client
}

func (p *openwrtProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewUCISectionResource,
		NewUbusCallResource,
		NewReconcileResource,
	}
}

func (p *openwrtProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewUCISectionDataSource,
		NewUbusCallDataSource,
	}
}
