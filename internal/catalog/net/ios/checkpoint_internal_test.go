// Package ios: tests of the checkpoint flow (net.ios.config keeping a
// checkpoint, net.ios.replace returning to it, and the undo lines a device
// with no checkpoint is given) against a device double.
package ios

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/iosconfig"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcli"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// This file proves the checkpoint flow's decisions against a device
// double that keeps a running configuration and files and answers
// show file systems, copy, show archive config differences, configure
// replace and delete the way the CML sandbox's IOS XE 17.15 router
// answered them (2026-10-23). The flow against that real router is
// cmd/pleiades's CML release gate.

// configDevice is a device double holding a running configuration and the
// files on its one disk, unix:.
type configDevice struct {
	running    string
	files      map[string]string
	noDisk     bool // show file systems lists no writable disk
	noArchive  bool // show archive config differences is not a command
	failConfig error
	// keys counts the key-material lines applied, which change no line of
	// the configuration.
	keys int
	// replaceSays is a line configure replace prints beside its own, as
	// IOS prints a routing protocol's notice while a replace runs.
	replaceSays string
	commands    []string
	configCalls [][]string
	closed      bool
}

// iosBase is the double's starting configuration.
const iosBase = "Building configuration...\n\nCurrent configuration : 300 bytes\n!\nhostname R1\n!\nusername admin privilege 15 secret 9 $9$supersecret\n!\ninterface Ethernet0/1\n description spare\n no ip address\n!\nip access-list extended EDGE\n 10 permit tcp any any eq 22\n!\nend\n"

func newConfigDevice() *configDevice {
	return &configDevice{running: iosBase, files: map[string]string{}}
}

func (d *configDevice) open(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any, string) (iosSession, error) {
	return d, nil
}

// read resolves a path show archive config differences names.
func (d *configDevice) read(path string) (string, bool) {
	if path == runningConfig {
		return d.running, true
	}
	text, ok := d.files[path]
	return text, ok
}

// lines flattens a configuration to its lines with their indentation, for
// a difference report.
func lines(config string) []string {
	var out []string
	var walk func(nodes []*iosconfig.Node, depth int)
	walk = func(nodes []*iosconfig.Node, depth int) {
		for _, n := range nodes {
			out = append(out, strings.Repeat(" ", depth)+n.Line)
			walk(n.Children, depth+1)
		}
	}
	walk(iosconfig.Parse(config), 0)
	return out
}

func (d *configDevice) Command(_ context.Context, line string) (string, error) {
	d.commands = append(d.commands, line)
	switch {
	case line == "show file systems":
		if d.noDisk {
			return "File Systems:\r\n\r\n     Size(b)  Free(b)  Type  Flags  Prefixes\r\n  -  -  opaque  rw  system:\r\n", nil
		}
		return "File Systems:\r\n\r\n       Size(b)       Free(b)      Type  Flags  Prefixes\r\n        131072        128972     nvram     rw   nvram:\r\n             -             -    opaque     rw   system:\r\n*   2147479552    2147479552      disk     rw   unix:\r\n", nil
	case line == "show running-config":
		return d.running, nil
	case strings.HasPrefix(line, "show archive config differences "):
		if d.noArchive {
			return "                ^\r\n% Invalid input detected at '^' marker.\r\n", nil
		}
		f := strings.Fields(line)
		from, okFrom := d.read(f[4])
		to, okTo := d.read(f[5])
		if !okFrom || !okTo {
			return "%Error opening " + f[4] + " (No such file or directory)\r\n", nil
		}
		a, b := lines(from), lines(to)
		var diff []string
		for _, l := range b {
			if !slices.Contains(a, l) {
				diff = append(diff, indentMark(l, "+"))
			}
		}
		for _, l := range a {
			if !slices.Contains(b, l) {
				diff = append(diff, indentMark(l, "-"))
			}
		}
		if len(diff) == 0 {
			return "!Contextual Config Diffs:\r\n!No changes were found\r\n", nil
		}
		return "!Contextual Config Diffs:\r\n" + strings.Join(diff, "\r\n") + "\r\n", nil
	case strings.HasPrefix(line, "configure replace "):
		path := strings.Fields(line)[2]
		text, ok := d.files[path]
		if !ok {
			return "%Error opening " + path + " (No such file or directory)\r\n", nil
		}
		d.running = text
		return d.replaceSays + "Total number of passes: 1\r\nRollback Done\r\n", nil
	case strings.HasPrefix(line, "delete /force "):
		path := strings.Fields(line)[2]
		if _, ok := d.files[path]; !ok {
			return "%Error deleting " + path + " (No such file or directory)\r\n", nil
		}
		delete(d.files, path)
		return "", nil
	}
	return "", fmt.Errorf("the double does not answer %q", line)
}

