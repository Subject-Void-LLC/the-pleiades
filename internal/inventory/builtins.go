package inventory

// This file exists purely to trigger the built-in device type packages'
// own init() registration (see devices/cisco/router.go,
// devices/linux/server.go, devices/windows/server.go,
// devices/aws/account.go) into the shared record.Types registry.
// factory.go itself no longer imports either package by name: a new
// in-tree device type is added by writing its package and adding one
// blank import here, never by editing NewItemFactory's logic.
import (
	_ "github.com/SubjectVoidLLC/the-pleiades/internal/inventory/devices/aws"
	_ "github.com/SubjectVoidLLC/the-pleiades/internal/inventory/devices/cisco"
	_ "github.com/SubjectVoidLLC/the-pleiades/internal/inventory/devices/linux"
	_ "github.com/SubjectVoidLLC/the-pleiades/internal/inventory/devices/windows"
)
