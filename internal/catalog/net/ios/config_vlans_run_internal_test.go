// Package ios: net.ios.config's run on a switch that keeps its VLANs in a
// VLAN database, outside its running configuration (VTP server mode, IOL's
// default): what the lines did to the database is reported and undone. The
// double's show vlan brief answers in the form SW-A answered it in the CML
// lab on 2026-11-17.
package ios

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// vtpSwitch is configDevice with a VLAN database: vlan, name, state and no
// vlan lines change the database and never the running configuration, and
// show vlan brief prints it. noVLANs makes it a router refusing the command.
type vtpSwitch struct {
	*configDevice
	vlans   map[int]vlan
	noVLANs bool
}

// newVTPSwitch is a switch with VLAN 1, VLAN 50 (host1-segment) and the
// four legacy VLANs, as SW-A had them.
func newVTPSwitch() *vtpSwitch {
	return &vtpSwitch{configDevice: newConfigDevice(), vlans: map[int]vlan{
		1: {"default", "active"}, 50: {"host1-segment", "active"},
		1002: {"fddi-default", "unsupport"}, 1003: {"token-ring-default", "unsupport"},
		1004: {"fddinet-default", "unsupport"}, 1005: {"trnet-default", "unsupport"},
	}}
}

func (s *vtpSwitch) open(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any, string) (iosSession, error) {
	return s, nil
}

// Command answers show vlan brief from the database, and everything else
// as configDevice does.
func (s *vtpSwitch) Command(ctx context.Context, line string) (string, error) {
	if line != "show vlan brief" {
		return s.configDevice.Command(ctx, line)
	}
	s.commands = append(s.commands, line)
	if s.noVLANs {
		return "                ^\r\n% Invalid input detected at '^' marker.\r\n", nil
	}
	var b strings.Builder
	b.WriteString("\r\nVLAN Name                             Status    Ports\r\n---- -------------------------------- --------- -------------------------------\r\n")
	for _, id := range slices.Sorted(maps.Keys(s.vlans)) {
		v := s.vlans[id]
		status := v.state
		switch status {
		case "unsupport":
			status = "act/unsup"
		case "suspend":
			status = "suspended"
		}
		fmt.Fprintf(&b, "%-4d %-32s %-9s \r\n", id, v.name, status)
	}
	return b.String(), nil
}

// ConfigWith applies lines to the database and the configuration.
func (s *vtpSwitch) ConfigWith(ctx context.Context, lines []string, _ netcli.ConfigOptions) (netcli.ConfigResult, error) {
	var rest []string
	current := -1
	for _, raw := range lines {
		l := strings.TrimSpace(raw)
		switch {
		case !s.noVLANs && vlanLine.MatchString(l):
			current, _ = strconv.Atoi(strings.Fields(l)[1])
			if _, ok := s.vlans[current]; !ok {
				s.vlans[current] = vlan{fmt.Sprintf("VLAN%04d", current), "active"}
			}
		case !s.noVLANs && strings.HasPrefix(l, "no vlan "):
			n, _ := strconv.Atoi(strings.Fields(l)[2])
			delete(s.vlans, n)
			current = -1
		case current >= 0 && strings.HasPrefix(l, "name "):
			v := s.vlans[current]
			v.name = strings.TrimPrefix(l, "name ")
			s.vlans[current] = v
		case current >= 0 && l == "no name":
			v := s.vlans[current]
			v.name = fmt.Sprintf("VLAN%04d", current)
			s.vlans[current] = v
		case current >= 0 && strings.HasPrefix(l, "state "):
			v := s.vlans[current]
			v.state = strings.TrimPrefix(l, "state ")
			s.vlans[current] = v
		case current >= 0 && l == "exit":
			current = -1
		default:
			if !vlanDatabaseLine.MatchString(l) {
				current = -1
				rest = append(rest, raw)
			}
		}
	}
	return netcli.ConfigResult{}, s.configDevice.Config(ctx, rest)
}

// runVLANs runs lines on sw and returns the result, the context and the
// error.
func runVLANs(t *testing.T, sw *vtpSwitch, lines []string, extra map[string]any) (bool, *stubContext, error) {
	t.Helper()
	swapOpenSession(t, sw.open)
	params := map[string]any{paramLines: toAny(lines)}
	maps.Copy(params, extra)
	rc := newStubContext()
	res, err := Config(context.Background(), rc, nil, params)
	return res.Changed, rc, err
}

// toAny is lines as a task's list parameter holds them.
func toAny(lines []string) []any {
	out := make([]any, len(lines))
	for i, l := range lines {
		out[i] = l
	}
	return out
}