// indentMark puts mark between a line's indentation and its text, as IOS
// prints a contextual difference.
func indentMark(line, mark string) string {
	trimmed := strings.TrimLeft(line, " ")
	return line[:len(line)-len(trimmed)] + mark + trimmed
}

func (d *configDevice) Dialog(_ context.Context, line string, _ netcli.DialogOptions) (netcli.DialogResult, error) {
	d.commands = append(d.commands, line)
	path, ok := strings.CutPrefix(line, "copy running-config ")
	if !ok {
		return netcli.DialogResult{}, fmt.Errorf("the double does not answer %q", line)
	}
	if _, there := d.files[path]; there {
		return netcli.DialogResult{}, errors.New("refused the overwrite question")
	}
	d.files[path] = d.running
	return netcli.DialogResult{Output: "Destination filename [x]? \r\n3623 bytes copied in 0.027 secs\r\n"}, nil
}

// ConfigWith applies lines as Config does.
func (d *configDevice) ConfigWith(ctx context.Context, cfg []string, _ netcli.ConfigOptions) (netcli.ConfigResult, error) {
	return netcli.ConfigResult{}, d.Config(ctx, cfg)
}

// Config applies lines as IOS would to the double's configuration: a line
// opening a block (interface, router, ip access-list) starts one, a "no"
// of a block removes it, and each other line lands in the open block,
// replacing a line it supersedes (description, ip address).
func (d *configDevice) Config(_ context.Context, cfg []string) error {
	d.configCalls = append(d.configCalls, cfg)
	if d.failConfig != nil {
		cfg = cfg[:1]
	}
	roots := iosconfig.Parse(d.running)
	var block *iosconfig.Node
	for _, l := range cfg {
		l = strings.TrimSpace(l)
		if keyMaterial.MatchString(l) {
			// IOS keeps key pairs outside the configuration (measured on
			// IOL XE 17.15): the line changes no line of it.
			d.keys++
			continue
		}
		opens := strings.HasPrefix(l, "interface ") || strings.HasPrefix(l, "router ") || strings.HasPrefix(l, "ip access-list ")
		if neg, ok := strings.CutPrefix(l, "no "); ok && (strings.HasPrefix(neg, "interface ") || strings.HasPrefix(neg, "router ")) {
			roots = slices.DeleteFunc(roots, func(n *iosconfig.Node) bool { return n.Line == neg })
			block = nil
			continue
		}
		if opens {
			block = nil
			for _, n := range roots {
				if n.Line == l {
					block = n
				}
			}
			if block == nil {
				block = &iosconfig.Node{Line: l}
				roots = append(roots, block)
			}
			continue
		}
		siblings := &roots
		if block != nil {
			siblings = &block.Children
		}
		*siblings = setLine(*siblings, l)
	}
	d.running = render(roots)
	return d.failConfig
}

// setLine applies one line among siblings: "no X" removes X (or the line
// X begins), a line sharing its first two words with one there replaces
// it, and any other line is added.
func setLine(siblings []*iosconfig.Node, l string) []*iosconfig.Node {
	if x, ok := strings.CutPrefix(l, "no "); ok {
		return slices.DeleteFunc(siblings, func(n *iosconfig.Node) bool { return n.Line == x || strings.HasPrefix(n.Line, x+" ") })
	}
	key := strings.Join(firstWords(l, 2), " ")
	for _, n := range siblings {
		if n.Line == l {
			return siblings
		}
		if strings.Join(firstWords(n.Line, 2), " ") == key || (strings.HasPrefix(l, "description ") && strings.HasPrefix(n.Line, "description ")) {
			n.Line = l
			return siblings
		}
	}
	return append(siblings, &iosconfig.Node{Line: l})
}

func firstWords(l string, n int) []string {
	f := strings.Fields(l)
	return f[:min(n, len(f))]
}

