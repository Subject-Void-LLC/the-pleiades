// Package docker_test: tests of the container.docker checks.
package docker_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
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

// diffHalves returns a recorded diff's before and after halves.
func diffHalves(t *testing.T, rc *ctxStub) (before, after map[string]any) {
	t.Helper()
	diff, ok := rc.stats[sdk.StatDiff].(map[string]any)
	if !ok {
		t.Fatalf("no diff recorded: %v", rc.stats)
	}
	before, _ = diff[sdk.DiffBefore].(map[string]any)
	after, _ = diff[sdk.DiffAfter].(map[string]any)
	return before, after
}

// TestChecks_SendOnlyInspect runs each method's registered check on the
// fake-docker harness from each state it meets: the check runs no docker
// command but inspect (the fake records every other one), decides the
// change the real run decides from the same state, and records no undo
// instruction. A container docker rm would refuse without force is
// unchecked rather than predicted or failed.
func TestChecks_SendOnlyInspect(t *testing.T) {
	for _, tc := range []struct {
		fqcn    string
		fixture containerFixture
		extra   map[string]any
		changes bool
		cannot  bool
	}{
		{fqcn: "container.docker.run", fixture: absent, extra: map[string]any{"image": "nginx"}, changes: true},
		{fqcn: "container.docker.run", fixture: running, extra: map[string]any{"image": "nginx"}},
		{fqcn: "container.docker.stop", fixture: running, changes: true},
		{fqcn: "container.docker.stop", fixture: stopped},
		{fqcn: "container.docker.stop", fixture: absent},
		{fqcn: "container.docker.remove", fixture: stopped, changes: true},
		{fqcn: "container.docker.remove", fixture: running, extra: map[string]any{"force": true}, changes: true},
		{fqcn: "container.docker.remove", fixture: absent},
		{fqcn: "container.docker.remove", fixture: running, cannot: true},
	} {
		t.Run(fmt.Sprintf("%s/%s/%v", tc.fqcn, tc.fixture.status, tc.extra), func(t *testing.T) {
			d, ok := collection.Lookup(tc.fqcn)
			if !ok || !d.Manifest.SupportsCheck || d.Check == nil {
				t.Fatalf("%s does not declare a check", tc.fqcn)
			}
			params := map[string]any{"name": "web"}
			for k, v := range tc.extra {
				params[k] = v
			}
			h := newHarness(t, tc.fixture)
			result, err := d.Check(context.Background(), h.rc, h.device, h.params(params))
			if calls := h.invocations(t); len(calls) != 0 {
				t.Errorf("the check ran %v", calls)
			}
			if tc.cannot {
				var cannot *collection.CannotCheckError
				if !errors.As(err, &cannot) || !strings.Contains(cannot.Reason, "container web is running") {
					t.Fatalf("check = %v, want a CannotCheckError naming the running container", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			if result.Changed != tc.changes {
				t.Errorf("the check predicted Changed = %v, want %v", result.Changed, tc.changes)
			}
			if _, ok := h.rc.stats[sdk.StatInverse]; ok {
				t.Error("the check recorded an undo instruction for a change it never made")
			}

			real := newHarness(t, tc.fixture)
			ran, err := d.Invoke(context.Background(), real.rc, real.device, real.params(params))
			if err != nil {
				t.Fatalf("the real run: %v", err)
			}
			if ran.Changed != result.Changed {
				t.Errorf("the check predicted Changed = %v, the real run reported %v", result.Changed, ran.Changed)
			}
		})
	}
}

// TestChecks_AgainstRealDocker is the control the fake cannot be: each
// check, then the real run, against this machine's real Docker daemon
// through the real SSH path, from the states a container passes through
// (absent, running, stopped). Every key a check's after half states is
// the one the real run leaves, and the check changes nothing, which the
// next check's reading of the same state shows. The refused removal is
// run for real too, to show the real run does fail where the check
// declined to predict.
func TestChecks_AgainstRealDocker(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the real Docker check test in short mode")
	}
	if err := exec.Command("docker", "info").Run(); err != nil { // #nosec G204 -- fixed arguments
		t.Fatalf("this test needs a reachable Docker daemon: %v", err)
	}
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatalf("starting the SSH harness: %v", err)
	}
	t.Cleanup(srv.Close)
	device := &target{
		Stub: &inventorytest.Stub{StubName: "web1", Caps: []capability.Name{capability.NameDocker}},
		host: srv.Host, port: srv.Port,
	}
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	name := "pleiades-check-" + hex.EncodeToString(suffix)
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() }) // #nosec G204 -- a name this test made
	params := map[string]any{
		"insecure_skip_host_key_verify": true,
		"name":                          name,
		"image":                         testsupport.NATSImage,
		"command":                       []any{"sleep", "300"},
	}

	// step checks fqcn from the container's current state, runs it for
	// real, and compares.
	step := func(fqcn string, wantChange bool) {
		t.Helper()
		d, ok := collection.Lookup(fqcn)
		if !ok || d.Check == nil {
			t.Fatalf("%s does not declare a check", fqcn)
		}
		crc := &ctxStub{secrets: srv.Secrets(), stats: map[string]any{}}
		checked, err := d.Check(context.Background(), crc, device, params)
		if err != nil {
			t.Fatalf("%s check: %v", fqcn, err)
		}
		rrc := &ctxStub{secrets: srv.Secrets(), stats: map[string]any{}}
		ran, err := d.Invoke(context.Background(), rrc, device, params)
		if err != nil {
			t.Fatalf("%s real run: %v", fqcn, err)
		}
		if checked.Changed != wantChange || ran.Changed != wantChange {
			t.Errorf("%s: the check predicted Changed = %v and the real run reported %v, want %v", fqcn, checked.Changed, ran.Changed, wantChange)
		}
		checkedBefore, predicted := diffHalves(t, crc)
		realBefore, actual := diffHalves(t, rrc)
		if checkedBefore["exists"] != realBefore["exists"] || checkedBefore["status"] != realBefore["status"] {
			t.Errorf("%s: the check read %v, the real run then read %v; the check changed something", fqcn, checkedBefore, realBefore)
		}
		for key, want := range predicted {
			if actual[key] != want {
				t.Errorf("%s: predicted %s = %v, the real run left %v", fqcn, key, want, actual[key])
			}
		}
	}

	step("container.docker.run", true)
	step("container.docker.run", false)

	// A running container without force: the check declines, and the real
	// run fails, which is the answer the check could not give without
	// knowing whether an earlier task stops it.
	remove, _ := collection.Lookup("container.docker.remove")
	_, err = remove.Check(context.Background(), &ctxStub{secrets: srv.Secrets(), stats: map[string]any{}}, device, params)
	var cannot *collection.CannotCheckError
	if !errors.As(err, &cannot) {
		t.Fatalf("a check of removing a running container without force = %v, want a CannotCheckError", err)
	}
	if _, err := remove.Invoke(context.Background(), &ctxStub{secrets: srv.Secrets(), stats: map[string]any{}}, device, params); err == nil {
		t.Fatal("the real run removed a running container without force, so the check declined for nothing")
	}

	step("container.docker.stop", true)
	step("container.docker.stop", false)
	step("container.docker.remove", true)
	step("container.docker.remove", false)
}

// TestChecks_FailWhenTheyCannotRecord covers a check that cannot record
// its answer, the container's name or the diff: it fails naming the
// method, as the real run fails, rather than reporting a decision with
// nothing behind it, and it still runs nothing but inspect.
func TestChecks_FailWhenTheyCannotRecord(t *testing.T) {
	for _, failKey := range []string{"name", sdk.StatDiff} {
		t.Run(failKey, func(t *testing.T) {
			d, _ := collection.Lookup("container.docker.stop")
			h := newHarness(t, running)
			h.rc.failOnKey = failKey
			_, err := d.Check(context.Background(), h.rc, h.device, h.params(map[string]any{"name": "web"}))
			if err == nil || !strings.HasPrefix(err.Error(), "container.docker.stop: ") {
				t.Errorf("check = %v, want the failure named for container.docker.stop", err)
			}
			if calls := h.invocations(t); len(calls) != 0 {
				t.Errorf("the check ran %v", calls)
			}
		})
	}
}
