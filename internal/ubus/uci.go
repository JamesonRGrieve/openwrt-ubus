// SPDX-License-Identifier: AGPL-3.0-or-later

package ubus

import (
	"encoding/json"
	"fmt"
	"time"
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
func (c *Client) GetSection(config, section string) (*Section, bool, error) {
	// Retry an empty/undecodable read: even serialized, the device occasionally
	// returns a code-only `uci get` result (no `values`) under load. That is a
	// transient glitch for a section we're addressing by id, not a real
	// not-found — so retry briefly rather than fail the refresh.
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 400 * time.Millisecond)
		}
		data, err := c.Call("uci", "get", map[string]any{
			"config":  config,
			"section": section,
		})
		if err != nil {
			if IsNotFound(err) {
				return nil, false, nil
			}
			lastErr = err
			continue
		}
		var wrap struct {
			Values json.RawMessage `json:"values"`
		}
		if err := json.Unmarshal(data, &wrap); err != nil || len(wrap.Values) == 0 {
			lastErr = fmt.Errorf("decode uci get values: empty/invalid response (%d bytes)", len(data))
			continue
		}
		sec, err := decodeSection(wrap.Values)
		if err != nil {
			lastErr = err
			continue
		}
		return sec, true, nil
	}
	return nil, false, lastErr
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
