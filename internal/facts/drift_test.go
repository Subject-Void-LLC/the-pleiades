// Tests of drift (Phase 41d): a volatile key never drifts and a key whose
// kind changed is counted apart, a device that cannot be compared is
// unknown, every difference is held to the latest gather of its family, and
// a value that is a digest is named, never shown.
package facts_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// changesOf is rep's changes as "change key".
func changesOf(rep facts.DriftReport) []string {
	var got []string
	for _, c := range rep.Changes {
		got = append(got, string(c.Change)+" "+c.Key)
	}
	return got
}

// unconfirmedOf is rep's unconfirmed keys as "key would_be".
func unconfirmedOf(rep facts.DriftReport) []string {
	var got []string
	for _, u := range rep.Unconfirmed {
		got = append(got, u.Key+" "+string(u.WouldBe))
	}
	return got
}

// TestDrift_AVolatileKeyNeverDrifts: a volatile key that changes is never
// compared; a key stable in the baseline and volatile now is listed in
// KindChanged rather than skipped unseen; and a key volatile when the
// baseline was taken and stable now is never added.
func TestDrift_AVolatileKeyNeverDrifts(t *testing.T) {
	store, _ := baselineStore(t)
	observe(t, store, "dev1", baselineAt, map[string]any{"os.kernel.release": "6.1", "host.uptime_seconds": 10, "host.boot_id": "a"}, "host.uptime_seconds", "host.boot_id")
	b := take(t, store, "dev1", "base", "")
	if len(b.Pairs) != 1 || strings.Join(b.Volatile, ",") != "host.boot_id,host.uptime_seconds" {
		t.Fatalf("the baseline = pairs %+v, volatile %v; want the kernel alone, and both volatile keys named", b.Pairs, b.Volatile)
	}
	observe(t, store, "dev1", baselineAt.Add(time.Minute), map[string]any{"os.kernel.release": "6.1", "host.uptime_seconds": 70}, "host.uptime_seconds")
	// host.boot_id, volatile then, is reported stable now.
	observe(t, store, "dev1", baselineAt.Add(time.Minute), map[string]any{"host.boot_id": "b"})
	rep := driftOf(t, store, "dev1")
	if rep.State != facts.DriftInSync || len(rep.Changes) != 0 || strings.Join(rep.KindChanged, ",") != "host.boot_id" {
		t.Fatalf("a moved uptime, and a boot id volatile at the baseline: %+v", rep)
	}
	// A baseline that holds a key as stable, against a device that now
	// reports it volatile: not compared, and counted.
	b.Pairs = append(b.Pairs, facts.BaselinePair{Key: "host.uptime_seconds", Hash: "00"})
	b.Volatile = nil
	current, err := store.Current(context.Background(), "dev1")
	if err != nil {
		t.Fatal(err)
	}
	rep = facts.Drift(b, current, baselineAt)
	if rep.Compared != 1 || strings.Join(rep.KindChanged, ",") != "host.uptime_seconds" || strings.Join(changesOf(rep), ",") != "added host.boot_id" {
		t.Errorf("a key volatile now, and one with no record of its kind: %+v", rep)
	}
}

