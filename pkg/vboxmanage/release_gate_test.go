// The Release Gate for pkg/vboxmanage: the real VBoxManage on a real
// Windows VirtualBox host, reached over WinRM by client certificate.
//
// It skips unless PLEIADES_WINRM_HOST, PLEIADES_WINRM_PFX with
// PLEIADES_WINRM_PFX_PASSWORD_FILE, PLEIADES_WINRM_CA and PLEIADES_VBOX_VM
// are set: the last names a powered-off VM this gate may snapshot, which
// it leaves with the snapshots it found. examples/windows_lab says how the
// host is prepared.
package vboxmanage_test

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// gateHost builds the Host the gate runs against, or skips.
func gateHost(t *testing.T) (vboxmanage.Host, string) {
	t.Helper()
	host, pfxPath, passFile, caPath, vm := os.Getenv("PLEIADES_WINRM_HOST"), os.Getenv("PLEIADES_WINRM_PFX"),
		os.Getenv("PLEIADES_WINRM_PFX_PASSWORD_FILE"), os.Getenv("PLEIADES_WINRM_CA"), os.Getenv("PLEIADES_VBOX_VM")
	if host == "" || pfxPath == "" || passFile == "" || caPath == "" || vm == "" {
		t.Skip("the vboxmanage Release Gate needs PLEIADES_WINRM_HOST, PLEIADES_WINRM_PFX, " +
			"PLEIADES_WINRM_PFX_PASSWORD_FILE, PLEIADES_WINRM_CA and PLEIADES_VBOX_VM (a powered-off VM it may snapshot)")
	}
	read := func(path string) []byte {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	auth, err := winrmexec.AuthFromSecrets(map[string]string{
		wire.SecretPFXBase64:  base64.StdEncoding.EncodeToString(read(pfxPath)),
		wire.SecretPassphrase: strings.TrimRight(string(read(passFile)), "\r\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	port := winrmexec.DefaultPortHTTPS
	if p := os.Getenv("PLEIADES_WINRM_PORT"); p != "" {
		if port, err = strconv.Atoi(p); err != nil {
			t.Fatal(err)
		}
	}
	runner := vboxmanage.WinRMRunner{
		Target: winrmexec.Target{Host: host, Port: port}, Auth: auth,
		Options: winrmexec.Options{CACert: read(caPath), Timeout: 2 * time.Minute},
	}
	return vboxmanage.Host{Runner: runner, Path: `C:\Program Files\Oracle\VirtualBox\VBoxManage.exe`}, vm
}

func TestReleaseGate_VBoxManageOnARealHost(t *testing.T) {
	h, vm := gateHost(t)
	ctx := context.Background()

	m, err := h.Machine(ctx, vm)
	if err != nil {
		t.Fatalf("Machine(%s): %v", vm, err)
	}
	if m.State != vboxmanage.StatePoweroff {
		t.Skipf("%s is %s; this gate needs it powered off", vm, m.State)
	}
	refs, err := h.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range refs {
		found = found || (r.Name == vm && r.UUID == m.UUID)
	}
	if !found {
		t.Errorf("List = %+v, missing %s", refs, vm)
	}

	if _, err := h.Machine(ctx, "pleiades-no-such-vm"); !errors.Is(err, vboxmanage.ErrNotFound) {
		t.Errorf("a missing VM: err = %v, want ErrNotFound", err)
	}
	if err := h.PowerOff(ctx, vm); !errors.Is(err, vboxmanage.ErrNotRunning) {
		t.Errorf("powering off a stopped VM: err = %v, want ErrNotRunning", err)
	}

	// Two snapshots under one name, which VirtualBox allows, each found
	// and deleted by its own UUID.
	var taken []string
	for i := 0; i < 2; i++ {
		uuid, err := h.TakeSnapshot(ctx, vm, "gate-snap", "vboxmanage release gate, safe to delete")
		if err != nil {
			t.Fatalf("TakeSnapshot: %v", err)
		}
		taken = append(taken, uuid)
	}
	t.Cleanup(func() {
		for _, uuid := range taken {
			_ = h.DeleteSnapshot(context.Background(), vm, uuid)
		}
	})
	after, err := h.Machine(ctx, vm)
	if err != nil {
		t.Fatal(err)
	}
	named := after.SnapshotsNamed("gate-snap")
	if len(named) != 2 || named[0].UUID == named[1].UUID {
		t.Fatalf("snapshots named gate-snap = %+v, want two with different UUIDs", named)
	}
	for _, uuid := range taken {
		if err := h.DeleteSnapshot(ctx, vm, uuid); err != nil {
			t.Fatalf("DeleteSnapshot(%s): %v", uuid, err)
		}
	}
	taken = nil
	final, err := h.Machine(ctx, vm)
	if err != nil {
		t.Fatal(err)
	}
	if len(final.SnapshotsNamed("gate-snap")) != 0 {
		t.Errorf("snapshots left: %+v", final.Snapshots)
	}
}

// TestReleaseGate_StartsThroughTheAutostartService starts the gate's VM
// from a WinRM logon through the account's autostart service, which is the
// only way a VM starts from one, and powers it off again. It also records
// whether VirtualBox lets a running machine's autostart mark be cleared.
func TestReleaseGate_StartsThroughTheAutostartService(t *testing.T) {
	h, vm := gateHost(t)
	ctx := context.Background()
	if m, err := h.Machine(ctx, vm); err != nil || m.State != vboxmanage.StatePoweroff {
		t.Skipf("%s: %v; this gate needs it powered off", vm, err)
	}
	t.Cleanup(func() {
		_ = h.PowerOff(context.Background(), vm)
		time.Sleep(3 * time.Second)
		_ = h.SetAutostart(context.Background(), vm, false)
	})

	a := vboxmanage.WindowsAutostart{Host: h}
	viaService, err := a.Start(ctx, vm)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !viaService {
		t.Error("with no VM running, the start did not go through the service")
	}
	m, err := h.Machine(ctx, vm)
	if err != nil || m.State != vboxmanage.StateRunning || !m.AutostartEnabled {
		t.Fatalf("after Start: %+v, %v", m, err)
	}
	// A running machine's answer, kept for the parser's own tests.
	if path := os.Getenv("PLEIADES_VBOX_CAPTURE"); path != "" {
		out, err := h.Runner.Run(ctx, h.Path, []string{"showvminfo", vm, "--machinereadable"})
		if err == nil {
			err = os.WriteFile(path, []byte(out.Stdout), 0o600)
		}
		if err != nil {
			t.Errorf("capturing a running machine: %v", err)
		}
	}
	// Measured, not assumed: can the mark be cleared while it runs?
	err = h.SetAutostart(ctx, vm, false)
	t.Logf("clearing the autostart mark while running: %v", err)
	// While it runs, a second Start is a plain one through the service's
	// server, which is refused only because the VM already runs.
	if viaService, err := a.Start(ctx, vm); viaService || err == nil {
		t.Errorf("a second Start: via service %v, err %v; want a plain start refused as already running", viaService, err)
	} else {
		t.Logf("a second, plain Start of the running VM: %v", err)
	}
	if err := h.PowerOff(ctx, vm); err != nil {
		t.Fatalf("PowerOff: %v", err)
	}
}
