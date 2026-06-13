// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package ubus is a minimal client for OpenWrt's ubus-over-HTTP JSON-RPC
// endpoint (served by rpcd + uhttpd-mod-ubus). It is the native, default
// management bus on OpenWrt 24.10+ — unlike LuCI RPC (luci-mod-rpc), which
// ships off by default and is deprecated.
package ubus

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// nullSession is the all-zero session id used for the unauthenticated
// session.login call.
const nullSession = "00000000000000000000000000000000"

// ubus call status codes (ubus_msg_status). Only the subset we branch on.
const (
	StatusOK               = 0
	StatusInvalidArgument  = 2
	StatusMethodNotFound   = 3
	StatusNotFound         = 4
	StatusNoData           = 5
	StatusPermissionDenied = 6
	StatusTimeout          = 7
)

// StatusError is returned when a ubus call completes transport-wise but the
// call itself reports a non-zero status code.
type StatusError struct {
	Object string
	Method string
	Code   int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("ubus %s.%s failed: status %d (%s)", e.Object, e.Method, e.Code, statusText(e.Code))
}

func statusText(code int) string {
	switch code {
	case StatusOK:
		return "ok"
	case StatusInvalidArgument:
		return "invalid argument"
	case StatusMethodNotFound:
		return "method not found"
	case StatusNotFound:
		return "not found"
	case StatusNoData:
		return "no data"
	case StatusPermissionDenied:
		return "permission denied"
	case StatusTimeout:
		return "timeout"
	default:
		return "unknown"
	}
}

// IsNotFound reports whether err is a ubus StatusError with a not-found code.
func IsNotFound(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Code == StatusNotFound
}

// Config configures a Client.
type Config struct {
	// Endpoint is the full URL to the ubus endpoint, e.g.
	// https://192.168.1.1/ubus.
	Endpoint string
	Username string
	Password string
	Insecure bool
	Timeout  time.Duration
}

// Client is a session-reusing ubus-over-HTTP JSON-RPC client. It is safe for
// concurrent use.
type Client struct {
	endpoint string
	username string
	password string
	http     *http.Client

	mu      sync.Mutex
	session string
	id      int

	// writeMu serializes whole mutation sequences (add/set/delete + commit +
	// reload). uci over ubus shares one staging area per config, so concurrent
	// resource operations that each commit `network` race and clobber one
	// another (observed: a parallel commit re-wrote a config that still held a
	// just-deleted bridge-vlan, resurrecting it). Create/Update/Delete hold this
	// for their full sequence so only one mutation+commit runs at a time.
	writeMu sync.Mutex

	// callMu serializes every individual ubus HTTP call. This is a precaution
	// for small APs whose uhttpd-mod-ubus has a tiny request backlog: Terraform
	// issues many refresh reads at once, and serializing keeps the device from
	// being overrun. (The code-only `uci get` results we first saw were NOT a
	// concurrency fault — they were stale anonymous `cfgXXXX` ids after a config
	// renumber, now handled by re-resolving anonymous sections by identity.)
	callMu sync.Mutex
}

// LockWrites / UnlockWrites bracket a full mutation sequence so concurrent
// resource operations do not interleave their uci commits.
func (c *Client) LockWrites()   { c.writeMu.Lock() }
func (c *Client) UnlockWrites() { c.writeMu.Unlock() }

// NewClient builds a Client. TLS verification is skipped when cfg.Insecure is
// set (OpenWrt ships self-signed certs by default).
func NewClient(cfg Config) *Client {
	to := cfg.Timeout
	if to == 0 {
		to = 30 * time.Second
	}
	tr := &http.Transport{
		// #nosec G402 -- operator-controlled lab endpoints; OpenWrt uhttpd
		// serves a self-signed cert by default. Verification is opt-in via
		// insecure=false in provider config.
		TLSClientConfig: &tls.Config{InsecureSkipVerify: cfg.Insecure},
	}
	return &Client{
		endpoint: cfg.Endpoint,
		username: cfg.Username,
		password: cfg.Password,
		http:     &http.Client{Timeout: to, Transport: tr},
	}
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("ubus json-rpc error %d: %s", e.Code, e.Message)
}

