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
