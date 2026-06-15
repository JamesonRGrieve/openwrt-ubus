// SPDX-License-Identifier: AGPL-3.0-or-later

package provider

import (
	"errors"
	"testing"
)

func TestRunReconcile(t *testing.T) {
	tests := []struct {
		name     string
		services []string
		reload   func(string) error
		wantWarn int
		wantAll  bool
	}{
		{
			name:     "all ok",
			services: []string{"firewall", "dnsmasq"},
			reload:   func(string) error { return nil },
			wantWarn: 0, wantAll: false,
		},
		{
			name:     "no services is a no-op",
			services: nil,
			reload:   func(string) error { return errors.New("should not be called") },
			wantWarn: 0, wantAll: false,
		},
		{
			name:     "transport error on the only service escalates",
			services: []string{"firewall"},
			reload:   func(string) error { return errors.New("ubus file.exec failed: status 6 (permission denied)") },
			wantWarn: 1, wantAll: true,
		},
		{
			name:     "non-zero exit on the only service escalates",
			services: []string{"firewall"},
			reload:   func(string) error { return errors.New("/etc/init.d/firewall reload: exit 1: ...") },
			wantWarn: 1, wantAll: true,
		},
		{
			name:     "partial failure warns but does not escalate",
			services: []string{"firewall", "odhcpd"},
			reload: func(s string) error {
				if s == "odhcpd" {
					return errors.New("/etc/init.d/odhcpd: not found") // service absent on this box
				}
				return nil
			},
			wantWarn: 1, wantAll: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warns, all := runReconcile(tt.services, tt.reload)
			if len(warns) != tt.wantWarn {
				t.Errorf("warnings = %d (%v), want %d", len(warns), warns, tt.wantWarn)
			}
			if all != tt.wantAll {
				t.Errorf("allFailed = %v, want %v", all, tt.wantAll)
			}
		})
	}
}
