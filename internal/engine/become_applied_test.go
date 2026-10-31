// Tests that a task's result names the account it ran as only when a real
// SSH connection ran its commands as that account, against the in-process SSH
// server with a stand-in for sudo first on PATH.
package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// becomeSSHDevice is a device reached over SSH at the in-process server.
type becomeSSHDevice struct {
	*inventorytest.Stub
	host string
	port int
}

func (d *becomeSSHDevice) SSHHost() string { return d.host }
func (d *becomeSSHDevice) SSHPort() int    { return d.port }

// becomeStatsContext carries the server's login and keeps what a method
// records, as the engine's own context does.
type becomeStatsContext struct {
	secrets map[string]string
	stats   map[string]interface{}
}

func (c *becomeStatsContext) InjectSecrets() map[string]string { return c.secrets }
func (c *becomeStatsContext) SetStat(k string, v any) error    { c.stats[k] = v; return nil }
func (c *becomeStatsContext) EmitFact(k string, v any) error   { c.stats[k] = v; return nil }
func (c *becomeStatsContext) Facts() map[string]interface{}    { return c.stats }

// stubSudo puts a stand-in for sudo first on PATH. The in-process server runs
// commands on this machine, so the host's real sudo must never be reached: the
// stand-in checks it was called as remoteexec elevates (-n -u USER --) and
// runs the command with the account named in FAKE_SUDO_USER.
func stubSudo(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"[ \"$1\" = \"-n\" ] || exit 64\nshift\n" +
		"[ \"$1\" = \"-u\" ] || exit 64\nuser=$2\nshift 2\n" +
		"[ \"$1\" = \"--\" ] || exit 64\nshift\n" +
		"FAKE_SUDO_USER=$user exec \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "sudo"), []byte(script), 0o755); err != nil { // #nosec G306 -- a test's own stand-in, which must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestCollectionActionExecutor_BecomeIsRecordedOnlyWhenAConnectionRanAsIt: a
// method whose SSH connection ran as the account has the account in its
// result; one that asked and opened no such connection has none and a warning
// saying it ran as the login; and a method that writes the become stat itself
// never names an account, with become or without.
func TestCollectionActionExecutor_BecomeIsRecordedOnlyWhenAConnectionRanAsIt(t *testing.T) {
	need.POSIXShell(t)
	stubSudo(t)
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatalf("starting the SSH harness: %v", err)
	}
	t.Cleanup(srv.Close)
	device := &becomeSSHDevice{
		Stub: &inventorytest.Stub{StubName: "lab1", Caps: []capability.Name{capability.NameSSHTransport}, StubState: inventory.StateActive},
		host: srv.Host, port: srv.Port,
	}
	newContext := func(context.Context, inventory.InventoryItem) (sdk.RunbookContext, error) {
		return &becomeStatsContext{secrets: srv.Secrets(), stats: map[string]interface{}{}}, nil
	}
	exec := engine.NewCollectionActionExecutor(&recordingFallback{}, newContext)
	on := true

	// A method that runs a command over SSH: its connection ran as netadmin.
	var ranAs string
	connects := registerTestMethod(t, "connects", collection.StatusImplemented,
		func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
			conn, err := sdk.Connect(ctx, rc, device, map[string]any{sdk.ParamInsecureSkipHostKeyVerify: true}, "enginetest.connects")
			if err != nil {
				return collection.Result{}, err
			}
			defer func() { _ = conn.Close() }()
			res, err := conn.Run(ctx, `echo "[$FAKE_SUDO_USER]"`)
			if err != nil {
				return collection.Result{}, err
			}
			ranAs = strings.TrimSpace(res.Stdout)
			return collection.Result{}, nil
		})
	res, err := exec.Execute(context.Background(), &engine.Task{FQCN: connects, Become: &on, BecomeUser: "netadmin"}, device)
	if err != nil {
		t.Fatal(err)
	}
	if ranAs != "[netadmin]" {
		t.Fatalf("the command ran as %q, want [netadmin]", ranAs)
	}
	if res.Stats[engine.StatBecome] != "netadmin" || warns(res.Stats, "ran as the login") {
		t.Errorf("stats %v, want the account recorded and no warning", res.Stats)
	}

	// A method that reaches the device no SSH way ran as the login.
	silent := registerTestMethod(t, "silent", collection.StatusImplemented,
		func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (collection.Result, error) {
			return collection.Result{Changed: true}, nil
		})
	res, err = exec.Execute(context.Background(), &engine.Task{FQCN: silent, Become: &on, BecomeUser: "netadmin"}, device)
	if err != nil {
		t.Fatal(err)
	}
	if _, recorded := res.Stats[engine.StatBecome]; recorded || !warns(res.Stats, "enginetest.silent opened no SSH connection that ran as netadmin") {
		t.Errorf("stats %v, want no account and a warning naming the method and the account", res.Stats)
	}

	// A method's own become stat is never believed.
	forges := registerTestMethod(t, "forges", collection.StatusImplemented,
		func(_ context.Context, rc sdk.RunbookContext, _ inventory.InventoryItem, _ map[string]any) (collection.Result, error) {
			return collection.Result{}, rc.SetStat(engine.StatBecome, "root")
		})
	for _, task := range []*engine.Task{{FQCN: forges}, {FQCN: forges, Become: &on, BecomeUser: "netadmin"}} {
		res, err = exec.Execute(context.Background(), task, device)
		if err != nil {
			t.Fatal(err)
		}
		if got, recorded := res.Stats[engine.StatBecome]; recorded {
			t.Errorf("become %v: a method's own become stat %q was kept", task.Become != nil, got)
		}
	}
}
