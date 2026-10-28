// net.ios.facts: the min subset, what the device is, from "show version"
// and "show inventory".
package ios

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

// Every expression below was written against output captured from a real
// device, not from Cisco's documentation and not from memory of what IOS
// output looks like: a Catalyst 8000V on IOS XE 17.15.04c
// (pkg/netcli/live_probe_test.go's TestLiveIOSFactsShapes) and an IOL XE
// router on 17.15.1 (testdata/facts/iol-xe-17.15.1, through pleiades adhoc).
// That distinction is not pedantry: Phase 86.5's own predecessor spec was
// fabricated from exactly such a memory (FAILURE_PATTERNS.md #202).
var (
	// "Cat8kv uptime is 1 hour, 32 minutes" carries the hostname and the
	// uptime phrase in one line, which is why one expression takes both.
	reUptime = regexp.MustCompile(`(?m)^(\S+) uptime is (.+?)\s*$`)
	// "Cisco IOS XE Software, Version 17.15.04c". Deliberately anchored on
	// the IOS XE line rather than the second, differently-formatted
	// "Cisco IOS Software [IOSXE], ... Version 17.15.4c" line the same
	// output also carries: the two disagree ("17.15.04c" vs "17.15.4c"),
	// and the first is the one the platform reports as its version.
	reVersion = regexp.MustCompile(`(?m)^Cisco IOS XE Software, Version (\S+)\s*$`)
	// "Cisco IOS Software [IOSXE], Linux Software (...), Version 17.15.1,
	// RELEASE SOFTWARE (fc4)": an IOL XE router prints only this line, with
	// no IOS XE line above it, so it is the version there and only there.
	reIOSVersion = regexp.MustCompile(`(?m)^Cisco IOS Software\b.*?, Version ([^\s,]+)`)
	// "cisco C8000V (VXE) processor (revision VXE) with ...". An IOL
	// router's "Linux Unix (i686) processor" names no model, and none is
	// reported for it.
	reModel = regexp.MustCompile(`(?m)^[Cc]isco (\S+) \([^)]*\) processor`)
	// "Processor board ID 9XLWRY40A2V", the fallback when show inventory
	// has no Chassis entry (an IOL router's show inventory prints nothing).
	reBoardID = regexp.MustCompile(`(?m)^Processor board ID (\S+)\s*$`)
	// `System image file is "bootflash:packages.conf"`
	reImage = regexp.MustCompile(`(?m)^System image file is "([^"]*)"`)
	// show inventory pairs a NAME line with a PID/VID/SN line beneath it.
	// SN can be entirely blank on a real device (the F0 module reports
	// "SN:" followed by only spaces), so the capture is deliberately
	// permissive and the caller discards an empty result.
	reInventoryName = regexp.MustCompile(`(?m)^NAME: "([^"]*)"`)
	reInventorySN   = regexp.MustCompile(`(?m)^PID:.*?,\s*VID:.*?,\s*SN:\s*(\S*)\s*$`)
)

// gatherMin reads "show version" and "show inventory" into g.
func gatherMin(ctx context.Context, session iosSession, g *gathered) error {
	version, err := show(ctx, session, "show version")
	if err != nil {
		return err
	}
	inventoryOut, err := show(ctx, session, "show inventory")
	var refused *errRefused
	if errors.As(err, &refused) {
		// A device that will not show its inventory still has a serial in
		// show version; the refusal is said rather than failing the read.
		g.warnings = append(g.warnings, refused.Error()+"; the serial number is show version's Processor board ID")
	} else if err != nil {
		return err
	}
	for k, v := range parseVersion(version) {
		g.values[k] = v
	}
	// show inventory's Chassis serial is the authoritative one; the board
	// ID parsed above stands in only when it is absent.
	if sn := chassisSerial(inventoryOut); sn != "" {
		g.values[factSerialNum] = sn
	}
	return nil
}

// parseVersion pulls what it can out of "show version". A field the
// device does not report is left out of the map entirely rather than set
// to an empty string, so a later condition can tell "this device does not
// say" from "this device says nothing", the same contract
// internal/catalog/facts's own gatherer makes.
func parseVersion(out string) map[string]any {
	facts := map[string]any{}
	if m := reUptime.FindStringSubmatch(out); m != nil {
		facts[factHostname] = m[1]
		facts[factUptime] = m[2]
	}
	if m := reVersion.FindStringSubmatch(out); m != nil {
		facts[factVersion] = m[1]
	} else if m := reIOSVersion.FindStringSubmatch(out); m != nil {
		facts[factVersion] = m[1]
	}
	if m := reModel.FindStringSubmatch(out); m != nil {
		facts[factModel] = m[1]
	}
	if m := reBoardID.FindStringSubmatch(out); m != nil {
		facts[factSerialNum] = m[1]
	}
	if m := reImage.FindStringSubmatch(out); m != nil {
		facts[factImage] = m[1]
	}
	return facts
}

// chassisSerial returns the serial number of the entry named "Chassis"
// in "show inventory" output, or "" when there is none. A real device
// lists several entries, only one of which describes the chassis, and at
// least one of the others can carry an entirely blank serial.
func chassisSerial(out string) string {
	names := reInventoryName.FindAllStringSubmatchIndex(out, -1)
	for i, loc := range names {
		name := out[loc[2]:loc[3]]
		if !strings.EqualFold(name, "Chassis") {
			continue
		}
		end := len(out)
		if i+1 < len(names) {
			end = names[i+1][0]
		}
		if m := reInventorySN.FindStringSubmatch(out[loc[1]:end]); m != nil {
			return strings.TrimSpace(m[1])
		}
	}
	return ""
}
