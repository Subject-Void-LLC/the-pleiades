// Tests for build.lock.verify against real files on a real SSH session.
package lock_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/build/lock"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

const lockManifest = `{"dependencies":{"left-pad":"^1.3.0"}}`

const lockClean = `{"lockfileVersion":3,"packages":{
 "":{"dependencies":{"left-pad":"^1.3.0"}},
 "node_modules/left-pad":{"version":"1.3.0","resolved":"https://registry.npmjs.org/left-pad/-/left-pad-1.3.0.tgz","integrity":"sha512-AAAA"}}}`

// TestRegistered declares what the design note promises: read-only, and
// a check of it is the task itself.
func TestRegistered(t *testing.T) {
	d, ok := collection.Lookup("build.lock.verify")
	if !ok {
		t.Fatal("build.lock.verify is not registered")
	}
	if r := d.Manifest.Reversibility; !r.ReadOnly || r.Reversible || !d.Manifest.SupportsCheck || d.Check == nil {
		t.Fatalf("build.lock.verify declares %+v, check %v", r, d.Manifest.SupportsCheck)
	}
}

// TestVerifyLock_PassesACleanLockfileAndNamesEachShortfall.
func TestVerifyLock_PassesACleanLockfileAndNamesEachShortfall(t *testing.T) {
	h := newHarness(t)
	h.write(t, "package.json", lockManifest, 0o644)
	lockfile := h.write(t, "package-lock.json", lockClean, 0o644)
	if _, err := lock.Verify(context.Background(), h.rc, h.device, params(map[string]any{"lockfile": lockfile})); err != nil {
		t.Fatalf("a clean lockfile = %v", err)
	}

	h.write(t, "package-lock.json", strings.Replace(lockClean, `,"integrity":"sha512-AAAA"`, "", 1), 0o644)
	_, err := lock.Verify(context.Background(), h.rc, h.device, params(map[string]any{"lockfile": lockfile}))
	if err == nil || !strings.Contains(err.Error(), "left-pad@1.3.0: it has no integrity hash") {
		t.Fatalf("no integrity = %v", err)
	}
	_, err = lock.Verify(context.Background(), h.rc, h.device, params(map[string]any{"lockfile": lockfile, "sources": []any{"npm.example.internal"}}))
	if err == nil || !strings.Contains(err.Error(), "registry.npmjs.org, which is not an allowed source") {
		t.Fatalf("a narrowed source list = %v", err)
	}
}

// TestVerifyLock_FindsTheFormatAndRefusesWhatItCannot: a format named for
// a lockfile it does not recognize by name, and one it cannot read.
func TestVerifyLock_FindsTheFormatAndRefusesWhatItCannot(t *testing.T) {
	h := newHarness(t)
	h.write(t, "deps/package.json", lockManifest, 0o644)
	renamed := h.write(t, "deps/locked.json", lockClean, 0o644)
	if _, err := lock.Verify(context.Background(), h.rc, h.device, params(map[string]any{"lockfile": renamed, "manifest": h.project + "/deps/package.json", "format": "npm"})); err != nil {
		t.Fatalf("a named format = %v", err)
	}
	for name, tc := range map[string]struct {
		params map[string]any
		want   string
	}{
		"an unknown name":   {map[string]any{"lockfile": renamed}, "set format"},
		"an unknown format": {map[string]any{"lockfile": renamed, "format": "yarn"}, "not one this reads"},
		"no manifest":       {map[string]any{"lockfile": h.write(t, "solo/package-lock.json", lockClean, 0o644)}, "does not exist"},
		"version 1":         {map[string]any{"lockfile": h.write(t, "old/package-lock.json", `{"lockfileVersion":1}`, 0o644), "manifest": h.project + "/deps/package.json"}, "lockfile version 1"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := lock.Verify(context.Background(), h.rc, h.device, params(tc.params)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("VerifyLock = %v, want it to say %q", err, tc.want)
			}
		})
	}
}
