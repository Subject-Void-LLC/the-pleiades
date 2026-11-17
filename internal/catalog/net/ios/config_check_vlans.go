// Package ios: the VLANs a switch keeps out of its running configuration.
// An IOS switch in VTP server or client mode (IOL's default) keeps VLANs 1
// to 1005 in its VLAN database, not in show running-config, so a check that
// read the configuration alone predicted "+vlan 50" for a VLAN that existed
// (measured on IOL L2 in the CML lab, 2026-11-17). For a line naming one
// VLAN the configuration lacks, the check reads show vlan brief and takes
// each VLAN it lists as a block the configuration holds, with its name.
package ios

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/iosconfig"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// vlanLine is a line that enters one VLAN by its number, "vlan 50". A list
// or a range ("vlan 10,20", "vlan 10-20") is not read: it is predicted from
// the configuration alone, which errs toward change.
var vlanLine = regexp.MustCompile(`^vlan ([0-9]{1,4})$`)

// vlanRow is one line of show vlan brief: the number, the name and a
// status, then the ports (which may wrap onto lines of their own).
var vlanRow = regexp.MustCompile(`^([0-9]{1,4})\s+(\S+)\s+(active|suspended|act/lshut|sus/lshut|act/ishut|sus/ishut|act/unsup)\b`)

// fixedVLANs are the VLANs whose names IOS sets and no line changes: the
// default VLAN and the four legacy media VLANs.
var fixedVLANs = map[int]bool{1: true, 1002: true, 1003: true, 1004: true, 1005: true}

// vlansNamed returns the VLAN numbers lines enter that roots, the running
// configuration, holds no block for.
func vlansNamed(roots []*iosconfig.Node, lines []string) []int {
	held := map[string]bool{}
	for _, n := range roots {
		held[n.Line] = true
	}
	var out []int
	for _, l := range lines {
		l = strings.TrimSpace(l)
		m := vlanLine.FindStringSubmatch(l)
		if m == nil || held[l] {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		out = append(out, n)
	}
	return out
}

// readVLANs is the switch's VLAN table from show vlan brief, by number,
// when lines name a VLAN the running configuration lacks; nil when they
// name none. A device that refuses show vlan brief (a router) gives nil and
// a warning, and the check predicts from the configuration alone.
func readVLANs(ctx context.Context, rc sdk.RunbookContext, session iosSession, running string, lines []string) (map[int]string, error) {
	if len(vlansNamed(iosconfig.Parse(running), lines)) == 0 {
		return nil, nil
	}
	out, err := show(ctx, session, "show vlan brief")
	var refused *errRefused
	if errors.As(err, &refused) {
		warning := fmt.Sprintf("%s, so a vlan line the running configuration lacks is predicted to change", refused.Error())
		return nil, sdk.RecordWarnings(rc, []string{warning})
	}
	if err != nil {
		return nil, fmt.Errorf("%s: reading the VLANs: %w", configFQCN, err)
	}
	return parseVLANBrief(out), nil
}

// parseVLANBrief reads show vlan brief's rows into names by number. A line
// that is not a row (the header, the rule under it, a wrapped port list) is
// passed over.
func parseVLANBrief(out string) map[int]string {
	vlans := map[int]string{}
	for line := range strings.SplitSeq(out, "\n") {
		m := vlanRow.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		vlans[n] = m[2]
	}
	return vlans
}

// withVLANs is roots with a block for each VLAN of vlans that roots holds
// none for: "vlan N", and under it "name X" unless X is the name IOS gives
// the VLAN itself (VLAN0050) or the VLAN is one whose name is fixed. roots
// is not changed.
func withVLANs(roots []*iosconfig.Node, vlans map[int]string) []*iosconfig.Node {
	if len(vlans) == 0 {
		return roots
	}
	held := map[string]bool{}
	for _, n := range roots {
		held[n.Line] = true
	}
	out := append([]*iosconfig.Node(nil), roots...)
	// In order, so the blocks added are the same on every run.
	for _, n := range slices.Sorted(maps.Keys(vlans)) {
		line := fmt.Sprintf("vlan %d", n)
		if held[line] {
			continue
		}
		node := &iosconfig.Node{Line: line}
		if name := vlans[n]; !fixedVLANs[n] && name != fmt.Sprintf("VLAN%04d", n) {
			node.Children = []*iosconfig.Node{{Line: "name " + name}}
		}
		out = append(out, node)
	}
	return out
}