// render writes nodes back as show running-config prints them.
func render(roots []*iosconfig.Node) string {
	var b strings.Builder
	b.WriteString("Building configuration...\n\n")
	var walk func(nodes []*iosconfig.Node, depth int)
	walk = func(nodes []*iosconfig.Node, depth int) {
		for _, n := range nodes {
			b.WriteString(strings.Repeat(" ", depth) + n.Line + "\n")
			walk(n.Children, depth+1)
			if depth == 0 {
				b.WriteString("!\n")
			}
		}
	}
	walk(roots, 0)
	b.WriteString("end\n")
	return b.String()
}

func (d *configDevice) Close() error {
	d.closed = true
	return nil
}

// recordedInverse returns the undo a run recorded, through the stat
// sdk.RecordInverse writes.
func recordedInverse(t *testing.T, rc *stubContext) (sdk.Inverse, bool) {
	t.Helper()
	raw, ok := rc.stats[sdk.StatInverse]
	if !ok {
		return sdk.Inverse{}, false
	}
	inv, ok := raw.(sdk.Inverse)
	if !ok {
		m, isMap := raw.(map[string]any)
		if !isMap {
			t.Fatalf("the inverse stat is a %T", raw)
		}
		inv.FQCN, _ = m[sdk.InverseFQCNKey].(string)
		inv.Params, _ = m[sdk.InverseParamsKey].(map[string]any)
		inv.KeepOnFailure, _ = m[sdk.InverseKeepOnFailureKey].(bool)
		inv.Partial, _ = m[sdk.InversePartialKey].(bool)
	}
	return inv, true
}

var loopback = map[string]any{paramLines: []any{"interface Loopback198", "description pleiades"}, sdk.ParamInsecureTelnet: true}

// TestConfig_CheckpointsAChangeAndItsUndoReturnsExactly is the whole
// round trip: the change keeps two checkpoints and records an undo naming
// only their paths; running that undo returns the configuration exactly
// and deletes both files.
func TestConfig_CheckpointsAChangeAndItsUndoReturnsExactly(t *testing.T) {
	dev := newConfigDevice()
	swapOpenSession(t, dev.open)
	rc := newStubContext()
	res, err := Config(context.Background(), rc, nil, loopback)
	if err != nil || !res.Changed {
		t.Fatalf("Config = %+v, %v", res, err)
	}
	inv, ok := recordedInverse(t, rc)
	if !ok || inv.FQCN != replaceFQCN || !inv.KeepOnFailure {
		t.Fatalf("undo %+v, want net.ios.replace kept on failure", inv)
	}
	before, _ := inv.Params["checkpoint"].(string)
	after, _ := inv.Params["expect"].(string)
	if !strings.HasPrefix(before, "unix:pleiades-") || after == "" || inv.Params[sdk.ParamInsecureTelnet] != true || len(inv.Params) != 3 {
		t.Fatalf("undo params %v: want the two checkpoint paths and the Telnet opt-in, nothing else", inv.Params)
	}
	if rc.stats[statCheckpoint] != before || dev.files[before] != iosBase || dev.files[after] != dev.running {
		t.Fatalf("checkpoints: stat %v, files %v", rc.stats[statCheckpoint], slices.Collect(maps.Keys(dev.files)))
	}
	if diffs, _ := rc.stats[statDifferences].([]string); !slices.Contains(diffs, "+interface Loopback198") {
		t.Errorf("differences %q, want the added interface", diffs)
	}

	undo := newStubContext()
	res, err = Replace(context.Background(), undo, nil, inv.Params)
	if err != nil || !res.Changed {
		t.Fatalf("Replace = %+v, %v", res, err)
	}
	if dev.running != iosBase {
		t.Errorf("after the undo the configuration is\n%s\nwant it as it was", dev.running)
	}
	if len(dev.files) != 0 {
		t.Errorf("the checkpoints were left behind: %v", slices.Collect(maps.Keys(dev.files)))
	}
	if _, ok := recordedInverse(t, undo); ok {
		t.Error("an undo recorded an undo of its own")
	}
}

// TestConfig_LinesAlreadyThereChangeNothing: the device's own comparison
// says nothing changed, so the task is unchanged, records no undo and
// leaves no checkpoint.
func TestConfig_LinesAlreadyThereChangeNothing(t *testing.T) {
	dev := newConfigDevice()
	swapOpenSession(t, dev.open)
	rc := newStubContext()
	res, err := Config(context.Background(), rc, nil, map[string]any{paramLines: []any{"interface Ethernet0/1", "description spare"}})
	if err != nil || res.Changed {
		t.Fatalf("Config = %+v, %v; want unchanged", res, err)
	}
	if _, ok := recordedInverse(t, rc); ok || len(dev.files) != 0 {
		t.Errorf("an unchanged run recorded an undo or left files: %v", dev.files)
	}
}

