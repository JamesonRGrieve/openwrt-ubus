// SPDX-License-Identifier: AGPL-3.0-or-later

package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/JamesonRGrieve/openwrt-ubus/internal/ubus"
)

var (
	_ resource.Resource              = &ubusCallResource{}
	_ resource.ResourceWithConfigure = &ubusCallResource{}
)

// ubusCallResource invokes an arbitrary ubus object.method — the imperative
// escape hatch covering every non-uci feature ubus exposes (service control,
// system actions, runtime operations). It is the action analogue of the
// declarative uci_section resource; together they span the whole bus.
type ubusCallResource struct {
	client *ubus.Client
}

// NewUbusCallResource is the resource constructor.
func NewUbusCallResource() resource.Resource {
	return &ubusCallResource{}
}

type ubusCallModel struct {
	ID       types.String `tfsdk:"id"`
	Object   types.String `tfsdk:"object"`
	Method   types.String `tfsdk:"method"`
	Params   types.String `tfsdk:"params"`
	Triggers types.Map    `tfsdk:"triggers"`
	Result   types.String `tfsdk:"result"`
}

func (r *ubusCallResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ubus_call"
}

func (r *ubusCallResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Invoke an arbitrary ubus `object.method` — the imperative escape hatch for any " +
			"non-uci feature (e.g. `service`/`restart`, `system`/`reboot`, runtime calls). Re-invoked on create " +
			"and whenever `triggers` change.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`<object>.<method>` identifier.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"object": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "ubus object (e.g. `service`, `system`, `network`).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"method": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "ubus method on the object (e.g. `restart`, `reboot`).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"params": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "JSON object of method arguments, e.g. `{\"name\":\"firewall\"}`. Defaults to `{}`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"triggers": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Arbitrary values that, when changed, re-invoke the call (like `null_resource` triggers).",
			},
			"result": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "JSON-encoded response data from the most recent invocation.",
			},
		},
	}
}

func (r *ubusCallResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*ubus.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data",
			fmt.Sprintf("expected *ubus.Client, got %T", req.ProviderData))
		return
	}
	r.client = client
}

func (r *ubusCallResource) invoke(object, method, paramsJSON string) (string, error) {
	var args any = map[string]any{}
	if paramsJSON != "" {
		if err := json.Unmarshal([]byte(paramsJSON), &args); err != nil {
			return "", fmt.Errorf("params is not valid JSON: %w", err)
		}
	}
	data, err := r.client.Call(object, method, args)
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "{}", nil
	}
	return string(data), nil
}

func (r *ubusCallResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ubusCallModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	result, err := r.invoke(plan.Object.ValueString(), plan.Method.ValueString(), plan.Params.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("ubus call failed", err.Error())
		return
	}
	plan.Result = types.StringValue(result)
	plan.ID = types.StringValue(plan.Object.ValueString() + "." + plan.Method.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read is a no-op: an imperative call has no persistent server state to refresh.
func (r *ubusCallResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ubusCallModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ubusCallResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan ubusCallModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	result, err := r.invoke(plan.Object.ValueString(), plan.Method.ValueString(), plan.Params.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("ubus call failed", err.Error())
		return
	}
	plan.Result = types.StringValue(result)
	plan.ID = types.StringValue(plan.Object.ValueString() + "." + plan.Method.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete is a no-op: an imperative call cannot be "undone" on destroy.
func (r *ubusCallResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}
