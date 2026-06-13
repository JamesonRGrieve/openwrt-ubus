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

func TestIsNotFound(t *testing.T) {
	if !IsNotFound(&StatusError{Object: "uci", Method: "get", Code: StatusNotFound}) {
		t.Errorf("IsNotFound(StatusNotFound) = false, want true")
	}
	if IsNotFound(&StatusError{Object: "uci", Method: "get", Code: StatusPermissionDenied}) {
		t.Errorf("IsNotFound(StatusPermissionDenied) = true, want false")
	}
}