// TestConfig_AVLANTheLinesMakeIsAChangeWithAnExactUndo: on a VTP server,
// a VLAN the lines make is reported under differences, though the
// configuration reads the same, and its undo removes it; running that undo
// removes it from the double's database. A VLAN renamed is undone by its
// old name, and a VLAN already as written is no change, as the lab's
// "vlan 50" was.
func TestConfig_AVLANTheLinesMakeIsAChangeWithAnExactUndo(t *testing.T) {
	cases := []struct {
		name      string
		lines     []string
		diffs     []string
		undo      []string
		unchanged bool
	}{
		{"made", []string{"vlan 60", " name lab"}, []string{"+vlan 60", " +name lab"}, []string{"no vlan 60"}, false},
		{"made with IOS's own name", []string{"vlan 70"}, []string{"+vlan 70"}, []string{"no vlan 70"}, false},
		// From IOS's own name back to it is "no name", no text of the switch.
		{"renamed from IOS's own name", []string{"vlan 70", "exit", "vlan 80", " name web"}, []string{"+vlan 70", "+vlan 80", " +name web"}, []string{"no vlan 70", "no vlan 80"}, false},
		{"already as written", []string{"vlan 50", " name host1-segment"}, []string{}, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sw := newVTPSwitch()
			changed, rc, err := runVLANs(t, sw, tc.lines, nil)
			if err != nil {
				t.Fatal(err)
			}
			if changed == tc.unchanged {
				t.Errorf("changed %v", changed)
			}
			if got, _ := rc.stats[statDifferences].([]string); !slices.Equal(got, tc.diffs) {
				t.Errorf("differences %q, want %q", got, tc.diffs)
			}
			inv, ok := recordedInverse(t, rc)
			if tc.undo == nil {
				if ok {
					t.Errorf("an unchanged run recorded %+v", inv)
				}
				return
			}
			if !ok || inv.FQCN != configFQCN || inv.Partial || !slices.Equal(inv.Params[paramLines].([]any), toAny(tc.undo)) {
				t.Fatalf("inverse %+v, want net.ios.config %q", inv, tc.undo)
			}
			if len(sw.files) != 0 {
				t.Errorf("the configuration did not change, and the checkpoint was kept: %v", sw.files)
			}
			// The undo puts the database back as it was.
			before := newVTPSwitch().vlans
			if _, _, err := runVLANs(t, sw, tc.undo, nil); err != nil {
				t.Fatal(err)
			}
			if !maps.Equal(sw.vlans, before) {
				t.Errorf("after the undo the VLANs are %v, want %v", sw.vlans, before)
			}
		})
	}
}

// TestConfig_AVLANChangeWithNoExactUndoRecordsNone: a VLAN removed, or
// whose state changed, cannot be made again exactly from show vlan brief,
// and a VLAN renamed would put its old name, text read off the switch, into
// the journal, which keeps none until the owner decides otherwise; so the
// run records no undo and says why.
func TestConfig_AVLANChangeWithNoExactUndoRecordsNone(t *testing.T) {
	for _, tc := range []struct {
		lines []string
		diffs []string
		why   string
	}{
		{[]string{"no vlan 50"}, []string{"-vlan 50", " -name host1-segment"}, "no undo was recorded: VLAN 50 was removed"},
		{[]string{"vlan 50", " state suspend"}, []string{"vlan 50", " -state active", " +state suspend"}, "no undo was recorded: VLAN 50's state changed"},
		{[]string{"vlan 50", " name web"}, []string{"vlan 50", " -name host1-segment", " +name web"}, "no undo was recorded: a VLAN was renamed"},
		// Beside a VLAN made, a rename still records nothing.
		{[]string{"vlan 60", "exit", "vlan 50", " name web"}, []string{"vlan 50", " -name host1-segment", " +name web", "+vlan 60"}, "no undo was recorded: a VLAN was renamed"},
	} {
		changed, rc, err := runVLANs(t, newVTPSwitch(), tc.lines, nil)
		if err != nil || !changed {
			t.Fatalf("%q: changed %v, err %v", tc.lines, changed, err)
		}
		if got, _ := rc.stats[statDifferences].([]string); !slices.Equal(got, tc.diffs) {
			t.Errorf("%q: differences %q, want %q", tc.lines, got, tc.diffs)
		}
		if inv, ok := recordedInverse(t, rc); ok {
			t.Errorf("%q recorded %+v", tc.lines, inv)
		}
		warns, _ := rc.stats[sdk.StatWarnings].([]string)
		if len(warns) != 1 || !strings.HasPrefix(warns[0], tc.why) {
			t.Errorf("%q: warnings %q", tc.lines, warns)
		}
		if strings.Contains(tc.why, "renamed") && !strings.Contains(warns[0], "owner decision still pending") {
			t.Errorf("%q: the warning does not say the decision is pending: %q", tc.lines, warns)
		}
	}
	// Without a checkpoint nothing is journaled, so the rename's undo is shown
	// under undo_lines with its old name.
	_, rc, err := runVLANs(t, newVTPSwitch(), []string{"vlan 50", " name web"}, map[string]any{paramCheckpoint: false})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := rc.stats[statUndoLines].([]string); !slices.Equal(got, []string{"vlan 50", " name host1-segment", "exit"}) {
		t.Errorf("undo_lines %q", got)
	}
}

