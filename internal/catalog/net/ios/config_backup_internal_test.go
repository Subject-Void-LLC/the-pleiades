// Package ios: net.ios.config's backup stat over a real SSH session, against
// a scripted device whose running configuration holds short and clear-text
// secrets that masking by value alone (eight characters or more) would let
// through.
package ios

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// backupDevice starts a scripted IOS device in configuration mode whose
// show running-config answers running.
func backupDevice(t *testing.T, running string) (*remoteexectest.Server, *secretContext, *sshDevice) {
	t.Helper()
	srv, err := remoteexectest.Start(remoteexectest.Options{Device: &remoteexectest.Device{
		Prompt:  "r1#",
		Config:  &remoteexectest.ConfigMode{Enter: "configure terminal", Exit: "end", Prompt: "r1(config)#"},
		Replies: map[string]string{"show running-config": running},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	device := &sshDevice{
		Stub: &inventorytest.Stub{StubName: "r1", Caps: []capability.Name{capability.NameSSHTransport}},
		host: srv.Host, port: srv.Port,
	}
	return srv, &secretContext{stubContext: newStubContext(), secrets: srv.Secrets()}, device
}

// TestConfig_BackupMasksShortAndClearTextSecrets: the backup stat holds the
// configuration with every secret masked structurally, a five-letter
// enable password and a clear-text user password with a space in it
// included, which masking by value alone never reached.
func TestConfig_BackupMasksShortAndClearTextSecrets(t *testing.T) {
	running := "Building configuration...\n\nhostname r1\n!\nenable password cisco\nusername admin password 0 bk1 bk2\nsnmp-server community pub RO\ntacacs-server key 0 bk3 bk4\n!\nend"
	srv, rc, device := backupDevice(t, running)

	if _, err := Config(context.Background(), rc, device, sessionParams(map[string]any{
		paramLines: []any{"hostname r2"}, paramBackup: true, paramCheckpoint: false,
	})); err != nil {
		t.Fatal(err)
	}
	backup, _ := rc.stats[statBackup].(string)
	if !strings.Contains(backup, "hostname r1") || !strings.Contains(backup, "enable password "+netcfg.MaskPrefix) {
		t.Fatalf("backup is not the masked configuration:\n%s", backup)
	}
	for _, secret := range []string{"cisco", "bk1", "bk2", " pub ", "bk3", "bk4"} {
		if strings.Contains(backup, secret) {
			t.Errorf("backup kept %q:\n%s", secret, backup)
		}
	}
	if !slices.Contains(srv.Commands(), "hostname r2") {
		t.Errorf("the device received %q: the line was never applied", srv.Commands())
	}
}

// TestConfig_ABackupMaskingCannotVouchForFailsBeforeAnyLine: a line holding
// a second secret masking leaves in place fails the task before any line
// reaches the device, records no backup, and quotes neither secret.
func TestConfig_ABackupMaskingCannotVouchForFailsBeforeAnyLine(t *testing.T) {
	running := "hostname r1\nradius-server host 10.0.0.1 key 7 FirstBk123 pac key 7 SecondBk456\nend"
	srv, rc, device := backupDevice(t, running)

	_, err := Config(context.Background(), rc, device, sessionParams(map[string]any{
		paramLines: []any{"hostname r2"}, paramBackup: true, paramCheckpoint: false,
	}))
	if err == nil || !strings.Contains(err.Error(), "capturing backup") {
		t.Fatalf("err %v", err)
	}
	if strings.Contains(err.Error(), "FirstBk123") || strings.Contains(err.Error(), "SecondBk456") {
		t.Error("the refusal quoted a secret")
	}
	if _, set := rc.stats[statBackup]; set {
		t.Error("a backup masking could not vouch for was recorded")
	}
	if slices.Contains(srv.Commands(), "hostname r2") {
		t.Error("a line reached the device after the backup failed")
	}
}
