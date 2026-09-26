// Tests for the --machinereadable parser, on output captured from
// VirtualBox 7.2.20 on Windows 11 (testdata, CRLF line endings kept) and
// on the escapes VBoxManage writes.
package vboxmanage

import (
	"os"
	"strings"
	"testing"
)

// fixture reads a captured file from testdata.
func fixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestMachineFrom_CapturedShowVMInfo(t *testing.T) {
	values, err := ParseMachineReadable(fixture(t, "showvminfo-poweroff.stdout"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := machineFrom(values)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "pleiades-probe" || m.UUID != "9883f6c3-b17d-4077-aa52-d9c49a6912a5" || m.State != StatePoweroff {
		t.Errorf("identity = %q %q %q", m.Name, m.UUID, m.State)
	}
	if m.ConfigFile != `G:\PleiadesLab\pleiades-probe\pleiades-probe.vbox` {
		t.Errorf("CfgFile = %q, want each doubled backslash read as one", m.ConfigFile)
	}
	if m.MemoryMB != 64 || m.CPUs != 1 || m.AutostartEnabled || len(m.Snapshots) != 0 || m.CurrentSnapshotUUID != "" {
		t.Errorf("machine = %+v", m)
	}
	// A section marker line (" rec_screen0") is skipped, not a failure.
	if _, ok := values.Get("rec_screen0"); ok {
		t.Error("a section marker was read as a key")
	}
}

func TestMachineFrom_CapturedSnapshot(t *testing.T) {
	values, err := ParseMachineReadable(fixture(t, "showvminfo-with-snapshot.stdout"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := machineFrom(values)
	if err != nil {
		t.Fatal(err)
	}
	want := Snapshot{Name: "fixture-snap", UUID: "5732a952-0b85-4e3a-820a-1a568f31166b", Description: "fixture"}
	if len(m.Snapshots) != 1 || m.Snapshots[0] != want || m.CurrentSnapshotUUID != want.UUID {
		t.Errorf("snapshots = %+v, current %q", m.Snapshots, m.CurrentSnapshotUUID)
	}
	if got := m.SnapshotsNamed("fixture-snap"); len(got) != 1 {
		t.Errorf("SnapshotsNamed = %+v", got)
	}
}

func TestSnapshotsFrom_TreeAndSharedNames(t *testing.T) {
	values, err := ParseMachineReadable(strings.Join([]string{
		`SnapshotName="base"`, `SnapshotUUID="11111111-1111-1111-1111-111111111111"`,
		`SnapshotName-1="child"`, `SnapshotUUID-1="22222222-2222-2222-2222-222222222222"`,
		`SnapshotName-1-1="base"`, `SnapshotUUID-1-1="33333333-3333-3333-3333-333333333333"`,
		`CurrentSnapshotName="base"`, `CurrentSnapshotUUID="33333333-3333-3333-3333-333333333333"`,
	}, "\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	m := Machine{Snapshots: snapshotsFrom(values)}
	paths := []string{}
	for _, s := range m.Snapshots {
		paths = append(paths, s.Path+":"+s.Name)
	}
	if strings.Join(paths, " ") != ":base -1:child -1-1:base" {
		t.Errorf("tree = %v", paths)
	}
	if len(m.SnapshotsNamed("base")) != 2 {
		t.Error("two snapshots sharing a name were not both found")
	}
}

func TestParseMachineReadable(t *testing.T) {
	values, err := ParseMachineReadable("\"SATA-0-0\"=\"G:\\\\disks\\\\a \\\"b\\\".vdi\"\r\nmemory=64\r\n\r\n rec_screen0\r\nk=\"\"\r\nk=\"second\"\r\n")
	if err != nil {
		t.Fatal(err)
	}
	want := Values{{"SATA-0-0", `G:\disks\a "b".vdi`}, {"memory", "64"}, {"k", ""}, {"k", "second"}}
	if len(values) != len(want) {
		t.Fatalf("values = %q", values)
	}
	for i := range want {
		if values[i] != want[i] {
			t.Errorf("pair %d = %q, want %q", i, values[i], want[i])
		}
	}
	if v, _ := values.Get("k"); v != "" {
		t.Errorf("Get returned %q, want the first value", v)
	}
	// Text after a closing quote is kept, as a running VM's VideoMode
	// needs; an unterminated quote is still refused.
	if v, err := ParseMachineReadable(`VideoMode="1024,768,32"@0,0 1`); err != nil || v[0].Value != "1024,768,32@0,0 1" {
		t.Errorf("a value with text after its quote = %q, %v", v, err)
	}
	for _, bad := range []string{`name="unterminated`, `"key=1`} {
		if _, err := ParseMachineReadable(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

// quote writes s the way VBoxManage quotes a value.
func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// FuzzParseMachineReadable holds two promises: the parser never panics,
// and any key and value, quoted the way VBoxManage quotes them, read back
// exactly.
func FuzzParseMachineReadable(f *testing.F) {
	f.Add("CfgFile", `G:\PleiadesLab\a.vbox`)
	f.Add("SATA-0-0", `a "quoted" \ value`)
	f.Add("k", "")
	f.Fuzz(func(t *testing.T, key, value string) {
		_, _ = ParseMachineReadable(key + "=" + value)
		if strings.ContainsAny(key+value, "\r\n") {
			return
		}
		values, err := ParseMachineReadable(quote(key) + "=" + quote(value) + "\r\n")
		if err != nil {
			t.Fatalf("parse of %q=%q: %v", key, value, err)
		}
		if len(values) != 1 || values[0].Key != key || values[0].Value != value {
			t.Fatalf("round trip of %q=%q gave %q", key, value, values)
		}
	})
}

func TestMachineFrom_CapturedRunningMachine(t *testing.T) {
	values, err := ParseMachineReadable(fixture(t, "showvminfo-running.stdout"))
	if err != nil {
		t.Fatalf("a running machine's answer did not parse: %v", err)
	}
	m, err := machineFrom(values)
	if err != nil {
		t.Fatal(err)
	}
	if m.State != StateRunning || !m.AutostartEnabled {
		t.Errorf("state %q, autostart %v; want running and marked, as the autostart start leaves it", m.State, m.AutostartEnabled)
	}
	if mode, ok := values.Get("VideoMode"); !ok || !strings.Contains(mode, "@") {
		t.Errorf("VideoMode = %q, want the text after its quote kept", mode)
	}
}
