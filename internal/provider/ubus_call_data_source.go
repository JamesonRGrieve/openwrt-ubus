// SPDX-License-Identifier: AGPL-3.0-or-later

package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/JamesonRGrieve/tofu-openwrt-ubus/internal/ubus"
)

var (
	_ datasource.DataSource              = &ubusCallDataSource{}
	_ datasource.DataSourceWithConfigure = &ubusCallDataSource{}
)

// ubusCallDataSource reads the result of an arbitrary ubus object.method —
// runtime state for any feature (e.g. `system.board`, `network.interface`
// `dump`, `iwinfo`). Pure read; no device mutation.
type ubusCallDataSource struct {
	client *ubus.Client
}

// NewUbusCallDataSource is the data source constructor.
func NewUbusCallDataSource() datasource.DataSource {
	return &ubusCallDataSource{}
}

type ubusCallDataModel struct {
	ID     types.String `tfsdk:"id"`
	Object types.String `tfsdk:"object"`
	Method types.String `tfsdk:"method"`
	Params types.String `tfsdk:"params"`
	Result types.String `tfsdk:"result"`
}

func (d *ubusCallDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ubus_call"
}

func (d *ubusCallDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Read the result of an arbitrary ubus `object.method` (runtime state for any feature, " +
			"e.g. `system`/`board`, `network.interface`/`dump`, `iwinfo`/`info`).",
		Attributes: map[string]schema.Attribute{
			"id":     schema.StringAttribute{Computed: true, MarkdownDescription: "`<object>.<method>` identifier."},
			"object": schema.StringAttribute{Required: true, MarkdownDescription: "ubus object (e.g. `system`)."},
			"method": schema.StringAttribute{Required: true, MarkdownDescription: "ubus method (e.g. `board`)."},
			"params": schema.StringAttribute{Optional: true, MarkdownDescription: "JSON object of method arguments. Defaults to `{}`."},
			"result": schema.StringAttribute{Computed: true, MarkdownDescription: "JSON-encoded response data."},
		},
	}
}

func (d *ubusCallDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *ubusCallDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data ubusCallDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var args any = map[string]any{}
	if !data.Params.IsNull() && data.Params.ValueString() != "" {
		if err := json.Unmarshal([]byte(data.Params.ValueString()), &args); err != nil {
			resp.Diagnostics.AddError("Invalid params", fmt.Sprintf("params is not valid JSON: %s", err))
			return
		}
	}
	raw, err := d.client.Call(data.Object.ValueString(), data.Method.ValueString(), args)
	if err != nil {
		resp.Diagnostics.AddError("ubus call failed", err.Error())
		return
	}
	result := "{}"
	if len(raw) > 0 {
		result = string(raw)
	}
	data.Result = types.StringValue(result)
	data.ID = types.StringValue(data.Object.ValueString() + "." + data.Method.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
