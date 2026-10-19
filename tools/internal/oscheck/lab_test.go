// Tests for a lab check: its two settings, its advice, and its main's flow
// through a stand-in for the machine.
package oscheck

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// labCheck is a lab check as a test configures one.
var labCheck = LabCheck{Tool: "xcheck", Kind: "box", OS: "BoxOS", EnvFile: "tests/x/.env", ProjectKey: "X_PROJECT", DeviceKey: "X_DEVICE"}

// TestLabTarget: neither setting is no machine, both name one (with ~/
// expanded), and one alone, a device name pleiades would misread, or a
// directory that is not a project are refused.
func TestLabTarget(t *testing.T) {
	project := t.TempDir()
	writeFile(t, project, "inventory.yaml", "devices: []\n")
	settings := func(p, d string) Settings { return Settings{Values: map[string]string{"P": p, "D": d}} }
	if p, d, err := LabTarget(settings("", ""), "P", "D"); p != "" || d != "" || err != nil {
		t.Errorf("neither: %q %q %v", p, d, err)
	}
	if p, d, err := LabTarget(settings(project, "box-1"), "P", "D"); p != project || d != "box-1" || err != nil {
		t.Errorf("both: %q %q %v", p, d, err)
	}
	for _, tc := range []struct {
		project, device, want string
	}{
		{project, "", "set both, or neither"},
		{"", "box-1", "set both, or neither"},
		{project, "-oProxyCommand=x", "is not a device name"},
		{project, "a b", "is not a device name"},
		{t.TempDir(), "box-1", "is not a Pleiades project: it holds no inventory.yaml"},
	} {
		if _, _, err := LabTarget(settings(tc.project, tc.device), "P", "D"); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q %q: %v, want %q", tc.project, tc.device, err, tc.want)
		}
	}
}

// TestLabCheck_Run drives a lab check's main: nothing configured (a skip
// with -optional, an error without), a setting refused with the advice
// naming its file, the machine it names handed the project and device, and
// the run's outcome.
func TestLabCheck_Run(t *testing.T) {
	dir := inRepo(t)
	t.Setenv("X_PROJECT", "")
	t.Setenv("X_DEVICE", "")
	project := t.TempDir()
	writeFile(t, project, "inventory.yaml", "devices: []\n")
	file := filepath.Join(dir, "tests/x/.env")
	pkgs := []string{"github.com/x/a"}
	run := func(leg *fakeLeg, args ...string) (int, string, string, string) {
		t.Helper()
		var gotProject, gotDevice string
		var stdout, stderr bytes.Buffer
		code := labCheck.Run(context.Background(), args, &stdout, &stderr, func(p, d string) (Leg, error) {
			gotProject, gotDevice = p, d
			return leg, nil
		})
		return code, stdout.String() + stderr.String(), gotProject, gotDevice
	}

	if code, out, _, _ := run(&fakeLeg{}, append([]string{"-optional"}, pkgs...)...); code != 0 || !strings.Contains(out, "no box configured (X_PROJECT and X_DEVICE in tests/x/.env), so BoxOS was NOT checked") {
		t.Errorf("optional, nothing configured: exit %d\n%s", code, out)
	}
	if code, out, _, _ := run(&fakeLeg{}, pkgs...); code != 2 || !strings.Contains(out, "cp tests/x/.env.example tests/x/.env, and set X_PROJECT and X_DEVICE in it") {
		t.Errorf("nothing configured: exit %d\n%s", code, out)
	}
	if code, out, _, _ := run(&fakeLeg{}, "a b"); code != 2 || !strings.Contains(out, "is not an import path") {
		t.Errorf("a bad package: exit %d\n%s", code, out)
	}

	writeFile(t, dir, "tests/x/.env", "X_PROJECT="+project+"\n")
	if code, out, _, _ := run(&fakeLeg{}, pkgs...); code != 2 || !strings.Contains(out, "set both, or neither") || !strings.Contains(out, "delete "+file) {
		t.Errorf("one setting: exit %d\n%s", code, out)
	}
	writeFile(t, dir, "tests/x/.env", "X_PROJECT="+project+"\nX_DEVICE=box-1\nX_OTHER=1\n")
	if code, out, _, _ := run(&fakeLeg{}, pkgs...); code != 2 || !strings.Contains(out, "sets X_OTHER") {
		t.Errorf("a setting it does not know: exit %d\n%s", code, out)
	}

	writeFile(t, dir, "tests/x/.env", "X_PROJECT="+project+"\nX_DEVICE=box-1\n")
	code, out, gotProject, gotDevice := run(&fakeLeg{passed: true}, pkgs...)
	if code != 0 || gotProject != project || gotDevice != "box-1" || !strings.Contains(out, "all tests passed on box-1 (BoxOS)") {
		t.Errorf("configured: exit %d, leg for %q %q\n%s", code, gotProject, gotDevice, out)
	}
	if code, out, _, _ := run(&fakeLeg{reachable: errors.New("box-1 did not answer")}, pkgs...); code != 2 || !strings.Contains(out, "To stop testing on a box: delete "+file) {
		t.Errorf("unreachable: exit %d\n%s", code, out)
	}

	var stderr bytes.Buffer
	failing := func(string, string) (Leg, error) { return nil, errors.New("no git directory") }
	if code := labCheck.Run(context.Background(), pkgs, &stderr, &stderr, failing); code != 2 || !strings.Contains(stderr.String(), "no git directory") {
		t.Errorf("a machine that could not be made: exit %d\n%s", code, &stderr)
	}
}

