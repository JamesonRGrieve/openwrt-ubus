// SPDX-License-Identifier: AGPL-3.0-or-later

// Command terraform-provider-openwrt-ubus is a native ubus-over-HTTP
// OpenTofu/Terraform provider for OpenWrt. It manages OpenWrt configuration via
// rpcd's ubus bus (uci + arbitrary ubus calls), with no dependency on LuCI RPC.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/JamesonRGrieve/openwrt-ubus/internal/provider"
)

// version is overridden at build time via -ldflags.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/jamesonrgrieve/openwrt-ubus",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err.Error())
	}
}