// TestReplace_RefusesADeviceChangedSince: configure replace would undo a
// change made after the one being undone, so the replace refuses unless
// forced, and a check reports what it would change without changing it.
func TestReplace_RefusesADeviceChangedSince(t *testing.T) {
	dev := newConfigDevice()
	swapOpenSession(t, dev.open)
	rc := newStubContext()
	if _, err := Config(context.Background(), rc, nil, loopback); err != nil {
		t.Fatal(err)
	}
	inv, _ := recordedInverse(t, rc)
	if err := dev.Config(context.Background(), []string{"hostname core-1"}); err != nil {
		t.Fatal(err)
	}
	changedSince := dev.running

	check := newStubContext()
	res, err := checkReplace(context.Background(), check, nil, map[string]any{"checkpoint": inv.Params["checkpoint"], "expect": inv.Params["expect"], paramReplaceForce: true})
	if err != nil || !res.Changed || dev.running != changedSince || len(dev.files) != 2 {
		t.Fatalf("check = %+v, %v; it must predict a change and touch nothing", res, err)
	}
	if _, err := Replace(context.Background(), newStubContext(), nil, inv.Params); err == nil || !strings.Contains(err.Error(), "has changed since") || !strings.Contains(err.Error(), "hostname core-1") {
		t.Fatalf("Replace = %v, want a refusal naming the later change", err)
	}
	if dev.running != changedSince {
		t.Fatal("a refused replace changed the device")
	}
	forced := maps.Clone(inv.Params)
	forced[paramReplaceForce] = true
	if _, err := Replace(context.Background(), newStubContext(), nil, forced); err != nil || dev.running != iosBase {
		t.Fatalf("forced Replace = %v, running\n%s", err, dev.running)
	}
}

// TestConfig_AFailedChangeKeepsItsUndo: a line the device refused after
// another reached it leaves the first on the device, so the undo stands.
func TestConfig_AFailedChangeKeepsItsUndo(t *testing.T) {
	dev := newConfigDevice()
	dev.failConfig = errors.New(`device rejected "description pleiades"`)
	swapOpenSession(t, dev.open)
	rc := newStubContext()
	if _, err := Config(context.Background(), rc, nil, loopback); err == nil {
		t.Fatal("a refused line did not fail the task")
	}
	inv, ok := recordedInverse(t, rc)
	if !ok || inv.FQCN != replaceFQCN || !inv.KeepOnFailure || inv.Params["expect"] != nil {
		t.Fatalf("undo %+v, want a kept net.ios.replace with no second checkpoint", inv)
	}
	if _, err := Replace(context.Background(), newStubContext(), nil, inv.Params); err != nil || dev.running != iosBase {
		t.Fatalf("undoing the partial change: %v\n%s", err, dev.running)
	}
}

// TestConfig_WithoutACheckpointTheUndoLinesAreShownNotRecorded: a device
// with no disk, or no archive, records no undo; the lines that would undo
// the change are shown, masked, for the task's rollback: list, since lines
// read off a device never reach the journal.
func TestConfig_WithoutACheckpointTheUndoLinesAreShownNotRecorded(t *testing.T) {
	for name, dev := range map[string]*configDevice{
		"no disk":           {running: iosBase, files: map[string]string{}, noDisk: true},
		"no archive":        {running: iosBase, files: map[string]string{}, noArchive: true},
		"checkpoint: false": {running: iosBase, files: map[string]string{}},
	} {
		swapOpenSession(t, dev.open)
		rc := newStubContext()
		params := map[string]any{paramLines: []any{"interface Ethernet0/1", "description uplink", "username admin privilege 15 secret 9 $9$newsecret"}}
		if name == "checkpoint: false" {
			params[paramCheckpoint] = false
		}
		res, err := Config(context.Background(), rc, nil, params)
		if err != nil || !res.Changed {
			t.Fatalf("%s: Config = %+v, %v", name, res, err)
		}
		if inv, ok := recordedInverse(t, rc); ok {
			t.Errorf("%s: an undo was recorded: %+v", name, inv)
		}
		shown, _ := rc.stats[statUndoLines].([]string)
		joined := strings.Join(shown, "\n")
		if !slices.Contains(shown, " description spare") || !slices.Contains(shown, " no description uplink") {
			t.Errorf("%s: undo lines %q", name, shown)
		}
		if strings.Contains(joined, "supersecret") || strings.Contains(joined, "newsecret") || !strings.Contains(joined, netcfg.MaskPrefix) {
			t.Errorf("%s: the undo lines shown are not masked: %q", name, shown)
		}
		if warnings, _ := rc.stats[sdk.StatWarnings].([]string); !strings.Contains(strings.Join(warnings, " "), "rollback: list") || len(dev.files) != 0 {
			t.Errorf("%s: warnings %q, files %v", name, warnings, dev.files)
		}
	}
}

