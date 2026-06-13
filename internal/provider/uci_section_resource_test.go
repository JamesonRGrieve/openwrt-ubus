// SPDX-License-Identifier: AGPL-3.0-or-later

package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

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