// rawCall performs a single JSON-RPC "call" with the supplied session id. It
// returns the data element (second element of the ubus result array, may be
// nil) and the ubus status code (first element).
func (c *Client) rawCall(session, object, method string, args any) (json.RawMessage, int, error) {
	c.mu.Lock()
	c.id++
	id := c.id
	c.mu.Unlock()

	if args == nil {
		args = map[string]any{}
	}
	payload, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  "call",
		Params:  []any{session, object, method, args},
	})
	if err != nil {
		return nil, -1, err
	}

	// Serialize every ubus call — the device cannot service concurrent requests
	// reliably (see callMu).
	c.callMu.Lock()
	defer c.callMu.Unlock()

	resp, err := c.http.Post(c.endpoint, "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, -1, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, -1, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, -1, fmt.Errorf("ubus http %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var rr rpcResponse
	if err := json.Unmarshal(body, &rr); err != nil {
		return nil, -1, fmt.Errorf("decode ubus response: %w (body=%s)", err, truncate(string(body), 200))
	}
	if rr.Error != nil {
		return nil, -1, rr.Error
	}
	// result is [code] or [code, data].
	var arr []json.RawMessage
	if err := json.Unmarshal(rr.Result, &arr); err != nil {
		return nil, -1, fmt.Errorf("ubus result not an array: %w", err)
	}
	if len(arr) == 0 {
		return nil, -1, errors.New("ubus result is an empty array")
	}
	var code int
	if err := json.Unmarshal(arr[0], &code); err != nil {
		return nil, -1, fmt.Errorf("parse ubus status code: %w", err)
	}
	var data json.RawMessage
	if len(arr) > 1 {
		data = arr[1]
	}
	return data, code, nil
}

func (c *Client) login() (string, error) {
	data, code, err := c.rawCall(nullSession, "session", "login", map[string]any{
		"username": c.username,
		"password": c.password,
	})
	if err != nil {
		return "", fmt.Errorf("ubus login: %w", err)
	}
	if code != StatusOK {
		return "", fmt.Errorf("ubus login failed: status %d — check credentials and rpcd ACLs", code)
	}
	var out struct {
		Session string `json:"ubus_rpc_session"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("decode ubus login response: %w", err)
	}
	if out.Session == "" {
		return "", errors.New("ubus login returned an empty session id")
	}
	return out.Session, nil
}

func (c *Client) currentSession() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.session
}

func (c *Client) setSession(s string) {
	c.mu.Lock()
	c.session = s
	c.mu.Unlock()
}

func (c *Client) ensureSession() (string, error) {
	if s := c.currentSession(); s != "" {
		return s, nil
	}
	s, err := c.login()
	if err != nil {
		return "", err
	}
	c.setSession(s)
	return s, nil
}

// Call invokes a ubus object/method, transparently logging in and retrying
// once when the session has expired (permission-denied/no-data on a previously
// valid session). A non-zero terminal status is returned as *StatusError.
func (c *Client) Call(object, method string, args any) (json.RawMessage, error) {
	s, err := c.ensureSession()
	if err != nil {
		return nil, err
	}
	data, code, err := c.rawCall(s, object, method, args)
	if err != nil {
		return nil, err
	}
	if code == StatusPermissionDenied || code == StatusNoData {
		// Session may have expired; drop it, re-login once, and retry.
		c.setSession("")
		s, err = c.ensureSession()
		if err != nil {
			return nil, err
		}
		data, code, err = c.rawCall(s, object, method, args)
		if err != nil {
			return nil, err
		}
	}
	if code != StatusOK {
		return data, &StatusError{Object: object, Method: method, Code: code}
	}
	return data, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
