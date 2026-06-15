// SPDX-License-Identifier: AGPL-3.0-or-later

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/JamesonRGrieve/tofu-openwrt-ubus/internal/ubus"
)

var (
	_ resource.Resource              = (*reconcileResource)(nil)
	_ resource.ResourceWithConfigure = (*reconcileResource)(nil)
)

// NewReconcileResource constructs the openwrt_reconcile resource: an
// unconditional reload that runs `/etc/init.d/<svc> reload` for each named
// service on every run. It manages no remote object — it exists to heal
// config-vs-live drift Terraform cannot detect. The provider tracks uci config
// (what it reads back over ubus), not the live running service state, so a plan
// with 0 object changes never re-applies. The per-section `uci reload_config`
// only reloads services whose config a tofu write just staged, so it cannot heal
// a config-only divergence (a manual `uci set` + commit without a reload, an
// offline/partial adoption, a reboot loading stale state). This re-runs the
// seamless reloads unconditionally. Pair with a `triggers` map holding
// `timestamp()` to fire every run. Use ONLY for seamless reloads
// (firewall/dnsmasq/odhcpd); never for `network` or wireguard, whose reloads
// reassign interfaces/tunnels and can drop the management path mid-apply.
func NewReconcileResource() resource.Resource { return &reconcileResource{} }

type reconcileResource struct {
	client *ubus.Client
}

type reconcileModel struct {
	ID       types.String `tfsdk:"id"`
	Services types.List   `tfsdk:"services"`
	Triggers types.Map    `tfsdk:"triggers"`
}

func (r *reconcileResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_reconcile"
}

func (r *reconcileResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Unconditional reconcile. Runs `/etc/init.d/<svc> reload` (via the `file.exec` ubus " +
			"method) for each service in `services` on every create/update — it manages no remote object. Pair with " +
			"a `triggers` map containing `timestamp()` so it re-runs on every run, healing config-vs-live drift " +
			"Terraform cannot detect (the provider tracks uci config, not the live service state, and `uci " +
			"reload_config` only reloads services a staged write touched, so a 0-change plan otherwise never " +
			"re-applies). Use ONLY for seamless reloads (`firewall`, `dnsmasq`, `odhcpd`); NEVER for `network` or " +
			"`wireguard`, whose reloads reassign interfaces/tunnels and can drop the management path mid-apply.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Static resource id (`reconcile`).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"services": schema.ListAttribute{
				Required:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Ordered list of init.d service names to `reload` (seamless reloads only — e.g. `firewall`, `dnsmasq`).",
			},
			"triggers": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Arbitrary key/value map; any change re-runs the reload. Set a key to `timestamp()` to fire every run.",
			},
		},
	}
}

func (r *reconcileResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// runReconcile reloads each service in order via reload and returns one warning
// string per failed service plus allFailed=true when every service failed. A
// per-service failure is tolerated (best-effort: an optional service may be
// absent on a given box); total failure means the device is unreachable or every
// service is wrong, which the caller escalates to an error. Pure (reload is
// injected) so the aggregation is unit-testable without a live device.
func runReconcile(services []string, reload func(service string) error) (warnings []string, allFailed bool) {
	failed := 0
	for _, s := range services {
		if err := reload(s); err != nil {
			failed++
			warnings = append(warnings, fmt.Sprintf("%s: %s", s, err.Error()))
		}
	}
	return warnings, len(services) > 0 && failed == len(services)
}

func (r *reconcileResource) reconcile(ctx context.Context, m reconcileModel, diags *diag.Diagnostics) {
	var svcs []string
	diags.Append(m.Services.ElementsAs(ctx, &svcs, false)...)
	if diags.HasError() {
		return
	}
	warnings, allFailed := runReconcile(svcs, func(s string) error { return r.client.ReloadService(s) })
	for _, w := range warnings {
		diags.AddWarning("OpenWrt reconcile service failed", w)
	}
	if allFailed {
		diags.AddError("OpenWrt reconcile failed",
			"every reconcile service failed — the device is likely unreachable or all service names are invalid")
	}
}

func (r *reconcileResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var m reconcileModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.reconcile(ctx, m, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	m.ID = types.StringValue("reconcile")
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *reconcileResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	// No remote object to read; keep prior state verbatim.
	var m reconcileModel
	resp.Diagnostics.Append(req.State.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *reconcileResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var m reconcileModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &m)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.reconcile(ctx, m, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	m.ID = types.StringValue("reconcile")
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

func (r *reconcileResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
	// Manages no remote object — nothing to delete.
}
