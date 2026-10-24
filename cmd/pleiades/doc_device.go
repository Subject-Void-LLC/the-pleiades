// pleiades doc <device type>: what a device type reads from its record,
// how its devices start, and what onboarding may grant it, so a person
// adding a device learns its properties from the tool rather than from the
// error a wrong one gives.
package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
)

// printDeviceType prints deviceType's page, or reports false when no such
// type is registered.
func printDeviceType(deviceType string) bool {
	if _, ok := record.LookupType(deviceType); !ok {
		return false
	}
	fmt.Printf("Device type: %s\n\n", deviceType)
	if record.IsOnboardedType(deviceType) {
		fmt.Println("Starts:      discovered, which runs nothing; pleiades onboard <name> proves what the device can do and makes it active")
	} else {
		fmt.Println("Starts:      active")
	}
	if record.UsesOwnCredential(deviceType) {
		fmt.Println("Credential:  its own only (pleiades add-credential <name> ...); a template's machine credential never reaches it")
	}
	props, _ := record.DispatchProperties(deviceType)
	props = slices.Clone(props)
	slices.Sort(props)
	fmt.Println("\nProperties its record is read for (pleiades add-host <name> --type " + deviceType + " --set key=value, or set-host):")
	if len(props) == 0 {
		fmt.Println("  none beyond its name")
	}
	for _, p := range props {
		fmt.Printf("  %s\n", p)
	}
	if grants := record.Discoverable(deviceType); len(grants) > 0 {
		names := make([]string, len(grants))
		for i, g := range grants {
			names[i] = string(g)
		}
		fmt.Printf("\nOnboarding may grant: %s\n", strings.Join(names, ", "))
	}
	fmt.Println("\nEvery type and its capabilities: docs/reference/devices.md.")
	return true
}
