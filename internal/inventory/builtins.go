package inventory

// This file exists purely to trigger the built-in device type packages'
// own init() registration (see devices/cisco/router.go,
// devices/cisco/switch.go, devices/linux/server.go,
// devices/windows/server.go, devices/aws/account.go,
// devices/catalyst/center.go, devices/container/host.go,
// devices/console/device.go) into the shared
// record.Types registry.
// factory.go itself no longer imports either package by name: a new
// in-tree device type is added by writing its package and adding one
// blank import here, never by editing NewItemFactory's logic.
//
// One import covers every device type in a vendor package, so
// devices/cisco's entry now registers both cisco_router and cisco_switch.
import (
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/aws"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/catalyst"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/cisco"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/console"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/container"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/linux"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/windows"
)
