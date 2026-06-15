// SPDX-License-Identifier: AGPL-3.0-or-later

package ubus

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Section is a decoded UCI section: its type, name/anonymous flag, and option
// values split into scalars (Options) and arrays (Lists). Meta keys (".type",
// ".name", ".anonymous", …) are not included in Options/Lists.
type Section struct {
	Type      string
	Name      string
	Anonymous bool
	Options   map[string]string
	Lists     map[string][]string
}

// GetSection reads a single section by name (or anonymous id). The bool is
// false when the section does not exist.
//
// A successful `uci get` that returns no data/values is treated as not-found
// rather than an error: addressing a section by an id that no longer resolves
// (notably a stale anonymous `cfgXXXX` id after UCI renumbered the config)
// comes back as a code-only result. The caller re-resolves anonymous sections
// by identity, so reporting not-found here lets that recovery run instead of
// failing the refresh.
func (c *Client) GetSection(config, section string) (*Section, bool, error) {
	data, err := c.Call("uci", "get", map[string]any{
		"config":  config,
		"section": section,
	})
	if err != nil {
		if IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if len(data) == 0 {
		return nil, false, nil // code-only result: id does not resolve
	}
	var wrap struct {
		Values json.RawMessage `json:"values"`
	}
	if err := json.Unmarshal(data, &wrap); err != nil {
		return nil, false, fmt.Errorf("decode uci get response for %s.%s: %w", config, section, err)
	}
	if len(wrap.Values) == 0 {
		return nil, false, nil
	}
	sec, err := decodeSection(wrap.Values)
	if err != nil {
		return nil, false, err
	}
	return sec, true, nil
}

// ListSections returns every section in a config keyed by its UCI section id
// (the name for named sections, the server-assigned `cfgXXXX` id for anonymous
// ones). It underpins re-resolution of anonymous sections whose stored id has
// gone stale.
func (c *Client) ListSections(config string) (map[string]*Section, error) {
	data, err := c.Call("uci", "get", map[string]any{"config": config})
	if err != nil {
		if IsNotFound(err) {
			return map[string]*Section{}, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return map[string]*Section{}, nil
	}
	var wrap struct {
		Values map[string]json.RawMessage `json:"values"`
	}
	if err := json.Unmarshal(data, &wrap); err != nil {
		return nil, fmt.Errorf("decode uci config %q: %w", config, err)
	}
	out := make(map[string]*Section, len(wrap.Values))
	for id, raw := range wrap.Values {
		sec, err := decodeSection(raw)
		if err != nil {
			return nil, fmt.Errorf("decode section %q in config %q: %w", id, config, err)
		}
		out[id] = sec
	}
	return out, nil
}

// SectionMatches reports whether s is of secType and carries every option in
// identity with the same value. An empty identity never matches: with nothing
// to disambiguate on, claiming a section would be a guess.
func SectionMatches(s *Section, secType string, identity map[string]string) bool {
	if s == nil || s.Type != secType || len(identity) == 0 {
		return false
	}
	for k, v := range identity {
		if s.Options[k] != v {
			return false
		}
	}
	return true
}

// FindUniqueSection scans all for the single section matching (secType,
// identity). n is the number of matches; id and sec are set only when n == 1.
// A caller treats n == 0 as not-found and n > 1 as ambiguous (it must not
// guess which drifted section is the managed one).
func FindUniqueSection(all map[string]*Section, secType string, identity map[string]string) (id string, sec *Section, n int) {
	for k, s := range all {
		if SectionMatches(s, secType, identity) {
			id, sec = k, s
			n++
		}
	}
	if n != 1 {
		return "", nil, n
	}
	return id, sec, 1
}

func decodeSection(raw json.RawMessage) (*Section, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("decode uci section: %w", err)
	}
	sec := &Section{Options: map[string]string{}, Lists: map[string][]string{}}
	for k, v := range m {
		switch k {
		case ".type":
			_ = json.Unmarshal(v, &sec.Type)
			continue
		case ".name":
			_ = json.Unmarshal(v, &sec.Name)
			continue
		case ".anonymous":
			_ = json.Unmarshal(v, &sec.Anonymous)
			continue
		}
		if len(k) > 0 && k[0] == '.' {
			continue // other meta keys (.index, etc.)
		}
		// A scalar comes back as a JSON string; a list as a JSON array.
		var s string
		if err := json.Unmarshal(v, &s); err == nil {
			sec.Options[k] = s
			continue
		}
		var l []string
		if err := json.Unmarshal(v, &l); err == nil {
			sec.Lists[k] = l
			continue
		}
		// Fallback: keep the raw JSON so nothing is silently dropped.
		sec.Options[k] = string(v)
	}
	return sec, nil
}

// AddSection creates a section. When name is non-empty a named section is
// created; otherwise an anonymous section is created and its server-assigned id
// is returned. values may mix scalars (string) and lists ([]string).
func (c *Client) AddSection(config, secType, name string, values map[string]any) (string, error) {
	args := map[string]any{"config": config, "type": secType}
	if name != "" {
		args["name"] = name
	}
	if len(values) > 0 {
		args["values"] = values
	}
	data, err := c.Call("uci", "add", args)
	if err != nil {
		return "", err
	}
	if name != "" {
		return name, nil
	}
	var out struct {
		Section string `json:"section"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("decode uci add response: %w", err)
	}
	if out.Section == "" {
		return "", fmt.Errorf("uci add returned an empty section id")
	}
	return out.Section, nil
}

// SetOptions sets (creates or overwrites) the given options on a section.
func (c *Client) SetOptions(config, section string, values map[string]any) error {
	if len(values) == 0 {
		return nil
	}
	_, err := c.Call("uci", "set", map[string]any{
		"config":  config,
		"section": section,
		"values":  values,
	})
	return err
}

// DeleteOptions removes the named options from a section. Missing options are
// not an error.
func (c *Client) DeleteOptions(config, section string, options []string) error {
	if len(options) == 0 {
		return nil
	}
	_, err := c.Call("uci", "delete", map[string]any{
		"config":  config,
		"section": section,
		"options": options,
	})
	if IsNotFound(err) {
		return nil
	}
	return err
}

// DeleteSection removes an entire section. A missing section is not an error.
func (c *Client) DeleteSection(config, section string) error {
	_, err := c.Call("uci", "delete", map[string]any{
		"config":  config,
		"section": section,
	})
	if IsNotFound(err) {
		return nil
	}
	return err
}

// Commit persists staged changes for a config.
func (c *Client) Commit(config string) error {
	_, err := c.Call("uci", "commit", map[string]any{"config": config})
	return err
}

// ReloadConfig triggers rpcd to reload services affected by committed changes.
func (c *Client) ReloadConfig() error {
	_, err := c.Call("uci", "reload_config", map[string]any{})
	return err
}

// ReloadService runs `/etc/init.d/<service> reload` via the `file.exec` ubus
// method. This is a SEAMLESS reload — it re-applies a service's config without
// bouncing it (the firewall ruleset reload preserves connection state; dnsmasq
// re-reads leases/hosts) — and, unlike ReloadConfig, it fires even when no uci
// change is staged, so it can reconcile a live service back to committed config.
// It returns an error on a transport/ACL failure or a non-zero exit from the
// init script. Only seamless init scripts belong here: `network`, wireguard, and
// the like restart interfaces/tunnels and would drop the management path.
func (c *Client) ReloadService(service string) error {
	data, err := c.Call("file", "exec", map[string]any{
		"command": "/etc/init.d/" + service,
		"params":  []string{"reload"},
	})
	if err != nil {
		return err
	}
	var out struct {
		Code   int    `json:"code"`
		Stderr string `json:"stderr"`
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &out); err != nil {
			return fmt.Errorf("decode file.exec result: %w", err)
		}
	}
	if out.Code != 0 {
		msg := strings.TrimSpace(out.Stderr)
		if msg == "" {
			msg = "no stderr"
		}
		return fmt.Errorf("/etc/init.d/%s reload: exit %d: %s", service, out.Code, msg)
	}
	return nil
}
