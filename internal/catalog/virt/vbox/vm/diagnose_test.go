// Tests for the methods that see into a VM with no window
// (virt.vbox.vm.screenshot, log, send_keys and addresses), against the
// model host.
package vm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage/vboxmanagetest"
)

// windowsVM is a running Windows clone at its first-boot screen, its
// host-only adapter given a fixed address and a DHCP lease.
func windowsVM() *vboxmanagetest.VM {
	vm := seededVM(vboxmanage.StateRunning)
	vm.NICs = map[int]vboxmanage.NIC{
		1: {Kind: "nat", MAC: "08002742C655"},
		2: {Kind: "hostonly", MAC: "0800273C99C7", HostOnlyAdapter: defaultHostOnlyAdapter},
	}
	vm.Extra = map[string]string{vboxmanage.ExtraAddress: "192.168.56.30"}
	return vm
}

// leases is VirtualBox's DHCP leases file with one held lease for
// windowsVM's host-only adapter and one expired lease for another.
const leases = `<?xml version="1.0"?>
<Leases version="1.0">
  <Lease mac="08:00:27:3c:99:c7" id="010800273c99c7" network="0.0.0.0" state="acked">
    <Address value="192.168.56.102"/>
    <Time issued="1790536392" expiration="600"/>
  </Lease>
  <Lease mac="08:00:27:09:e4:40" id="0108002709e440" network="0.0.0.0" state="expired">
    <Address value="192.168.56.101"/>
    <Time issued="1790535599" expiration="600"/>
  </Lease>
</Leases>`

func TestTheWindowlessMethodsAreImplemented(t *testing.T) {
	for _, fqcn := range []string{"virt.vbox.vm.screenshot", "virt.vbox.vm.log", "virt.vbox.vm.send_keys", "virt.vbox.vm.addresses"} {
		if _, err := call(t, fqcn, false, newRecorder(), map[string]any{"name": "a b", "dest": "x", "keys": "a"}); err == nil {
			t.Errorf("%s took a bad VM name", fqcn)
		}
	}
}

// projectRecorder is a recorder that writes project files through the
// command line's own sdk.ProjectFileWriter (internal/engine) over a project
// directory, so a test meets every refusal a run meets.
type projectRecorder struct {
	*recorder
	sdk.ProjectFileWriter
}

