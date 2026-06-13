// SPDX-License-Identifier: AGPL-3.0-or-later

package ubus

import (
	"encoding/json"
	"testing"
)

func TestDecodeSection(t *testing.T) {
	raw := json.RawMessage(`{
		".type": "interface",
		".name": "lan",
		".anonymous": false,
		".index": 3,
		"proto": "static",
		"ipaddr": "192.168.1.1",
		"ports": ["lan1", "lan2:t"]
	}`)

	sec, err := decodeSection(raw)
	if err != nil {
		t.Fatalf("decodeSection: %v", err)
	}
	if sec.Type != "interface" {
		t.Errorf("Type = %q, want interface", sec.Type)
	}
	if sec.Name != "lan" {
		t.Errorf("Name = %q, want lan", sec.Name)
	}
	if sec.Anonymous {
		t.Errorf("Anonymous = true, want false")
	}
	if sec.Options["proto"] != "static" || sec.Options["ipaddr"] != "192.168.1.1" {
		t.Errorf("Options = %v, missing scalar values", sec.Options)
	}
	if _, ok := sec.Options[".index"]; ok {
		t.Errorf("meta key .index leaked into Options")
	}
	if got := sec.Lists["ports"]; len(got) != 2 || got[0] != "lan1" || got[1] != "lan2:t" {
		t.Errorf("Lists[ports] = %v, want [lan1 lan2:t]", got)
	}
}

func TestSectionMatches(t *testing.T) {
	brvlan := &Section{
		Type:      "bridge-vlan",
		Anonymous: true,
		Options:   map[string]string{"device": "br-lan", "vlan": "58"},
	}
	cases := []struct {
		name     string
		sec      *Section
		secType  string
		identity map[string]string
		want     bool
	}{
		{"full match", brvlan, "bridge-vlan", map[string]string{"device": "br-lan", "vlan": "58"}, true},
		{"match on subset of options", brvlan, "bridge-vlan", map[string]string{"vlan": "58"}, true},
		{"wrong type", brvlan, "interface", map[string]string{"vlan": "58"}, false},
		{"value mismatch", brvlan, "bridge-vlan", map[string]string{"vlan": "59"}, false},
		{"missing option", brvlan, "bridge-vlan", map[string]string{"pvid": "1"}, false},
		{"empty identity never matches", brvlan, "bridge-vlan", map[string]string{}, false},
		{"nil section", nil, "bridge-vlan", map[string]string{"vlan": "58"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SectionMatches(c.sec, c.secType, c.identity); got != c.want {
				t.Errorf("SectionMatches = %v, want %v", got, c.want)
			}
		})
	}
}

func TestFindUniqueSection(t *testing.T) {
	// Models a network config after a 59->58 cutover renumbered anonymous ids:
	// the VID-58 bridge-vlan is what we want to re-resolve by identity.
	all := map[string]*Section{
		"lan":       {Type: "interface", Name: "lan", Options: map[string]string{"proto": "static"}},
		"cfg05a1b0": {Type: "bridge-vlan", Anonymous: true, Options: map[string]string{"device": "br-lan", "vlan": "1"}},
		"cfg0ca1b0": {Type: "bridge-vlan", Anonymous: true, Options: map[string]string{"device": "br-lan", "vlan": "58"}},
		"cfg07a1b0": {Type: "bridge-vlan", Anonymous: true, Options: map[string]string{"device": "br-lan", "vlan": "82"}},
	}

	t.Run("unique match returns id and section", func(t *testing.T) {
		id, sec, n := FindUniqueSection(all, "bridge-vlan", map[string]string{"device": "br-lan", "vlan": "58"})
		if n != 1 || id != "cfg0ca1b0" || sec == nil || sec.Options["vlan"] != "58" {
			t.Fatalf("got id=%q n=%d sec=%v, want cfg0ca1b0/1", id, n, sec)
		}
	})
	t.Run("no match", func(t *testing.T) {
		id, sec, n := FindUniqueSection(all, "bridge-vlan", map[string]string{"device": "br-lan", "vlan": "999"})
		if n != 0 || id != "" || sec != nil {
			t.Fatalf("got id=%q n=%d sec=%v, want 0/empty/nil", id, n, sec)
		}
	})
	t.Run("ambiguous match returns count without choosing", func(t *testing.T) {
		// Two sections share the identity (device only) — caller must not guess.
		id, sec, n := FindUniqueSection(all, "bridge-vlan", map[string]string{"device": "br-lan"})
		if n != 3 || id != "" || sec != nil {
			t.Fatalf("got id=%q n=%d sec=%v, want 3/empty/nil", id, n, sec)
		}
	})
}

func TestIsNotFound(t *testing.T) {
	if !IsNotFound(&StatusError{Object: "uci", Method: "get", Code: StatusNotFound}) {
		t.Errorf("IsNotFound(StatusNotFound) = false, want true")
	}
	if IsNotFound(&StatusError{Object: "uci", Method: "get", Code: StatusPermissionDenied}) {
		t.Errorf("IsNotFound(StatusPermissionDenied) = true, want false")
	}
}
