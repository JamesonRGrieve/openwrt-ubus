// SPDX-License-Identifier: AGPL-3.0-or-later

package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/JamesonRGrieve/tofu-openwrt-ubus/internal/ubus"
)

func TestStoredIDIsAuthoritative(t *testing.T) {
	device := &ubus.Section{
		Type:      "device",
		Anonymous: true,
		Options:   map[string]string{"name": "br-lan", "type": "bridge"},
	}
	cases := []struct {
		name      string
		sec       *ubus.Section
		secType   string
		anonymous bool
		identity  map[string]string
		want      bool
	}{
		{"nil section is never authoritative", nil, "device", true, nil, false},
		{"named section: stable id, accept", device, "device", false, nil, true},
		// The import-survival case: a freshly imported anonymous section captures
		// no options, so identity is empty; the stored id still resolves to the
		// right type and must be accepted (else its post-import Read deletes it).
		{"anon, empty identity, type matches: accept (import survives)", device, "device", true, nil, true},
		{"anon, empty identity, type matches (empty map): accept", device, "device", true, map[string]string{}, true},
		// A non-empty identity that matches is accepted normally.
		{"anon, identity matches: accept", device, "device", true, map[string]string{"name": "br-lan"}, true},
		// A non-empty identity that does NOT match falls through to re-resolution.
		{"anon, identity mismatches: not authoritative (re-resolve)", device, "device", true, map[string]string{"name": "br-wan"}, false},
		// An empty identity whose stored id resolves to the WRONG type must not be
		// blindly accepted.
		{"anon, empty identity, type mismatches: not authoritative", device, "bridge-vlan", true, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := storedIDIsAuthoritative(tc.sec, tc.secType, tc.anonymous, tc.identity); got != tc.want {
				t.Errorf("storedIDIsAuthoritative(%q, anon=%v, id=%v) = %v, want %v",
					tc.secType, tc.anonymous, tc.identity, got, tc.want)
			}
		})
	}
}

func TestSplitID(t *testing.T) {
	cases := []struct {
		in      string
		config  string
		section string
		ok      bool
	}{
		{"network.lan", "network", "lan", true},
		{"firewall.cfg012345", "firewall", "cfg012345", true},
		{"network.@interface[0]", "network", "@interface[0]", true},
		{"bad", "", "", false},
		{".lan", "", "", false},
		{"network.", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		config, section, ok := splitID(c.in)
		if ok != c.ok || config != c.config || section != c.section {
			t.Errorf("splitID(%q) = (%q,%q,%v), want (%q,%q,%v)",
				c.in, config, section, ok, c.config, c.section, c.ok)
		}
	}
}

func TestRemovedKeys(t *testing.T) {
	ctx := context.Background()
	mkOpts := func(kv map[string]string) types.Map {
		m, _ := types.MapValueFrom(ctx, types.StringType, kv)
		return m
	}

	state := uciSectionModel{
		Options: mkOpts(map[string]string{"proto": "static", "ipaddr": "10.0.0.1", "gone": "x"}),
		Lists:   types.MapNull(listElemType),
	}
	plan := uciSectionModel{
		Options: mkOpts(map[string]string{"proto": "static", "ipaddr": "10.0.0.2"}),
		Lists:   types.MapNull(listElemType),
	}

	got := removedKeys(state, plan)
	if len(got) != 1 || got[0] != "gone" {
		t.Errorf("removedKeys = %v, want [gone]", got)
	}
}