// TestConfig_AChangeWithNoLinesToUndoItWarns: inside an access list no
// undo lines are written, so the change records none and says so.
func TestConfig_AChangeWithNoLinesToUndoItWarns(t *testing.T) {
	dev := &configDevice{running: iosBase, files: map[string]string{}, noArchive: true}
	swapOpenSession(t, dev.open)
	rc := newStubContext()
	res, err := Config(context.Background(), rc, nil, map[string]any{paramLines: []any{"ip access-list extended EDGE", "20 deny ip any any"}})
	if err != nil || !res.Changed {
		t.Fatalf("Config = %+v, %v", res, err)
	}
	if _, ok := recordedInverse(t, rc); ok {
		t.Error("an undo was recorded for a change inside an access list")
	}
	if warnings, _ := rc.stats[sdk.StatWarnings].([]string); len(warnings) < 2 || !strings.Contains(strings.Join(warnings, " "), "rollback: list") {
		t.Errorf("warnings %q, want the missing undo named", warnings)
	}
}

// TestReplace_RefusesAPathThatIsNotAFile: a path is written into commands
// the device runs, so anything but a plain file path is refused before the
// device is reached.
func TestReplace_RefusesAPathThatIsNotAFile(t *testing.T) {
	swapOpenSession(t, func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any, string) (iosSession, error) {
		t.Fatal("the device was reached")
		return nil, nil
	})
	for _, params := range []map[string]any{
		{},
		{"checkpoint": "unix:a.cfg force\nreload"},
		{"checkpoint": "unix:a.cfg", "expect": "a b"},
		{"checkpoint": "nofilesystem.cfg"},
		{"checkpoint": "unix:a.cfg", paramReplaceKeep: "yes"},
	} {
		if _, err := Replace(context.Background(), newStubContext(), nil, params); err == nil {
			t.Errorf("%v: accepted", params)
		}
	}
}

// TestDifferences_MaskWhatTheyShow: a difference line's marker sits against
// its first word, and the secret after it is still masked.
func TestDifferences_MaskWhatTheyShow(t *testing.T) {
	got := maskDifferences([]string{"+username admin privilege 15 secret 9 $9$abc", " -password 7 0822455D0A16", "+interface Loopback1"})
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "$9$abc") || strings.Contains(joined, "0822455D0A16") || got[2] != "+interface Loopback1" || !strings.HasPrefix(got[1], " -password 7 "+netcfg.MaskPrefix) {
		t.Errorf("masked %q", got)
	}
}

func TestPickFileSystem(t *testing.T) {
	router := "File Systems:\n\n     Size(b)     Free(b)      Type  Flags  Prefixes\n*  7194652672  4291928064      disk     rw   bootflash: flash:\n          -           -    opaque     rw   system:\n"
	if fs, err := pickFileSystem(router); err != nil || fs != "bootflash:" {
		t.Errorf("a router's table: %q, %v", fs, err)
	}
	notDefault := "  33554432  1000  flash  rw  flash:\n*  -  -  opaque  rw  system:\n"
	if fs, err := pickFileSystem(notDefault); err != nil || fs != "flash:" {
		t.Errorf("no writable default: %q, %v", fs, err)
	}
	if _, err := pickFileSystem("  -  -  opaque  rw  system:\n  100  1  disk  ro  tar:\n"); !errors.Is(err, errNoCheckpoint) {
		t.Errorf("nothing writable: %v", err)
	}
}

