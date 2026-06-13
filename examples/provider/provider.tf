# SPDX-License-Identifier: AGPL-3.0-or-later
terraform {
  required_providers {
    openwrt-ubus = {
      source = "jamesonrgrieve/openwrt-ubus"
    }
  }
}

variable "openwrt_password" {
  type      = string
  sensitive = true
}

provider "openwrt-ubus" {
  host     = "192.168.8.98"
  username = "root"
  password = var.openwrt_password
}

# Named section.
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

# Anonymous DSA bridge-vlan (server assigns the section id).
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

# Imperative: restart a service after config changes.
resource "openwrt_ubus_call" "reload_network" {
  object = "service"
  method = "restart"
  params = jsonencode({ name = "network" })
  triggers = {
    lan  = openwrt_uci_section.lan.id
    vlan = openwrt_uci_section.vlan_ai_gen.id
  }
}

# Read-only runtime state.
data "openwrt_ubus_call" "board" {
  object = "system"
  method = "board"
}

output "board" {
  value = jsondecode(data.openwrt_ubus_call.board.result)
}
