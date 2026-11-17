// Package ios: the VLANs a switch keeps out of its running configuration.
// An IOS switch in VTP server or client mode (IOL's default) keeps VLANs 1
// to 1005 in its VLAN database, not in show running-config, so a check that
// read the configuration alone predicted "+vlan 50" for a VLAN that existed
// (measured on IOL L2 in the CML lab, 2026-11-17), and a run, deciding from
// the configuration's differences, would report a VLAN it made unchanged.
// Both read the VLAN table from show vlan brief, parsed by pkg/iosparse's
// ShowVlan (genieparser's), whose first table that output is.
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
	"github.com/Subject-Void-LLC/the-pleiades/pkg/iosparse"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// vlanLine is a line that enters one VLAN by its number, "vlan 50". A list
// or a range ("vlan 10,20", "vlan 10-20") is not read: it is predicted from
// the configuration alone, which errs toward change.
var vlanLine = regexp.MustCompile(`^vlan ([0-9]{1,4})$`)

// vlanDatabaseLine is any line that makes or removes VLANs in the database:
// "vlan 50", "vlan 10,20-30", "no vlan 50". "vlan internal allocation
// policy" and "vlan configuration 50" are commands of their own, not these.
var vlanDatabaseLine = regexp.MustCompile(`^(?:no\s+)?vlan\s+[0-9]`)

// fixedVLANs are the VLANs whose names IOS sets and no line changes: the
// default VLAN and the four legacy media VLANs.
var fixedVLANs = map[int]bool{1: true, 1002: true, 1003: true, 1004: true, 1005: true}

// vlan is one row of the VLAN table: its name and state ("active",
// "suspend", or genieparser's "unsupport" for act/unsup).
type vlan struct {
	name, state string
}

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

// touchesVLANs reports whether any of lines makes or removes VLANs.
func touchesVLANs(lines []string) bool {
	return slices.ContainsFunc(lines, func(l string) bool { return vlanDatabaseLine.MatchString(strings.TrimSpace(l)) })
}

// readVLANTable reads show vlan brief. A device that refuses the command (a
// router), or output ShowVlan finds no VLAN in, gives a nil table and the
// reason, for a warning; only a session that fails is an error.
func readVLANTable(ctx context.Context, session iosSession) (map[int]vlan, string, error) {
	out, err := show(ctx, session, "show vlan brief")
	var refused *errRefused
	if errors.As(err, &refused) {
		return nil, refused.Error(), nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("reading the VLANs: %w", err)
	}
	vlans, err := parseVLANs(out)
	if err != nil {
		return nil, fmt.Sprintf("show vlan brief did not read as a VLAN table: %v", err), nil
	}
	return vlans, "", nil
}

// parseVLANs reads show vlan brief's table through pkg/iosparse's ShowVlan:
// each VLAN by number, with its name and state.
func parseVLANs(out string) (map[int]vlan, error) {
	_, parsed, err := iosparse.Parse("show vlan", out)
	if err != nil {
		return nil, err
	}
	rows, _ := parsed["vlans"].(map[string]any)
	vlans := make(map[int]vlan, len(rows))
	for id, v := range rows {
		n, err := strconv.Atoi(id)
		if err != nil {
			continue
		}
		row, _ := v.(map[string]any)
		name, _ := row["name"].(string)
		state, _ := row["state"].(string)
		vlans[n] = vlan{name: name, state: state}
	}
	return vlans, nil
}

// readVLANs is the switch's VLAN table for a check, when lines name a VLAN
// the running configuration lacks; nil when they name none. A device whose
// table cannot be read gives nil and a warning, and the check predicts from
// the configuration alone.
func readVLANs(ctx context.Context, rc sdk.RunbookContext, session iosSession, running string, lines []string) (map[int]vlan, error) {
	if len(vlansNamed(iosconfig.Parse(running), lines)) == 0 {
		return nil, nil
	}
	vlans, why, err := readVLANTable(ctx, session)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", configFQCN, err)
	}
	if vlans == nil {
		return nil, sdk.RecordWarnings(rc, []string{why + ", so a vlan line the running configuration lacks is predicted to change"})
	}
	return vlans, nil
}

// defaultName reports whether name is the one IOS gives VLAN n itself
// (VLAN0050), or n's name is fixed: no name line sets it.
func defaultName(n int, name string) bool {
	return fixedVLANs[n] || name == fmt.Sprintf("VLAN%04d", n)
}

// withVLANs is roots with a block for each VLAN of vlans that roots holds
// none for: "vlan N", and under it "name X" unless X is the name IOS gives
// the VLAN itself. roots is not changed.
func withVLANs(roots []*iosconfig.Node, vlans map[int]vlan) []*iosconfig.Node {
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
		if v := vlans[n]; !defaultName(n, v.name) {
			node.Children = []*iosconfig.Node{{Line: "name " + v.name}}
		}
		out = append(out, node)
	}
	return out
}