// TestConfig_RefusesAMaskedSecret: undo_lines are masked, and lines pasted
// from them unchanged would set each secret to its placeholder, so a line
// holding one is refused before the device is reached.
func TestConfig_RefusesAMaskedSecret(t *testing.T) {
	swapOpenSession(t, func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any, string) (iosSession, error) {
		t.Fatal("the device was reached")
		return nil, nil
	})
	masked := netcfg.Mask("username admin privilege 15 secret 9 $9$abc")
	_, err := Config(context.Background(), newStubContext(), nil, map[string]any{paramLines: []any{masked}})
	if err == nil || !strings.Contains(err.Error(), "masked secret") {
		t.Fatalf("err = %v, want the masked secret refused", err)
	}
}

// TestConfigUndoCall: a call keeping a checkpoint records an undo; one with
// checkpoint anything but true (false, or a template) records none.
func TestConfigUndoCall(t *testing.T) {
	for params, want := range map[*map[string]any]bool{
		&map[string]any{paramLines: []any{"x"}}:                                     false,
		&map[string]any{paramLines: []any{"x"}, paramCheckpoint: true}:              false,
		&map[string]any{paramLines: []any{"x"}, paramCheckpoint: false}:             true,
		&map[string]any{paramLines: []any{"x"}, paramCheckpoint: "{{ vars.keep }}"}: true,
	} {
		if got := configUndoCall(*params) != ""; got != want {
			t.Errorf("configUndoCall(%v) records none = %v, want %v", *params, got, want)
		}
	}
}

// TestReplace_NeverDeletesAFileItDidNotMake: returning to the saved
// configuration replaces from nvram:startup-config and leaves it there.
func TestReplace_NeverDeletesAFileItDidNotMake(t *testing.T) {
	dev := newConfigDevice()
	swapOpenSession(t, dev.open)
	dev.files["nvram:startup-config"] = iosBase
	dev.files["unix:golden.cfg"] = iosBase
	if err := dev.Config(context.Background(), []string{"hostname core-1"}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"nvram:startup-config", "unix:golden.cfg"} {
		if _, err := Replace(context.Background(), newStubContext(), nil, map[string]any{"checkpoint": path}); err != nil {
			t.Fatalf("Replace %s: %v", path, err)
		}
		if _, kept := dev.files[path]; !kept {
			t.Errorf("Replace deleted %s, a file it did not make", path)
		}
	}
	for _, minted := range []string{"unix:pleiades-0123456789ab.cfg", "flash:pleiades-0123456789ab-after.cfg"} {
		if !mintedCheckpoint.MatchString(minted) {
			t.Errorf("%s is a checkpoint net.ios.config makes", minted)
		}
	}
	for _, other := range []string{"nvram:startup-config", "unix:pleiades-x.cfg", "flash:pleiades-0123456789ab.cfg.bak", "unix:dir/pleiades-0123456789ab.cfg"} {
		if mintedCheckpoint.MatchString(other) {
			t.Errorf("%s would be deleted", other)
		}
	}
}

// TestShownDifferences: an error quotes at most ten difference lines.
func TestShownDifferences(t *testing.T) {
	many := make([]string, 25)
	for i := range many {
		many[i] = fmt.Sprintf("+line %d", i)
	}
	if got := shownDifferences(many); !strings.HasSuffix(got, "+line 9; and 15 more") || strings.Contains(got, "+line 10") {
		t.Errorf("shownDifferences = %q", got)
	}
	if got := shownDifferences(many[:2]); got != "+line 0; +line 1" {
		t.Errorf("shownDifferences of two = %q", got)
	}
}

// TestReplace_ANoticeDuringAReplaceThatMatchesIsAWarning: configure replace
// printing a "% " line still succeeds when the device matches the
// checkpoint afterwards, warning with the line masked; when the device
// still differs, the line is the failure's reason.
func TestReplace_ANoticeDuringAReplaceThatMatchesIsAWarning(t *testing.T) {
	dev := newConfigDevice()
	dev.replaceSays = "% Removing the EIGRP process cleared its key 7 0822455D0A16\r\n"
	swapOpenSession(t, dev.open)
	rc := newStubContext()
	if _, err := Config(context.Background(), rc, nil, loopback); err != nil {
		t.Fatal(err)
	}
	inv, _ := recordedInverse(t, rc)
	undo := newStubContext()
	if res, err := Replace(context.Background(), undo, nil, inv.Params); err != nil || !res.Changed || dev.running != iosBase {
		t.Fatalf("Replace = %+v, %v", res, err)
	}
	warnings, _ := undo.stats[sdk.StatWarnings].([]string)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "% Removing the EIGRP process") || strings.Contains(warnings[0], "0822455D0A16") {
		t.Errorf("warnings %q, want the line, masked", warnings)
	}

	dev = newConfigDevice()
	swapOpenSession(t, dev.open)
	rc = newStubContext()
	if _, err := Config(context.Background(), rc, nil, loopback); err != nil {
		t.Fatal(err)
	}
	inv, _ = recordedInverse(t, rc)
	// A replace that prints its failure and leaves the device as it was.
	before := inv.Params["checkpoint"].(string)
	dev.replaceSays = "% Rollback aborted\r\n"
	stuck := &stuckReplace{configDevice: dev, keep: dev.running}
	swapOpenSession(t, stuck.open)
	if _, err := Replace(context.Background(), newStubContext(), nil, map[string]any{"checkpoint": before}); err == nil || !strings.Contains(err.Error(), "% Rollback aborted") {
		t.Errorf("a replace that changed nothing and complained: %v", err)
	}
}