// TestDrift_ADeviceThatCannotBeComparedIsUnknown: a device never gathered,
// one whose baseline's every key is volatile now, and one whose files do
// not open are unknown with the reason, never in sync; a device with no
// baseline says so.
func TestDrift_ADeviceThatCannotBeComparedIsUnknown(t *testing.T) {
	store, dir := baselineStore(t)
	observe(t, store, "dev1", baselineAt, map[string]any{"os.kernel.release": "6.1"})
	b := take(t, store, "dev1", "base", "")
	if rep := facts.Drift(b, nil, baselineAt); rep.State != facts.DriftUnknown {
		t.Errorf("never gathered: %+v, want unknown", rep)
	}
	if rep := driftOf(t, store, "dev1"); rep.State != facts.DriftInSync {
		t.Errorf("the control: %+v, want in_sync", rep)
	}
	// Every key of the baseline volatile now: nothing was compared.
	observe(t, store, "dev1", baselineAt.Add(time.Minute), map[string]any{"os.kernel.release": "6.2"}, "os.kernel.release")
	if rep := driftOf(t, store, "dev1"); rep.State != facts.DriftUnknown || rep.Compared != 0 || !strings.Contains(rep.Reason, "no key of the baseline could be compared (0 not comparable, 1 volatile now)") {
		t.Errorf("nothing compared: %+v, want unknown saying why", rep)
	}
	// A device with no baseline at all says so.
	observe(t, store, "dev2", baselineAt, map[string]any{"os.kernel.release": "6.1"})
	if rep := driftOf(t, store, "dev2"); rep.State != facts.DriftNoBaseline {
		t.Errorf("no baseline: %+v", rep)
	}
	take(t, store, "dev2", "base", "")
	// Its facts file edited: its own unknown, the reason naming the file.
	path := filepath.Join(dir, "dev2.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)/2] ^= 1
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	rep := driftOf(t, store, "dev2")
	if rep.State != facts.DriftUnknown || !strings.Contains(rep.Reason, "its facts could not be read") || !strings.Contains(rep.Reason, "sealed under another master key, edited, or moved") || rep.Baseline == nil {
		t.Errorf("an edited facts file: %+v", rep)
	}
	// Its baselines file in plaintext, as a release before sealing wrote:
	// the distinct words a file from before sealing is refused with.
	if err := os.WriteFile(filepath.Join(dir, "dev2.baselines.jsonl"), []byte(`{"name":"base"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep = driftOf(t, store, "dev2")
	if rep.State != facts.DriftUnknown || !strings.Contains(rep.Reason, "its baselines could not be read") || !strings.Contains(rep.Reason, "written before stored facts were sealed") || strings.Contains(rep.Reason, "another master key") {
		t.Errorf("a plaintext baselines file: %+v", rep)
	}
	// Neither can be taken of a device never gathered.
	if _, err := store.TakeBaseline(context.Background(), "dev3", facts.BaselineRequest{Name: "b", Reason: "r", By: "u", At: baselineAt}); !errors.Is(err, facts.ErrNeverGathered) {
		t.Errorf("a baseline of a device never gathered: %v", err)
	}
	if _, err := store.TakeBaseline(context.Background(), "dev1", facts.BaselineRequest{Name: "b", Reason: "r", By: "u", From: "dev3", At: baselineAt}); !errors.Is(err, facts.ErrNeverGathered) {
		t.Errorf("a baseline from a source never gathered: %v", err)
	}
}

// TestDrift_AKeyTheLatestGatherDidNotConfirmIsNotDrift: the store never
// drops a key, so a package uninstalled after the baseline keeps its last
// value. Every key the latest gather of its family did not confirm is listed
// as unconfirmed with what it would have been (added, changed, matching),
// and is not drift: a host whose only differences are unconfirmed is in
// sync, and one with a confirmed difference still drifts.
func TestDrift_AKeyTheLatestGatherDidNotConfirmIsNotDrift(t *testing.T) {
	store, _ := baselineStore(t)
	sel, err := facts.ParseKeySelector("package.*")
	if err != nil {
		t.Fatal(err)
	}
	observe(t, store, "dev1", baselineAt, map[string]any{"package.a": "1", "package.b": "2", "os.family": "alpine"})
	if _, err := store.TakeBaseline(context.Background(), "dev1", facts.BaselineRequest{Name: "pkgs", Reason: "r", By: "u", Keys: sel, At: baselineAt}); err != nil {
		t.Fatal(err)
	}
	// package.c installed, then removed again, and package.b removed: the
	// latest gather reports package.a alone.
	observe(t, store, "dev1", baselineAt.Add(time.Minute), map[string]any{"package.a": "1", "package.b": "2", "package.c": "3"})
	observe(t, store, "dev1", baselineAt.Add(2*time.Minute), map[string]any{"package.a": "1"})
	rep := driftOf(t, store, "dev1")
	if rep.State != facts.DriftInSync || len(rep.Changes) != 0 || rep.Compared != 2 || rep.Matching != 1 {
		t.Fatalf("only unconfirmed differences: %+v, want in_sync", rep)
	}
	if got := strings.Join(unconfirmedOf(rep), ","); got != "package.b matching,package.c added" {
		t.Errorf("unconfirmed = %s", got)
	}
	if u := rep.Unconfirmed[0]; u.LastSeenAt == nil || u.AgeSeconds == nil {
		t.Errorf("an unconfirmed key without when it was last seen: %+v", u)
	}
	// A confirmed added key still drifts, beside the unconfirmed ones.
	observe(t, store, "dev1", baselineAt.Add(3*time.Minute), map[string]any{"package.a": "1", "package.d": "4"})
	rep = driftOf(t, store, "dev1")
	if rep.State != facts.DriftDrifted || strings.Join(changesOf(rep), ",") != "added package.d" || strings.Join(unconfirmedOf(rep), ",") != "package.b matching,package.c added" {
		t.Errorf("a confirmed added key: %+v", rep)
	}
	// A value changed and then no longer reported would be changed, and is
	// not drift either.
	observe(t, store, "dev2", baselineAt, map[string]any{"package.a": "1", "package.b": "2"})
	take(t, store, "dev2", "base", "")
	observe(t, store, "dev2", baselineAt.Add(time.Minute), map[string]any{"package.a": "1", "package.b": "3"})
	observe(t, store, "dev2", baselineAt.Add(2*time.Minute), map[string]any{"package.a": "1"})
	if rep := driftOf(t, store, "dev2"); rep.State != facts.DriftInSync || strings.Join(unconfirmedOf(rep), ",") != "package.b changed" {
		t.Errorf("a changed key no longer reported: %+v", rep)
	}
	// A golden key the target has never reported is the one removal a store
	// can show.
	observe(t, store, "golden", baselineAt, map[string]any{"package.a": "1", "package.z": "9"})
	if _, err := store.TakeBaseline(context.Background(), "dev1", facts.BaselineRequest{Name: "gold", Reason: "r", By: "u", From: "golden", Keys: sel, At: baselineAt}); err != nil {
		t.Fatal(err)
	}
	rep = driftOf(t, store, "dev1")
	if got := strings.Join(changesOf(rep), ","); got != "added package.d,removed package.z" {
		t.Errorf("against the golden baseline: %s", got)
	}
	if c := rep.Changes[1]; c.BaselineValue != "9" || c.CurrentValue != nil || c.LastSeenAt != nil {
		t.Errorf("the removed key = %+v", c)
	}
}

// TestDrift_ADigestKeyIsNamedNeverShown: a fact whose name ends in _digest
// is a digest, so its change is named with its hashes and neither value.
func TestDrift_ADigestKeyIsNamedNeverShown(t *testing.T) {
	store, _ := baselineStore(t)
	observe(t, store, "dev1", baselineAt, map[string]any{"config.running_digest": "aaaa", "os.family": "alpine"})
	take(t, store, "dev1", "base", "")
	observe(t, store, "dev1", baselineAt.Add(time.Minute), map[string]any{"config.running_digest": "bbbb", "os.family": "debian"})
	rep := driftOf(t, store, "dev1")
	if len(rep.Changes) != 2 {
		t.Fatalf("changes = %+v", rep.Changes)
	}
	if c := rep.Changes[0]; c.Key != "config.running_digest" || !c.Digest || c.BaselineValue != nil || c.CurrentValue != nil || c.CurrentHash == "" {
		t.Errorf("the digest key's change = %+v, want named with no value", c)
	}
	if c := rep.Changes[1]; c.Digest || c.CurrentValue != "debian" {
		t.Errorf("an ordinary key's change = %+v, want its value shown", c)
	}
	for _, key := range []string{"gnmi.models_digest", "yang.modules_digest", "x.digest"} {
		if !facts.DigestKey(key) {
			t.Errorf("DigestKey(%q) = false", key)
		}
	}
	if facts.DigestKey("package.digest-tools") {
		t.Error("a package named like a digest read as one")
	}
}
