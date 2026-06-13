<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
# terraform-provider-openwrt-ubus

A native **ubus-over-HTTP** OpenTofu/Terraform provider for OpenWrt.

OpenWrt's configuration bus is **ubus** (served by `rpcd` + `uhttpd-mod-ubus`) —
the default, maintained management API on 24.10+. Every other Terraform provider
for OpenWrt (`joneshf`, `Foxboron`, `ORFops`) drives **LuCI RPC**
(`luci-mod-rpc`/`rpcd-mod-luci`), which ships *off by default* and is on a
deprecation path. This provider talks to ubus directly — nothing extra to
install on the device.

## Coverage: complete by construction

Rather than ship one typed resource per feature (and silently miss whatever
isn't modeled), this provider is **generic over the bus**, so it covers
*everything* ubus exposes — including features added in future OpenWrt releases,
with zero provider changes:

| Surface | What it covers | Resource | Data source |
|---|---|---|---|
| **All persistent config** (UCI) — network, firewall, dhcp, wireless, system, dropbear, uhttpd, … | every uci section, named or anonymous, scalar + list options | `openwrt_uci_section` | `openwrt_uci_section` |
| **All imperative / runtime ops** — service control, system actions, status reads (`system.board`, `network.interface.dump`, `iwinfo`, …) | any `object.method` with JSON args | `openwrt_ubus_call` | `openwrt_ubus_call` |

UCI *is* OpenWrt's config model; ubus is how you reach it (`uci.get/add/set/
delete/commit/reload_config`). So `openwrt_uci_section` is the declarative
workhorse, and `openwrt_ubus_call` is the imperative escape hatch. Together they
span the entire bus. Typed convenience resources (e.g. `openwrt_interface`) may
be layered on later as **ergonomics** — they add sugar, not coverage.

Because `openwrt_uci_section` implements `ImportState`, existing devices import
to **0-diff** — the thing the shell/`shell_script` adapter structurally cannot do.

## Usage

```hcl
terraform {
  required_providers {
    openwrt-ubus = {
      source = "jamesonrgrieve/openwrt-ubus"
    }
  }
}

provider "openwrt-ubus" {
  host     = "192.168.8.98"
  username = "root"
  password = var.openwrt_password # from OpenBao at apply time
  # scheme   = "https"  (default)
  # insecure = true     (default; OpenWrt self-signed cert)
}

# A named section (network.lan)
resource "openwrt_uci_section" "lan" {
  config = "network"
  type   = "interface"
  name   = "lan"
  options = {
    proto   = "static"
    ipaddr  = "192.168.1.1"
    netmask = "255.255.255.0"
  }
}

# A DSA bridge-vlan (anonymous section; server assigns the id)
resource "openwrt_uci_section" "vlan_ai_gen" {
  config = "network"
  type   = "bridge-vlan"
  options = {
    device = "br-lan"
    vlan   = "5"
  }
  lists = {
    ports = ["lan1", "lan2:t"]
  }
}

# Reload a service after config changes
resource "openwrt_ubus_call" "reload_network" {
  object = "service"
  method = "restart"
  params = jsonencode({ name = "network" })
  triggers = {
    lan = openwrt_uci_section.lan.id
  }
}
```

Import an existing section to reach 0-diff:

```sh
tofu import 'openwrt_uci_section.lan' 'network.lan'
```

## Local development

No registry round-trip — build and point OpenTofu at the binary via
`dev_overrides`:

```sh
make install   # builds + installs to ~/.local/bin
```

`~/.config/openwrt-ubus.tfrc`:

```hcl
provider_installation {
  dev_overrides { "jamesonrgrieve/openwrt-ubus" = "/home/<you>/.local/bin" }
  direct {}
}
```

```sh
export TF_CLI_CONFIG_FILE=~/.config/openwrt-ubus.tfrc
tofu plan   # uses the local build; dev_overrides skips the lock file
```

## License

AGPL-3.0-or-later. See [LICENSE](LICENSE).