// stuckReplace is a device whose configure replace prints its notice and
// changes nothing.
type stuckReplace struct {
	*configDevice
	keep string
}

func (s *stuckReplace) open(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any, string) (iosSession, error) {
	return s, nil
}

func (s *stuckReplace) Command(ctx context.Context, line string) (string, error) {
	out, err := s.configDevice.Command(ctx, line)
	if strings.HasPrefix(line, "configure replace ") {
		s.running = s.keep
	}
	return out, err
}

// TestConfig_AKeyChangeReportsChangedAndNoExactUndo: a key line that
// changes no line of the configuration still reports changed, warns that
// no undo returns the key pair, and leaves no checkpoint; one beside a
// configuration change records the configuration's undo as partial; and a
// call holding one is never an exact undo.
func TestConfig_AKeyChangeReportsChangedAndNoExactUndo(t *testing.T) {
	dev := newConfigDevice()
	swapOpenSession(t, dev.open)
	rc := newStubContext()
	res, err := Config(context.Background(), rc, nil, map[string]any{paramLines: []any{"crypto key zeroize rsa OLDKEY"}})
	if err != nil || !res.Changed || dev.keys != 1 {
		t.Fatalf("a key line alone: %+v, %v, keys %d", res, err, dev.keys)
	}
	if _, ok := recordedInverse(t, rc); ok || len(dev.files) != 0 {
		t.Errorf("a key line alone recorded an undo or left a checkpoint: %v", dev.files)
	}
	if w, _ := rc.stats[sdk.StatWarnings].([]string); len(w) != 1 || !strings.Contains(w[0], "no undo returns the key pair") {
		t.Errorf("warnings = %v", rc.stats[sdk.StatWarnings])
	}

	rc = newStubContext()
	if _, err := Config(context.Background(), rc, nil, map[string]any{paramLines: []any{"crypto key generate rsa general-keys label K modulus 2048", "interface Loopback9", "description k"}}); err != nil {
		t.Fatal(err)
	}
	if inv, ok := recordedInverse(t, rc); !ok || !inv.Partial {
		t.Errorf("a key line beside a change: undo %+v, want it partial", inv)
	}
	if configUndoCall(map[string]any{paramLines: []any{" crypto key import rsa K pem terminal"}}) == "" {
		t.Error("a call importing a key pair was taken as exactly undoable")
	}
}

// TestConfig_ARejectionIsMaskedAndBounded: a rejected line is quoted with
// its secret masked, and a long answer is cut.
func TestConfig_ARejectionIsMaskedAndBounded(t *testing.T) {
	dev := newConfigDevice()
	dev.failConfig = errors.New(`device rejected "username u secret 0 hunter2": ` + strings.Repeat("x", 3*maxErrorText))
	swapOpenSession(t, dev.open)
	_, err := Config(context.Background(), newStubContext(), nil, map[string]any{paramLines: []any{"username u secret 0 hunter2"}})
	if err == nil || strings.Contains(err.Error(), "hunter2") || len(err.Error()) > maxErrorText+200 {
		t.Errorf("err = %.200v (%d bytes)", err, len(err.Error()))
	}
}

// TestReadConfigOptions_AnAcceptPatternCannotEscapeItsAnchor: a pattern
// that would close the group around it is refused.
func TestReadConfigOptions_AnAcceptPatternCannotEscapeItsAnchor(t *testing.T) {
	if _, err := readConfigOptions(context.Background(), newStubContext(), map[string]any{paramAccept: []any{"%)|(?:.*"}}); err == nil {
		t.Error("an escaping accept pattern was taken")
	}
}
