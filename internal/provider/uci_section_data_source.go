// SPDX-License-Identifier: AGPL-3.0-or-later

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/JamesonRGrieve/tofu-openwrt-ubus/internal/ubus"
)

var (
	_ datasource.DataSource              = &uciSectionDataSource{}
	_ datasource.DataSourceWithConfigure = &uciSectionDataSource{}
)

type uciSectionDataSource struct {
	client *ubus.Client
}

// NewUCISectionDataSource is the data source constructor.
func NewUCISectionDataSource() datasource.DataSource {
	return &uciSectionDataSource{}
}

type uciSectionDataModel struct {
	ID      types.String `tfsdk:"id"`
	Config  types.String `tfsdk:"config"`
	Section types.String `tfsdk:"section"`
	Exists  types.Bool   `tfsdk:"exists"`
	Type    types.String `tfsdk:"type"`
	Name    types.String `tfsdk:"name"`
	Options types.Map    `tfsdk:"options"`
	Lists   types.Map    `tfsdk:"lists"`
}

func (d *uciSectionDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_uci_section"
}

func (d *uciSectionDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Read a single UCI section over ubus.",
		Attributes: map[string]schema.Attribute{
			"id":      schema.StringAttribute{Computed: true, MarkdownDescription: "`<config>.<section>` identifier."},
			"config":  schema.StringAttribute{Required: true, MarkdownDescription: "UCI config file (e.g. `network`)."},
			"section": schema.StringAttribute{Required: true, MarkdownDescription: "Section name or anonymous id."},
			"exists":  schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the section exists."},
			"type":    schema.StringAttribute{Computed: true, MarkdownDescription: "Section type."},
			"name":    schema.StringAttribute{Computed: true, MarkdownDescription: "Section name (empty for anonymous)."},
			"options": schema.MapAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Scalar options."},
			"lists":   schema.MapAttribute{Computed: true, ElementType: listElemType, MarkdownDescription: "List options."},
		},
	}
}

func (d *uciSectionDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*ubus.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data",
			fmt.Sprintf("expected *ubus.Client, got %T", req.ProviderData))
		return
	}
	d.client = client
}

func (d *uciSectionDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data uciSectionDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	config := data.Config.ValueString()
	section := data.Section.ValueString()
	data.ID = types.StringValue(config + "." + section)

	sec, found, err := d.client.GetSection(config, section)
	if err != nil {
		resp.Diagnostics.AddError("uci get failed", err.Error())
		return
	}
	data.Exists = types.BoolValue(found)
	if !found {
		data.Type = types.StringNull()
		data.Name = types.StringNull()
		data.Options = types.MapNull(types.StringType)
		data.Lists = types.MapNull(listElemType)
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		return
	}

	data.Type = types.StringValue(sec.Type)
	if sec.Name != "" && !sec.Anonymous {
		data.Name = types.StringValue(sec.Name)
	} else {
		data.Name = types.StringNull()
	}
	opts, dOpts := types.MapValueFrom(ctx, types.StringType, sec.Options)
	resp.Diagnostics.Append(dOpts...)
	lists, dLists := types.MapValueFrom(ctx, listElemType, sec.Lists)
	resp.Diagnostics.Append(dLists...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Options = opts
	data.Lists = lists
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
