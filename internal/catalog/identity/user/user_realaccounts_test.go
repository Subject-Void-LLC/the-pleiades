// Package user_test: the identity.user checks against a real account
// database, inside an unprivileged user and mount namespace.
package user_test

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	inventorytest "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestChecks_AgainstARealAccountDatabase is the control the fake getent
// cannot be: each user method's check, then its real run, with real
// useradd, usermod, userdel and getent. They need root and write /etc,
// so the test re-runs itself as root in an unprivileged user and mount
// namespace over a private copy of /etc and an empty /home
// (testsupport.PrivateEtc); the host's own account database is never
// touched. The check leaves the account as getent reported it, decides
// the change the real run makes, and every key its after half states,
// including a group resolved to its gid, is the one the real run leaves.
func TestChecks_AgainstARealAccountDatabase(t *testing.T) {
	if !testsupport.InPrivateRoot(t, testsupport.PrivateEtc) {
		return
	}
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatalf("starting the SSH harness: %v", err)
	}
	t.Cleanup(srv.Close)
	device := &target{
		Stub: &inventorytest.Stub{StubName: "web1", Caps: []capability.Name{capability.NamePosixAccount}},
		host: srv.Host, port: srv.Port,
	}
	const name = "pleiadescheckusr"
	entry := func() string {
		out, _ := exec.Command("getent", "passwd", name).Output() // #nosec G204 -- a fixed name
		return strings.TrimSpace(string(out))
	}

	step := func(fqcn string, extra map[string]any, wantChange bool) {
		t.Helper()
		d, ok := collection.Lookup(fqcn)
		if !ok || d.Check == nil {
			t.Fatalf("%s does not declare a check", fqcn)
		}
		params := map[string]any{"insecure_skip_host_key_verify": true, "name": name}
		for k, v := range extra {
			params[k] = v
		}
		before := entry()
		crc := &ctxStub{secrets: srv.Secrets(), stats: map[string]any{}}
		checked, err := d.Check(context.Background(), crc, device, params)
		if err != nil {
			t.Fatalf("%s check: %v", fqcn, err)
		}
		if got := entry(); got != before {
			t.Fatalf("%s's check changed the account: %q became %q", fqcn, before, got)
		}
		rrc := &ctxStub{secrets: srv.Secrets(), stats: map[string]any{}}
		ran, err := d.Invoke(context.Background(), rrc, device, params)
		if err != nil {
			t.Fatalf("%s real run: %v", fqcn, err)
		}
		if checked.Changed != wantChange || ran.Changed != wantChange {
			t.Errorf("%s: the check predicted Changed = %v and the real run reported %v, want %v", fqcn, checked.Changed, ran.Changed, wantChange)
		}
		predicted := afterOf(t, crc)
		actual := afterOf(t, rrc)
		for key, want := range predicted {
			if actual[key] != want {
				t.Errorf("%s: predicted %s = %v, the real run left %v", fqcn, key, want, actual[key])
			}
		}
	}

	// No home directory: the namespace maps only root, so useradd -m
	// cannot give a new account's home to its uid, and userdel -r then
	// refuses a home its user does not own. That is the namespace's limit,
	// not the method's; the account database is what this test proves.
	create := map[string]any{"shell": "/bin/sh", "comment": "Pleiades check", "group": "users", "create_home": false}
	step("identity.user.create", create, true)
	if entry() == "" {
		t.Fatal("the real useradd left no account, so the comparisons prove nothing")
	}
	step("identity.user.create", create, false)
	step("identity.user.modify", map[string]any{"shell": "/bin/bash"}, true)
	step("identity.user.modify", map[string]any{"shell": "/bin/bash"}, false)
	step("identity.user.remove", nil, true)
	step("identity.user.remove", nil, false)
}

// afterOf returns a recorded diff's after half.
func afterOf(t *testing.T, rc *ctxStub) map[string]any {
	t.Helper()
	diff, ok := rc.stats[sdk.StatDiff].(map[string]any)
	if !ok {
		t.Fatalf("no diff recorded: %v", rc.stats)
	}
	after, _ := diff[sdk.DiffAfter].(map[string]any)
	return after
}