// TestLabAdvice names the settings file when one named the machine, and the
// environment's variables otherwise.
func TestLabAdvice(t *testing.T) {
	fromFile := LabAdvice("/r/tests/x/.env", "tests/x/.env", "X_PROJECT", "X_DEVICE", "box")
	if !strings.Contains(fromFile, "set X_PROJECT and X_DEVICE in /r/tests/x/.env") || !strings.Contains(fromFile, "To stop testing on a box: delete /r/tests/x/.env") {
		t.Errorf("from a file:\n%s", fromFile)
	}
	fromEnv := LabAdvice("", "tests/x/.env", "X_PROJECT", "X_DEVICE", "box")
	if !strings.Contains(fromEnv, "unset X_PROJECT and X_DEVICE, and delete tests/x/.env if one names a box") {
		t.Errorf("from the environment:\n%s", fromEnv)
	}
}

// TestNewLab builds pleiades under the worktree's own git directory, named
// for the check.
func TestNewLab(t *testing.T) {
	dir := inRepo(t)
	lab, err := NewLab("xcheck", "/p", "box-1")
	if err != nil || lab.Pleiades.Binary != filepath.Join(dir, ".git", "pleiades-xcheck", "pleiades") || lab.Pleiades.Project != "/p" || lab.Pleiades.Device != "box-1" || lab.Target != "box-1" {
		t.Errorf("NewLab = %+v, %v", lab, err)
	}
	if !strings.HasSuffix(filepath.Dir(lab.Pleiades.Binary), "pleiades-xcheck") || !dirExists(filepath.Dir(lab.Pleiades.Binary)) {
		t.Error("the private directory was not made")
	}
}

// TestLabCheckBox_BuildsThisTreesPleiades: the box is driven by a pleiades
// built from this tree every time, even with one already at the binary's
// path, since a binary an earlier run left is another tree's; a build that
// fails is no box.
func TestLabCheckBox_BuildsThisTreesPleiades(t *testing.T) {
	dir := inRepo(t)
	stale := filepath.Join(dir, ".git", "pleiades-xcheck", "pleiades")
	if err := os.MkdirAll(filepath.Dir(stale), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("an earlier tree's build"), 0o700); err != nil {
		t.Fatal(err)
	}
	var built []string
	old := buildPleiades
	t.Cleanup(func() { buildPleiades = old })
	buildPleiades = func(p Pleiades) error { built = append(built, p.Binary); return nil }
	c := LabCheck{Tool: "xcheck"}
	box, err := c.box("/p", "box-1", "vengeance", "box-vm")
	if err != nil || len(built) != 1 || built[0] != stale {
		t.Fatalf("box %+v, err %v, built %v", box, err, built)
	}
	if box.Host.Device != "vengeance" || box.Device.Device != "box-1" || box.VM != "box-vm" || box.Host.Binary != stale {
		t.Errorf("box %+v", box)
	}
	buildPleiades = func(Pleiades) error { return errors.New("go build: undefined: x") }
	if _, err := c.box("/p", "box-1", "vengeance", "box-vm"); err == nil || !strings.Contains(err.Error(), "undefined: x") {
		t.Errorf("a build that failed: %v", err)
	}
}
