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

General Go/provider standards (generic-over-the-bus, import-to-0-diff,
secrets-from-provider-block): see `/home/jameson/source/ai-prompts/go.md` §8.

Repo-specific application of those tenets:

- The bus-generic resources here are `openwrt_uci_section` (expresses any uci
  config) and `openwrt_ubus_call` (invokes any ubus method). This is *fuller*
  coverage than a fixed typed set and survives new OpenWrt features with no code
  change. Typed resources, if added, are ergonomic sugar layered on top — never
  the only path to a feature.
- This provider replaces the `net/routers` shell/`shell_script` OpenWrt adapter,
  whose resources cannot be imported and so can never reach 0-diff — hence the
  import-to-0-diff discipline matters here specifically.

## 3. Layout

General provider layout (provider-entry / `internal/<transport>` /
`internal/provider` with `*_resource.go`+`*_data_source.go` pairs / `examples/`,
and keeping the transport free of terraform-framework imports): see
`/home/jameson/source/ai-prompts/go.md` §1/§8.

Repo-specific concrete files (registry address `jamesonrgrieve/openwrt-ubus`):

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

General Go/provider conventions (SPDX-per-file, gofmt/vet gating,
register-in-`provider.go` + add-example + unit-test, branch-on-named-consts):
see `/home/jameson/source/ai-prompts/go.md` §9/§3/§5/§8/§2.

Repo-specific: ubus status codes live in the `internal/ubus` package.

## 5. Pre-commit gate

`make check` (= `tidy` + `gofmt` + `vet` + `test` + `build`, mirrored in CI;
`--no-verify` needs explicit authorization): see
`/home/jameson/source/ai-prompts/go.md` §10.

## 6. Integration with `net/routers`

Consumed by `../tofu/opentofu/net/routers` via `required_providers` +
`provider "openwrt-ubus"` (per-device `for_each`, like the opnsense provider).
The `modules/prosumer/openwrt` module emits `openwrt_uci_section` resources in
place of `shell_script`. Onboard a device by importing its sections to 0-diff —
never a blind first-apply against a live box.
