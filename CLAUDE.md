# openwrt-ubus — Agent Operating Guide

Repo-specific standards for `openwrt-ubus`, a native ubus-over-HTTP
OpenTofu/Terraform provider for OpenWrt. The workspace-root `../CLAUDE.md`
applies (AGPL for production IaC, SPDX headers, pre-commit gates); this file
adds Go/provider specifics and **cannot relax** anything there.

---

## 1. What this is

A Terraform Plugin Framework provider that manages OpenWrt over **ubus**
(`rpcd` + `uhttpd-mod-ubus`), the native bus — *not* LuCI RPC. It replaces the
`net/routers` shell/`shell_script` OpenWrt adapter, whose resources cannot be
imported and so can never reach 0-diff.

## 2. Design tenets

- **Generic over the bus, not typed-per-feature.** `openwrt_uci_section`
  expresses any uci config; `openwrt_ubus_call` invokes any ubus method. This is
  *fuller* coverage than a fixed typed set and survives new OpenWrt features
  with no code change. Typed resources, if added, are ergonomic sugar layered on
  top — never the only path to a feature.
- **Import to 0-diff is the point.** Every stateful resource implements
  `ImportState`. Drift is scoped to declared keys so co-managed/imported boxes
  don't throw phantom diffs.
- **Secrets never in code.** The device password comes from the provider block
  (injected from OpenBao at apply), never hard-coded.

## 3. Layout

```
main.go                              provider server entry (address jamesonrgrieve/openwrt-ubus)
internal/ubus/        client.go      ubus-over-HTTP JSON-RPC transport (login, call, retry)
                     uci.go          uci get/add/set/delete/commit/reload helpers
internal/provider/   provider.go     provider schema + Configure
                     uci_section_resource.go / _data_source.go
                     ubus_call_resource.go    / _data_source.go
examples/                            runnable HCL
```

## 4. Conventions

- **SPDX header** (`// SPDX-License-Identifier: AGPL-3.0-or-later`) on every Go file.
- `gofmt`-clean, `go vet`-clean — both gate the commit. Run `make check`.
- New resources/data sources: register in `provider.go`, add an `examples/`
  snippet, and a unit test for any non-trivial pure logic (decode/split/diff).
- ubus status codes live in `internal/ubus` — branch on the named consts, not
  magic numbers.
- Keep the transport (`internal/ubus`) free of any terraform-framework imports;
  the provider layer adapts it.

## 5. Pre-commit gate

`make check` = `tidy` + `gofmt` + `vet` + `test` + `build`, all clean. Mirror it
in `.husky`/CI. `--no-verify` requires explicit operator authorization.

## 6. Integration with `net/routers`

Consumed by `../tofu/opentofu/net/routers` via `required_providers` +
`provider "openwrt-ubus"` (per-device `for_each`, like the opnsense provider).
The `modules/prosumer/openwrt` module emits `openwrt_uci_section` resources in
place of `shell_script`. Onboard a device by importing its sections to 0-diff —
never a blind first-apply against a live box.