// newProjectRecorder returns a projectRecorder over dir.
func newProjectRecorder(t *testing.T, dir string) *projectRecorder {
	t.Helper()
	rc, err := engine.WithProjectFiles(engine.NewDeviceRunbookContext, dir)(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return &projectRecorder{recorder: newRecorder(), ProjectFileWriter: rc.(sdk.ProjectFileWriter)}
}

func TestScreenshot(t *testing.T) {
	model := vboxmanagetest.New(windowsVM())
	onModel(t, model)
	project := t.TempDir()
	dest := filepath.Join(project, "shots", "win-lab.png")
	params := map[string]any{"name": "win-lab", "dest": "shots/win-lab.png"}
	check := newProjectRecorder(t, project)
	if result, err := call(t, "virt.vbox.vm.screenshot", true, check, params); err != nil || !result.Changed || len(changes(model)) != 0 {
		t.Fatalf("check: %+v, %v, %v", result, err, changes(model))
	}
	if _, err := os.Stat(filepath.Dir(dest)); err == nil {
		t.Error("a check made the picture's folder")
	}
	rc := newProjectRecorder(t, project)
	if result, err := call(t, "virt.vbox.vm.screenshot", false, rc, params); err != nil || !result.Changed {
		t.Fatalf("%+v, %v", result, err)
	}
	picture, err := os.ReadFile(dest)
	if err != nil || !strings.HasPrefix(string(picture), pngSignature) || rc.stats[statWidth] != 1024 || rc.stats[statHeight] != 768 || rc.stats[statBytes] != len(picture) || rc.stats[statDest] != "shots/win-lab.png" {
		t.Errorf("wrote %d bytes, stats %v, %v", len(picture), rc.stats, err)
	}
	// Windows keeps no permission bits: Go reports 0666 for any file there.
	if info, _ := os.Stat(dest); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("the picture is %v, not the owner's alone", info.Mode().Perm())
	}
	if _, left := model.Files[`G:\PleiadesLab\win-lab\`+screenFile]; left {
		t.Error("the host kept a copy")
	}
}

// TestScreenshot_DestStaysInTheProject: dest is a file in the project, and
// nothing else. An absolute path, one climbing out of the project, one
// through a symbolic link, the project's .pleiades, a name that is not a
// .png, and a run with no project at all (a Runner's) are each refused
// before the host is asked for a picture, in a check and in a run, and
// nothing is written outside the project. Until 2026-11-13 dest was any path
// on the machine running the task, written with os.WriteFile
// (FAILURE_PATTERNS 1237): the absolute path below was written.
func TestScreenshot_DestStaysInTheProject(t *testing.T) {
	project := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(project, "elsewhere")); err != nil {
		t.Skipf("no symbolic link here: %v", err)
	}
	for why, dest := range map[string]string{
		"an absolute path":        filepath.Join(outside, "x.png"),
		"a climb out":             "../" + filepath.Base(outside) + "/x.png",
		"a path through a link":   "elsewhere/x.png",
		"the project's .pleiades": ".pleiades/x.png",
		"a runbook, not a png":    "runbooks/lab.yaml",
	} {
		for _, check := range []bool{true, false} {
			model := vboxmanagetest.New(windowsVM())
			onModel(t, model)
			if _, err := call(t, "virt.vbox.vm.screenshot", check, newProjectRecorder(t, project), map[string]any{"name": "win-lab", "dest": dest}); err == nil {
				t.Errorf("%s (%s), check %v: accepted", why, dest, check)
			}
			for _, c := range model.Calls() {
				if strings.Contains(c, "screenshotpng") {
					t.Errorf("%s, check %v: the host was asked for a picture", why, check)
				}
			}
		}
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("something was written outside the project: %v", entries)
	}
	onModel(t, vboxmanagetest.New(windowsVM()))
	if _, err := call(t, "virt.vbox.vm.screenshot", false, newRecorder(), map[string]any{"name": "win-lab", "dest": "shots/x.png"}); !errors.Is(err, sdk.ErrNoProjectFiles) {
		t.Errorf("a run with no project: %v, want sdk.ErrNoProjectFiles", err)
	}
}

func TestScreenshotRefusals(t *testing.T) {
	project := t.TempDir()
	dest := "x.png"
	off := seededVM(vboxmanage.StatePoweroff)
	off.Name = "off"
	blank := windowsVM()
	blank.Name = "blank"
	model := vboxmanagetest.New(windowsVM(), off, blank)
	model.Fail = map[string]vboxmanage.Output{"VBoxManage.exe controlvm blank screenshotpng": {ExitCode: 1,
		Stderr: "VBoxManage.exe: error: Unsupported resolution for screen shot: 0x0 (screen 0)\r\n"}}
	onModel(t, model)
	for why, tt := range map[string]struct {
		params map[string]any
		want   string
	}{
		"no dest":            {map[string]any{"name": "win-lab"}, "dest is required"},
		"not a png":          {map[string]any{"name": "win-lab", "dest": "x.jpg"}, "must name a .png file"},
		"a VM that is off":   {map[string]any{"name": "off", "dest": dest}, "only a running VM"},
		"a VM not there":     {map[string]any{"name": "gone", "dest": dest}, "no VM named"},
		"a screen not drawn": {map[string]any{"name": "blank", "dest": dest}, "no picture on its screen yet"},
	} {
		if _, err := call(t, "virt.vbox.vm.screenshot", false, newProjectRecorder(t, project), tt.params); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: %v, want one mentioning %q", why, err, tt.want)
		}
	}
	notPNG := vboxmanagetest.New(windowsVM())
	onModel(t, notPNG)
	notPNG.Fail = map[string]vboxmanage.Output{"powershell read": {Stdout: "bm90IGEgcGljdHVyZQ==\r\n"}}
	if _, err := call(t, "virt.vbox.vm.screenshot", false, newProjectRecorder(t, project), map[string]any{"name": "win-lab", "dest": dest}); err == nil || !strings.Contains(err.Error(), "not a PNG") {
		t.Errorf("bytes that are not a picture: %v", err)
	}
	for _, stat := range []string{statDest, statWidth} {
		onModel(t, vboxmanagetest.New(windowsVM()))
		failing := newProjectRecorder(t, project)
		failing.failOn = stat
		if _, err := call(t, "virt.vbox.vm.screenshot", false, failing, map[string]any{"name": "win-lab", "dest": dest}); err == nil {
			t.Errorf("recording %s failed and the task did not", stat)
		}
	}
	// A project file the picture cannot replace: a directory under its name.
	if err := os.Mkdir(filepath.Join(project, "taken.png"), 0o700); err != nil {
		t.Fatal(err)
	}
	onModel(t, vboxmanagetest.New(windowsVM()))
	if _, err := call(t, "virt.vbox.vm.screenshot", false, newProjectRecorder(t, project), map[string]any{"name": "win-lab", "dest": "taken.png"}); err == nil {
		t.Error("a directory was taken for the picture's file")
	}
}

func TestLog(t *testing.T) {
	model := vboxmanagetest.New(windowsVM())
	onModel(t, model)
	log := `G:\PleiadesLab\win-lab\Logs\VBox.log`
	rc := newRecorder()
	if _, err := call(t, "virt.vbox.vm.log", false, rc, map[string]any{"name": "win-lab"}); err != nil || len(rc.stats[statLines].([]string)) != 0 || rc.stats[statPath] != log {
		t.Fatalf("a VM that never ran: %v, %v", rc.stats, err)
	}
	model.SetFile(log, []byte("00:00:01 EFI: debug point DXE_CORE\r\n00:00:02 GIM: HyperV: guest\r\n00:00:03 EFI: debug point DXE_AP\r\n"))
	rc = newRecorder()
	if _, err := call(t, "virt.vbox.vm.log", false, rc, map[string]any{"name": "win-lab", "pattern": "EFI", "lines": 1}); err != nil {
		t.Fatal(err)
	}
	if got := rc.stats[statLines]; !reflect.DeepEqual(got, []string{"00:00:03 EFI: debug point DXE_AP"}) {
		t.Errorf("read %q", got)
	}
	if lines := lastLines([]byte(strings.Repeat("x", logTail-5)+"\ncut\n"), nil, 5); !reflect.DeepEqual(lines, []string{"cut"}) {
		t.Errorf("a read that began part way through a line kept it: %d lines", len(lines))
	}
	for why, p := range map[string]map[string]any{
		"no lines":           {"name": "win-lab", "lines": 0},
		"too many lines":     {"name": "win-lab", "lines": 5000},
		"lines not a number": {"name": "win-lab", "lines": "all"},
		"a bad pattern":      {"name": "win-lab", "pattern": "("},
	} {
		if _, err := call(t, "virt.vbox.vm.log", false, newRecorder(), p); err == nil {
			t.Errorf("%s: accepted", why)
		}
	}
	model.Fail = map[string]vboxmanage.Output{"powershell read": {ExitCode: 1, Stderr: "Access denied\r\n"}}
	if _, err := call(t, "virt.vbox.vm.log", false, newRecorder(), map[string]any{"name": "win-lab"}); err == nil {
		t.Error("a log that could not be read was reported")
	}
	model.Fail = nil
	for _, stat := range []string{statLines, statPath} {
		failing := newRecorder()
		failing.failOn = stat
		if _, err := call(t, "virt.vbox.vm.log", false, failing, map[string]any{"name": "win-lab"}); err == nil {
			t.Errorf("recording %s failed and the task did not", stat)
		}
	}
}

func TestSendKeys(t *testing.T) {
	model := vboxmanagetest.New(windowsVM())
	onModel(t, model)
	params := map[string]any{"name": "win-lab", "keys": "<tab><tab><enter>"}
	check := newRecorder()
	if result, err := call(t, "virt.vbox.vm.send_keys", true, check, params); err != nil || !result.Changed || len(changes(model)) != 0 || check.stats[statStrokes] != 3 {
		t.Fatalf("check: %+v, %v, %v", result, err, check.stats)
	}
	if result, err := call(t, "virt.vbox.vm.send_keys", false, newRecorder(), params); err != nil || !result.Changed {
		t.Fatalf("%+v, %v", result, err)
	}
	if typed := model.VM("win-lab").Typed; !reflect.DeepEqual(typed, []string{"0f 8f", "0f 8f", "1c 9c"}) {
		t.Errorf("typed %q", typed)
	}
	off := seededVM(vboxmanage.StatePoweroff)
	off.Name = "off"
	onModel(t, vboxmanagetest.New(windowsVM(), off))
	for why, p := range map[string]map[string]any{
		"no keys":          {"name": "win-lab"},
		"an unknown key":   {"name": "win-lab", "keys": "<return>"},
		"a VM that is off": {"name": "off", "keys": "<enter>"},
		"a VM not there":   {"name": "gone", "keys": "<enter>"},
	} {
		if _, err := call(t, "virt.vbox.vm.send_keys", false, newRecorder(), p); err == nil {
			t.Errorf("%s: accepted", why)
		}
	}
	failing := newRecorder()
	failing.failOn = statStrokes
	if _, err := call(t, "virt.vbox.vm.send_keys", false, failing, params); err == nil {
		t.Error("recording the strokes failed and the task did not")
	}
	broken := vboxmanagetest.New(windowsVM())
	broken.Fail = map[string]vboxmanage.Output{"VBoxManage.exe controlvm win-lab keyboardputscancode": {ExitCode: 1, Stderr: "VBoxManage.exe: error: refused\r\n"}}
	onModel(t, broken)
	if _, err := call(t, "virt.vbox.vm.send_keys", false, newRecorder(), params); err == nil {
		t.Error("keys the host refused were reported typed")
	}
}

func TestAddresses(t *testing.T) {
	model := vboxmanagetest.New(windowsVM())
	model.Leases = map[string]string{defaultHostOnlyAdapter: leases}
	onModel(t, model)
	rc := newRecorder()
	if _, err := call(t, "virt.vbox.vm.addresses", false, rc, map[string]any{"name": "win-lab"}); err != nil {
		t.Fatal(err)
	}
	found := rc.stats[statAddresses].([]map[string]any)
	if len(found) != 2 || found[0]["source"] != sourceFixed || found[0][statAddress] != "192.168.56.30" ||
		found[1]["source"] != sourceDHCP || found[1][statAddress] != "192.168.56.102" || found[1]["state"] != "acked" || found[1]["nic"] != 2 {
		t.Errorf("found %v", found)
	}
	// The fixed address comes first: a first boot that applied it leaves
	// the lease it took before then marked held for a while.
	if rc.stats[statAddress] != "192.168.56.30" {
		t.Errorf("reach it at %v", rc.stats[statAddress])
	}
	leaseOnly := windowsVM()
	leaseOnly.Extra = nil
	onModel(t, vboxmanagetest.New(leaseOnly))
	model = vboxmanagetest.New(leaseOnly)
	model.Leases = map[string]string{defaultHostOnlyAdapter: leases}
	onModel(t, model)
	byLease := newRecorder()
	if _, err := call(t, "virt.vbox.vm.addresses", false, byLease, map[string]any{"name": "win-lab"}); err != nil || byLease.stats[statAddress] != "192.168.56.102" {
		t.Errorf("no fixed address: %v, %v", byLease.stats, err)
	}
	bare := windowsVM()
	bare.Extra = nil
	onModel(t, vboxmanagetest.New(bare))
	none := newRecorder()
	if _, err := call(t, "virt.vbox.vm.addresses", false, none, map[string]any{"name": "win-lab"}); err != nil || len(none.stats[statAddresses].([]map[string]any)) != 0 {
		t.Errorf("a VM with no address: %v, %v", none.stats, err)
	}
	if _, set := none.stats[statAddress]; set {
		t.Error("an address was named for a VM with none")
	}
	for key := range map[string]bool{"VBoxManage.exe getextradata": true, "powershell leases": true} {
		failing := vboxmanagetest.New(windowsVM())
		failing.Fail = map[string]vboxmanage.Output{key: {ExitCode: 1, Stderr: "VBoxManage.exe: error: refused\r\n"}}
		onModel(t, failing)
		if _, err := call(t, "virt.vbox.vm.addresses", false, newRecorder(), map[string]any{"name": "win-lab"}); err == nil {
			t.Errorf("%s failing: reported", key)
		}
	}
	for _, stat := range []string{statAddresses, statAddress} {
		onModel(t, vboxmanagetest.New(windowsVM()))
		failing := newRecorder()
		failing.failOn = stat
		if _, err := call(t, "virt.vbox.vm.addresses", false, failing, map[string]any{"name": "win-lab"}); err == nil {
			t.Errorf("recording %s failed and the task did not", stat)
		}
	}
}
