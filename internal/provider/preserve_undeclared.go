// SPDX-License-Identifier: AGPL-3.0-or-later

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// preserveUndeclaredMap makes a map attribute ADDITIVE: keys present in prior
// state but absent from config are carried into the plan, so the resource never
// removes options/lists it wasn't told about. This is what lets an imported uci
// section keep its device-managed defaults (e.g. an interface's gateway,
// ip6assign, proto) while the config manages only the declared intent keys.
//
// Trade-off: removing a key from config does NOT remove it from the device
// (manage it out-of-band, e.g. via openwrt_ubus_call). For additive adoption of
// shared device config this is the safe default — it cannot strip live config.
type preserveUndeclaredMap struct{}

// PreserveUndeclaredMap returns the plan modifier.
func PreserveUndeclaredMap() planmodifier.Map { return preserveUndeclaredMap{} }

func (m preserveUndeclaredMap) Description(_ context.Context) string {
	return "Preserves prior-state keys absent from config (additive map management; never deletes undeclared keys)."
}

func (m preserveUndeclaredMap) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (m preserveUndeclaredMap) PlanModifyMap(ctx context.Context, req planmodifier.MapRequest, resp *planmodifier.MapResponse) {
	// Nothing to preserve on create or when values are unknown.
	if req.StateValue.IsNull() || req.StateValue.IsUnknown() || req.PlanValue.IsUnknown() {
		return
	}
	stateElems := req.StateValue.Elements()
	if len(stateElems) == 0 {
		return
	}

	planElems := map[string]attr.Value{}
	if !req.PlanValue.IsNull() {
		for k, v := range req.PlanValue.Elements() {
			planElems[k] = v
		}
	}

	changed := false
	for k, v := range stateElems {
		if _, ok := planElems[k]; !ok {
			planElems[k] = v // carry the undeclared device key into the plan
			changed = true
		}
	}
	if !changed {
		return
	}

	merged, diags := types.MapValue(req.PlanValue.ElementType(ctx), planElems)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.PlanValue = merged
}