// TestConfig_AVLANBesideAConfigurationChangeMakesTheUndoPartial: the
// return to the checkpoint leaves the VLAN database as it is, so beside a
// configuration change it is partial, with a warning naming the VLAN.
func TestConfig_AVLANBesideAConfigurationChangeMakesTheUndoPartial(t *testing.T) {
	changed, rc, err := runVLANs(t, newVTPSwitch(), []string{"vlan 60", "interface Ethernet0/1", " description trunk"}, nil)
	if err != nil || !changed {
		t.Fatalf("changed %v, err %v", changed, err)
	}
	inv, ok := recordedInverse(t, rc)
	if !ok || inv.FQCN != replaceFQCN || !inv.Partial {
		t.Fatalf("inverse %+v, want a partial net.ios.replace", inv)
	}
	diffs, _ := rc.stats[statDifferences].([]string)
	if !slices.Contains(diffs, "+vlan 60") || !slices.Contains(diffs, " +description trunk") {
		t.Errorf("differences %q", diffs)
	}
	warns, _ := rc.stats[sdk.StatWarnings].([]string)
	if len(warns) != 1 || !strings.Contains(warns[0], "VLAN 60") || !strings.Contains(warns[0], "partial") {
		t.Errorf("warnings %q", warns)
	}
}

// TestConfig_AVLANTableItCannotReadIsWarnedOf: a device refusing show vlan
// brief has its VLAN effect said to be unknown, and a checkpoint undo of a
// configuration change beside it is partial.
func TestConfig_AVLANTableItCannotReadIsWarnedOf(t *testing.T) {
	sw := newVTPSwitch()
	sw.noVLANs = true
	_, rc, err := runVLANs(t, sw, []string{"vlan 60", "interface Ethernet0/1", " description trunk"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if inv, ok := recordedInverse(t, rc); !ok || !inv.Partial {
		t.Errorf("inverse %+v, want a partial one", inv)
	}
	warns, _ := rc.stats[sdk.StatWarnings].([]string)
	if len(warns) != 1 || !strings.Contains(warns[0], "show vlan brief") || !strings.Contains(warns[0], "not reported") {
		t.Errorf("warnings %q", warns)
	}
}

// TestConfig_WithoutACheckpointAVLANsUndoIsShown: checkpoint false records
// no undo, and the lines that would undo a VLAN made are shown under
// undo_lines with the configuration's.
func TestConfig_WithoutACheckpointAVLANsUndoIsShown(t *testing.T) {
	changed, rc, err := runVLANs(t, newVTPSwitch(), []string{"vlan 60"}, map[string]any{paramCheckpoint: false})
	if err != nil || !changed {
		t.Fatalf("changed %v, err %v", changed, err)
	}
	if got, _ := rc.stats[statUndoLines].([]string); !slices.Equal(got, []string{"no vlan 60"}) {
		t.Errorf("undo_lines %q", got)
	}
	if _, ok := recordedInverse(t, rc); ok {
		t.Error("checkpoint false recorded an undo")
	}
	// A VLAN removed has no such lines, and says why.
	_, rc, err = runVLANs(t, newVTPSwitch(), []string{"no vlan 50"}, map[string]any{paramCheckpoint: false})
	if err != nil {
		t.Fatal(err)
	}
	if warns, _ := rc.stats[sdk.StatWarnings].([]string); len(warns) != 1 || !strings.Contains(warns[0], "VLAN 50 was removed") {
		t.Errorf("warnings %q", warns)
	}
}

// TestConfigUndoCall_AVLANLineMayHaveNoExactUndo: validation is told a
// vlan line's undo is decided by the run.
func TestConfigUndoCall_AVLANLineMayHaveNoExactUndo(t *testing.T) {
	if why := configUndoCall(map[string]any{paramLines: []any{"vlan 60"}}); !strings.Contains(why, "VLAN database") {
		t.Errorf("configUndoCall = %q", why)
	}
	if why := configUndoCall(map[string]any{paramLines: []any{"vlan internal allocation policy ascending"}}); why != "" {
		t.Errorf("a vlan command of its own: %q", why)
	}
}
