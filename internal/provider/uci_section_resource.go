// SPDX-License-Identifier: AGPL-3.0-or-later

package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/JamesonRGrieve/tofu-openwrt-ubus/internal/ubus"
)

var (
	_ resource.Resource                = &uciSectionResource{}
	_ resource.ResourceWithImportState = &uciSectionResource{}
	_ resource.ResourceWithConfigure   = &uciSectionResource{}
	_ resource.ResourceWithModifyPlan  = &uciSectionResource{}
)

// uciSectionResource manages a single UCI section over ubus. It is deliberately
// generic: any config/type/section is expressible, so it covers network,
// firewall, dhcp, wireless, system, etc. without a typed resource per kind.
//
// Drift semantics: the resource manages exactly the options/lists declared in
// configuration. Read refreshes the declared keys from the device and ignores
// undeclared options the device may carry, so an imported or co-managed box
// does not produce phantom diffs.
type uciSectionResource struct {
	client *ubus.Client
}

// NewUCISectionResource is the resource constructor.
func NewUCISectionResource() resource.Resource {
	return &uciSectionResource{}
}

type uciSectionModel struct {
	ID               types.String `tfsdk:"id"`
	Config           types.String `tfsdk:"config"`
	Type             types.String `tfsdk:"type"`
	Name             types.String `tfsdk:"name"`
	Section          types.String `tfsdk:"section"`
	Options          types.Map    `tfsdk:"options"`
	Lists            types.Map    `tfsdk:"lists"`
	RecreateOnChange types.Set    `tfsdk:"recreate_on_change"`
	RemoveOptions    types.Set    `tfsdk:"remove_options"`
}

var listElemType = types.ListType{ElemType: types.StringType}

func (r *uciSectionResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_uci_section"
}

func (r *uciSectionResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A single UCI configuration section, managed over ubus. Works for any uci config " +
			"(`network`, `firewall`, `dhcp`, `wireless`, `system`, …). Importable by `<config>.<section>`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`<config>.<section>` identifier.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"config": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "UCI config file (e.g. `network`, `firewall`).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"type": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Section type (e.g. `interface`, `bridge-vlan`, `rule`).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Named-section name. Omit for an anonymous section (server-assigned id).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"section": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resolved UCI section id (equals `name` for named sections, else the server-assigned anonymous id like `cfg012345`).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"options": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Scalar UCI options. The resource manages ONLY the keys you declare (uci set merges); device options not declared are left untouched and never deleted. Import does not capture device options, so the first apply sets the declared keys (idempotent for unchanged values) without disturbing the rest.",
			},
			"lists": schema.MapAttribute{
				Optional:            true,
				ElementType:         listElemType,
				MarkdownDescription: "List-valued UCI options. Manages only declared keys (see options).",
			},
			"recreate_on_change": schema.SetAttribute{
				Optional:    true,
				ElementType: types.StringType,
				MarkdownDescription: "Option keys that are identity-defining and cannot be changed in place — " +
					"a change to any of them forces the section to be destroyed and recreated. Required for " +
					"DSA bridge-vlan `vlan` (VID): OpenWrt's `uci set vlan=...` + reload does NOT retag the bridge, " +
					"so the section must be removed and re-added.",
			},
			"remove_options": schema.SetAttribute{
				Optional:    true,
				ElementType: types.StringType,
				MarkdownDescription: "Option keys to explicitly DELETE from the section on every apply, even if they were " +
					"never declared/managed by this resource. Unlike `options` (subset semantics — undeclared device keys are " +
					"left untouched and never deleted, and import captures none), these are removed unconditionally. Deleting a " +
					"missing option is a no-op. Use to strip stock/adopted config the model wants gone — e.g. clearing a base-LAN " +
					"`ipaddr`/`netmask` to turn it into a pure-L2 interface. Do not list a key that also appears in `options` " +
					"(the delete would undo the set).",
			},
		},
	}
}

// ModifyPlan forces replacement when an identity-defining option (listed in
// recreate_on_change) differs between prior state and plan — an in-place uci set
// won't take effect for these (e.g. a bridge-vlan VID on DSA).
func (r *uciSectionResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return // create or destroy — nothing to compare
	}
	var state, plan uciSectionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || plan.RecreateOnChange.IsNull() || plan.RecreateOnChange.IsUnknown() {
		return
	}
	var keys []string
	resp.Diagnostics.Append(plan.RecreateOnChange.ElementsAs(ctx, &keys, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	so := state.Options.Elements()
	po := plan.Options.Elements()
	for _, k := range keys {
		sv, sok := so[k]
		pv, pok := po[k]
		// Replace only when the key is present in BOTH and differs — a genuine
		// in-place-impossible change (e.g. a bridge-vlan VID 59->58). When state
		// lacks the key (fresh import captures no options), adopt without
		// recreating; the declared value is applied as a normal in-place set.
		if sok && pok && !sv.Equal(pv) {
			resp.RequiresReplace = append(resp.RequiresReplace, path.Root("options"))
			return
		}
	}
}

func (r *uciSectionResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *uciSectionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan uciSectionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	values, diags := buildValues(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Serialize the whole add+commit+reload so a parallel resource's commit
	// can't clobber it (see Client.writeMu).
	r.client.LockWrites()
	defer r.client.UnlockWrites()

	config := plan.Config.ValueString()
	name := ""
	if !plan.Name.IsNull() {
		name = plan.Name.ValueString()
	}
	section, err := r.client.AddSection(config, plan.Type.ValueString(), name, values)
	if err != nil {
		resp.Diagnostics.AddError("uci add failed", err.Error())
		return
	}
	// Strip any explicitly-removed options (stock/adopted keys the model wants
	// gone). On a genuinely new section these don't exist yet (no-op); on a named
	// section the device already carries with defaults, this clears them.
	if rm := explicitRemovals(ctx, plan, &resp.Diagnostics); len(rm) > 0 {
		if err := r.client.DeleteOptions(config, section, rm); err != nil {
			resp.Diagnostics.AddError("uci delete option failed", err.Error())
			return
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.Commit(config); err != nil {
		resp.Diagnostics.AddError("uci commit failed", err.Error())
		return
	}
	if err := r.client.ReloadConfig(); err != nil {
		resp.Diagnostics.AddWarning("uci reload_config failed", err.Error())
	}

	plan.Section = types.StringValue(section)
	plan.ID = types.StringValue(config + "." + section)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *uciSectionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state uciSectionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	config := state.Config.ValueString()
	section := state.Section.ValueString()
	if section == "" {
		if c, s, ok := splitID(state.ID.ValueString()); ok {
			config, section = c, s
		}
	}

	anonymous := state.Name.IsNull() || state.Name.ValueString() == ""
	identity := optionsToStringMap(ctx, state.Options)

	sec, resolvedID, found, err := r.resolveSection(config, section, state.Type.ValueString(), anonymous, identity)
	if err != nil {
		resp.Diagnostics.AddError("uci get failed", err.Error())
		return
	}
	if !found {
		resp.State.RemoveResource(ctx)
		return
	}

	// Persist the (possibly re-resolved) live section id so subsequent
	// plan/apply address the section by its current id, never a stale one.
	state.Section = types.StringValue(resolvedID)
	state.ID = types.StringValue(config + "." + resolvedID)

	state.Type = types.StringValue(sec.Type)

	if !state.Options.IsNull() {
		out := map[string]string{}
		for k := range state.Options.Elements() {
			if v, ok := sec.Options[k]; ok {
				out[k] = v
			}
		}
		v, d := types.MapValueFrom(ctx, types.StringType, out)
		resp.Diagnostics.Append(d...)
		state.Options = v
	}
	if !state.Lists.IsNull() {
		out := map[string][]string{}
		for k := range state.Lists.Elements() {
			if v, ok := sec.Lists[k]; ok {
				out[k] = v
			}
		}
		v, d := types.MapValueFrom(ctx, listElemType, out)
		resp.Diagnostics.Append(d...)
		state.Lists = v
	}
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *uciSectionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state uciSectionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	config := state.Config.ValueString()
	anonymous := state.Name.IsNull() || state.Name.ValueString() == ""
	identity := optionsToStringMap(ctx, state.Options)

	values, diags := buildValues(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	removed := removedKeys(state, plan)
	// Plus any options the model asks to strip unconditionally (stock/adopted
	// device options never captured in state, e.g. a legacy base-LAN ipaddr).
	removed = append(removed, explicitRemovals(ctx, plan, &resp.Diagnostics)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Serialize the whole set/delete+commit+reload (see Client.writeMu). Resolve
	// the live section id INSIDE the lock so a sibling's commit can't renumber an
	// anonymous id between resolution and our mutation.
	r.client.LockWrites()
	defer r.client.UnlockWrites()

	_, section, found, err := r.resolveSection(config, state.Section.ValueString(), state.Type.ValueString(), anonymous, identity)
	if err != nil {
		resp.Diagnostics.AddError("uci get failed", err.Error())
		return
	}
	if !found {
		resp.Diagnostics.AddError("uci section not found",
			fmt.Sprintf("%s.%s no longer exists on the device; cannot update", config, state.Section.ValueString()))
		return
	}

	if err := r.client.SetOptions(config, section, values); err != nil {
		resp.Diagnostics.AddError("uci set failed", err.Error())
		return
	}
	if err := r.client.DeleteOptions(config, section, removed); err != nil {
		resp.Diagnostics.AddError("uci delete option failed", err.Error())
		return
	}
	if err := r.client.Commit(config); err != nil {
		resp.Diagnostics.AddError("uci commit failed", err.Error())
		return
	}
	if err := r.client.ReloadConfig(); err != nil {
		resp.Diagnostics.AddWarning("uci reload_config failed", err.Error())
	}

	plan.ID = types.StringValue(config + "." + section)
	plan.Section = types.StringValue(section)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *uciSectionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state uciSectionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	config := state.Config.ValueString()
	anonymous := state.Name.IsNull() || state.Name.ValueString() == ""
	identity := optionsToStringMap(ctx, state.Options)

	// Serialize the whole delete+commit+reload (see Client.writeMu). Resolve the
	// live section id INSIDE the lock so a sibling's commit can't renumber an
	// anonymous id out from under us.
	r.client.LockWrites()
	defer r.client.UnlockWrites()

	_, section, found, err := r.resolveSection(config, state.Section.ValueString(), state.Type.ValueString(), anonymous, identity)
	if err != nil {
		resp.Diagnostics.AddError("uci get failed", err.Error())
		return
	}
	if !found {
		return // already gone — nothing to delete
	}
	if err := r.client.DeleteSection(config, section); err != nil {
		resp.Diagnostics.AddError("uci delete failed", err.Error())
		return
	}
	if err := r.client.Commit(config); err != nil {
		resp.Diagnostics.AddError("uci commit failed", err.Error())
		return
	}
	if err := r.client.ReloadConfig(); err != nil {
		resp.Diagnostics.AddWarning("uci reload_config failed", err.Error())
	}
}

func (r *uciSectionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	config, section, ok := splitID(req.ID)
	if !ok {
		resp.Diagnostics.AddError("Invalid import id",
			"expected `<config>.<section>` (e.g. `network.lan` or `firewall.cfg012345`)")
		return
	}
	sec, found, err := r.client.GetSection(config, section)
	if err != nil {
		resp.Diagnostics.AddError("uci get failed", err.Error())
		return
	}
	if !found {
		resp.Diagnostics.AddError("Section not found", fmt.Sprintf("%s.%s does not exist on the device", config, section))
		return
	}

	m := uciSectionModel{
		ID:               types.StringValue(config + "." + section),
		Config:           types.StringValue(config),
		Type:             types.StringValue(sec.Type),
		Section:          types.StringValue(section),
		Name:             types.StringNull(),
		RecreateOnChange: types.SetNull(types.StringType),
	}
	if sec.Name != "" && !sec.Anonymous {
		m.Name = types.StringValue(sec.Name)
	}

	// Do NOT capture device options into state on import. The resource manages
	// only the options/lists declared in config (additive — uci set merges).
	// Capturing the full device section would make the authoritative map plan to
	// DELETE undeclared device defaults (interface gateway, ip6assign, proto,
	// system compat_version, …) on the first apply. Leaving them null means the
	// first apply only SETS the declared keys (idempotent for unchanged values)
	// and never strips live config the config doesn't mention. (sec is read only
	// to confirm existence + bind config/type/name/section above.)
	_ = sec
	m.Options = types.MapNull(types.StringType)
	m.Lists = types.MapNull(listElemType)
	resp.Diagnostics.Append(resp.State.Set(ctx, &m)...)
}

// resolveSection returns the live section and its current UCI id for a managed
// section, recovering from anonymous-id drift. For a named section the id is
// stable and used as-is. For an anonymous section whose stored `cfgXXXX` id no
// longer addresses a section matching the managed identity (its declared scalar
// options), the section is re-resolved by scanning the config for the unique
// section of the same type with matching options — OpenWrt renumbers anonymous
// ids whenever the config file is rewritten by a sibling add/delete, so the
// stored id goes stale. found is false when the section is genuinely gone.
func (r *uciSectionResource) resolveSection(config, storedID, secType string, anonymous bool, identity map[string]string) (*ubus.Section, string, bool, error) {
	sec, found, err := r.client.GetSection(config, storedID)
	if err != nil {
		return nil, "", false, err
	}
	if found && storedIDIsAuthoritative(sec, secType, anonymous, identity) {
		return sec, storedID, true, nil
	}
	if !anonymous {
		return nil, "", false, nil // named section: stable id, genuinely gone
	}
	// Anonymous section: the stored id is stale or now points at a different
	// section. Re-resolve it by identity. With no identity to match on (e.g. a
	// bare import whose stored id no longer resolves to its type) we cannot
	// safely re-resolve, so report it not-found rather than risk adopting the
	// wrong section.
	if len(identity) == 0 {
		return nil, "", false, nil
	}
	all, err := r.client.ListSections(config)
	if err != nil {
		return nil, "", false, err
	}
	id, rsec, n := ubus.FindUniqueSection(all, secType, identity)
	switch n {
	case 1:
		return rsec, id, true, nil
	case 0:
		return nil, "", false, nil
	default:
		return nil, "", false, fmt.Errorf(
			"cannot re-resolve anonymous %s section in config %q: %d sections match identity %v",
			secType, config, n, identity)
	}
}

// storedIDIsAuthoritative reports whether the section GetSection returned for the
// stored id should be accepted as-is, rather than re-resolved by identity. It is
// pure so the accept rule is unit-testable independent of the ubus transport.
//
//   - A named section's id is stable: accept it.
//   - An anonymous section is accepted when it matches the managed identity, OR —
//     crucially — when there is no identity to match on AND the stored id resolves
//     to a section of the expected type. A freshly imported section captures no
//     options (additive import), so its first Read has an empty identity; the
//     user-supplied cfgXXXX id is authoritative there. Without this an anonymous
//     import is deleted by its own post-import Read ("Cannot import non-existent
//     remote object"). Identity re-resolution remains the fallback for a stale id
//     that now resolves to the wrong section (caller handles that path).
func storedIDIsAuthoritative(sec *ubus.Section, secType string, anonymous bool, identity map[string]string) bool {
	if sec == nil {
		return false
	}
	if !anonymous {
		return true
	}
	if ubus.SectionMatches(sec, secType, identity) {
		return true
	}
	return len(identity) == 0 && sec.Type == secType
}

// optionsToStringMap extracts a model's declared scalar options as a plain map
// for identity matching. Returns nil when none are declared (an import captures
// none), so re-resolution has nothing to match on and falls back to the stored
// id.
func optionsToStringMap(ctx context.Context, m types.Map) map[string]string {
	if m.IsNull() || m.IsUnknown() {
		return nil
	}
	out := map[string]string{}
	if diags := m.ElementsAs(ctx, &out, false); diags.HasError() || len(out) == 0 {
		return nil
	}
	return out
}

// buildValues merges declared options (scalars) and lists (arrays) into the
// values map ubus uci add/set expects.
func buildValues(ctx context.Context, m uciSectionModel) (map[string]any, diag.Diagnostics) {
	var diags diag.Diagnostics
	values := map[string]any{}
	if !m.Options.IsNull() && !m.Options.IsUnknown() {
		opts := map[string]string{}
		diags.Append(m.Options.ElementsAs(ctx, &opts, false)...)
		for k, v := range opts {
			values[k] = v
		}
	}
	if !m.Lists.IsNull() && !m.Lists.IsUnknown() {
		lists := map[string][]string{}
		diags.Append(m.Lists.ElementsAs(ctx, &lists, false)...)
		for k, v := range lists {
			values[k] = v
		}
	}
	return values, diags
}

// removedKeys returns option/list keys present in prior state but absent from
// the plan — these must be uci-deleted on update.
func removedKeys(state, plan uciSectionModel) []string {
	var removed []string
	planOpts := plan.Options.Elements()
	for k := range state.Options.Elements() {
		if _, ok := planOpts[k]; !ok {
			removed = append(removed, k)
		}
	}
	planLists := plan.Lists.Elements()
	for k := range state.Lists.Elements() {
		if _, ok := planLists[k]; !ok {
			removed = append(removed, k)
		}
	}
	return removed
}

// explicitRemovals returns the keys listed in remove_options — options to delete
// unconditionally on apply (stock/adopted device options never captured in
// state). DeleteOptions treats a missing key as a no-op, so this is idempotent.
func explicitRemovals(ctx context.Context, plan uciSectionModel, diags *diag.Diagnostics) []string {
	if plan.RemoveOptions.IsNull() || plan.RemoveOptions.IsUnknown() {
		return nil
	}
	var out []string
	diags.Append(plan.RemoveOptions.ElementsAs(ctx, &out, false)...)
	return out
}

func splitID(id string) (config, section string, ok bool) {
	parts := strings.SplitN(id, ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}
