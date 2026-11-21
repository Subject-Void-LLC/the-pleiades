// Tests of taking a baseline (Phase 41d): a golden baseline is hashed again
// for its target, a value keyed for one device is not comparable across
// devices, a baseline holding nothing comparable is refused, a key pattern
// matching nothing is refused by name, and a plan writes nothing.
package facts_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/factstest"
)

// TestBaseline_GoldenIsHashedForItsTarget: two devices with the same values
// read in sync against a baseline of one taken from the other, because each
// pair is hashed again for the target; the source's own hashes differ from
// the target's, so a copy of them would read every key changed.
func TestBaseline_GoldenIsHashedForItsTarget(t *testing.T) {
	store, _ := baselineStore(t)
	same := map[string]any{"os.kernel.release": "6.1", "package.busybox": map[string]any{"version": "1.36", "source": "apk"}}
	observe(t, store, "golden", baselineAt, same)
	observe(t, store, "web1", baselineAt, same)
	b := take(t, store, "web1", "gold", "golden")
	if b.From != "golden" || b.Device != "web1" || len(b.Pairs) != 2 {
		t.Fatalf("golden baseline = %+v", b)
	}
	if rep := driftOf(t, store, "web1"); rep.State != facts.DriftInSync || rep.Matching != 2 || len(rep.Changes) != 0 {
		t.Fatalf("equal values against a golden baseline: %+v", rep)
	}
	// What a copy of the source's hashes would have held: never the target's.
	src, err := store.Current(context.Background(), "golden")
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range src {
		if facts.SameMAC(e.Hash, b.Pairs[i].Hash) {
			t.Errorf("%s: the golden pair holds the source's own hash", e.Key)
		}
	}
	// And a real difference still drifts.
	observe(t, store, "web1", baselineAt.Add(time.Minute), map[string]any{"os.kernel.release": "6.2", "package.busybox": same["package.busybox"]})
	rep := driftOf(t, store, "web1")
	if rep.State != facts.DriftDrifted || len(rep.Changes) != 1 || rep.Changes[0].Key != "os.kernel.release" || rep.Changes[0].Change != facts.ChangeChanged {
		t.Fatalf("a changed kernel against the golden baseline: %+v", rep)
	}
	if c := rep.Changes[0]; c.BaselineValue != "6.1" || c.CurrentValue != "6.2" || c.Digest || c.AgeSeconds == nil {
		t.Errorf("the change = %+v, want 6.1 to 6.2 shown with its age", c)
	}
}

// keyedFile is a file fact's value with its digest keyed for device, as the
// engine keys a private file's digest.
func keyedFile(t *testing.T, device, key, content string) map[string]any {
	t.Helper()
	keyer, err := facts.NewKeyer(factstest.Secret, "test")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	v := keyer.KeyRaw(device, key, map[string]any{"state": "present", "world_readable": false, facts.RawDigestField: hex.EncodeToString(sum[:])})
	obj, _ := v.(map[string]any)
	if obj[facts.KeyedDigestField] == nil {
		t.Fatalf("no keyed digest in %v", v)
	}
	return obj
}

// TestBaseline_AKeyedDigestIsNotComparableAcrossDevices: the same private
// file on two devices carries two digests, each keyed for its device, so a
// golden baseline records it not comparable and never reports it as drift;
// on its own device it is compared, and a change to it drifts with its value
// not shown. A golden baseline of nothing but such values is refused, since
// it would read in sync whatever changed.
func TestBaseline_AKeyedDigestIsNotComparableAcrossDevices(t *testing.T) {
	store, dir := baselineStore(t)
	const key = "file.%2Fetc%2Fshadow"
	observe(t, store, "golden", baselineAt, map[string]any{key: keyedFile(t, "golden", key, "s3cret"), "os.family": "alpine"})
	observe(t, store, "web1", baselineAt, map[string]any{key: keyedFile(t, "web1", key, "s3cret"), "os.family": "alpine"})
	take(t, store, "web1", "gold", "golden")
	rep := driftOf(t, store, "web1")
	if rep.State != facts.DriftInSync || len(rep.NotComparable) != 1 || rep.NotComparable[0] != key || rep.Compared != 1 {
		t.Fatalf("a keyed digest under a golden baseline: %+v", rep)
	}
	// Only private files: nothing the baseline holds could be compared.
	sel, err := facts.ParseKeySelector("file.*")
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "web1.baselines.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.TakeBaseline(context.Background(), "web1", facts.BaselineRequest{Name: "files", Reason: "r", By: "u", From: "golden", Keys: sel, At: baselineAt})
	if !errors.Is(err, facts.ErrInvalid) || !strings.Contains(err.Error(), "none of the 1 fact(s) selected can be compared") {
		t.Errorf("a golden baseline of private files alone: %v, want refused naming the one not comparable", err)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, "web1.baselines.jsonl")); string(after) != string(before) {
		t.Error("the refused baseline was written")
	}
	// On its own device the same value is compared, and a change shows as
	// changed with neither value shown.
	take(t, store, "web1", "own", "")
	if rep := driftOf(t, store, "web1"); rep.State != facts.DriftInSync || rep.Compared != 2 || len(rep.NotComparable) != 0 {
		t.Fatalf("its own device's baseline: %+v", rep)
	}
	observe(t, store, "web1", baselineAt.Add(time.Minute), map[string]any{key: keyedFile(t, "web1", key, "changed"), "os.family": "alpine"})
	rep = driftOf(t, store, "web1")
	if rep.State != facts.DriftDrifted || len(rep.Changes) != 1 {
		t.Fatalf("a changed private file: %+v", rep)
	}
	if c := rep.Changes[0]; c.Key != key || !c.Digest || c.BaselineValue != nil || c.CurrentValue != nil || c.BaselineHash == "" || c.CurrentHash == "" {
		t.Errorf("the digest's change = %+v, want the key and its hashes with no value", c)
	}
}

// TestBaseline_KeysSelectWhatIsTaken: --keys takes only the keys it names, a
// pattern that matches no stable fact is refused by name even beside others
// that match, and a malformed pattern is refused.
func TestBaseline_KeysSelectWhatIsTaken(t *testing.T) {
	store, _ := baselineStore(t)
	observe(t, store, "dev1", baselineAt, map[string]any{"package.a": "1", "package.b": "2", "os.family": "alpine", "host.uptime": 5}, "host.uptime")
	sel, err := facts.ParseKeySelector("package.*")
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.TakeBaseline(context.Background(), "dev1", facts.BaselineRequest{Name: "pkgs", Reason: "r", By: "u", Keys: sel, At: baselineAt})
	if err != nil || len(b.Pairs) != 2 || len(b.Volatile) != 0 {
		t.Fatalf("a package baseline = %+v, %v", b, err)
	}
	for _, list := range []string{"pacakge.*,os.family", "os.family,host.uptime", "nope"} {
		sel, err := facts.ParseKeySelector(list)
		if err != nil {
			t.Fatal(err)
		}
		bad := strings.Split(list, ",")[0]
		if list == "os.family,host.uptime" {
			// A pattern matching only a volatile fact takes nothing either.
			bad = "host.uptime"
		}
		_, err = store.TakeBaseline(context.Background(), "dev1", facts.BaselineRequest{Name: "typo", Reason: "r", By: "u", Keys: sel, At: baselineAt})
		if !errors.Is(err, facts.ErrInvalid) || !strings.Contains(err.Error(), `"`+bad+`" matches no stable fact`) {
			t.Errorf("--keys %s: %v, want %q refused by name", list, err, bad)
		}
	}
	if got, err := store.Baselines(context.Background(), "dev1"); err != nil || len(got) != 1 {
		t.Errorf("after the refused patterns: %d baselines, %v; want the one", len(got), err)
	}
	for _, bad := range []string{"a,,b", "pa*ck", "a b"} {
		if _, err := facts.ParseKeySelector(bad); err == nil {
			t.Errorf("ParseKeySelector(%q) accepted", bad)
		}
	}
}

// TestBaseline_APlanWritesNothing: PlanBaseline makes every refusal before a
// write, a name the device already has among them, and writes no file;
// StoreBaseline writes what it planned, and refuses a plan it did not make
// or a name another taker stored since.
func TestBaseline_APlanWritesNothing(t *testing.T) {
	store, dir := baselineStore(t)
	observe(t, store, "dev1", baselineAt, map[string]any{"os.family": "alpine"})
	req := facts.BaselineRequest{Name: "base", Reason: "r", By: "u", At: baselineAt}
	p, err := store.PlanBaseline(context.Background(), "dev1", req)
	if err != nil || p.Baseline.Name != "base" || len(p.Baseline.Pairs) != 1 {
		t.Fatalf("PlanBaseline = %+v, %v", p.Baseline, err)
	}
	path := filepath.Join(dir, "dev1.baselines.jsonl")
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a plan wrote the baselines file: %v", err)
	}
	if err := store.StoreBaseline(context.Background(), facts.PlannedBaseline{Baseline: p.Baseline}); !errors.Is(err, facts.ErrInvalid) {
		t.Errorf("a plan PlanBaseline did not make was stored: %v", err)
	}
	// Another taker stores the name between the plan and the write.
	take(t, store, "dev1", "base", "")
	if err := store.StoreBaseline(context.Background(), p); !errors.Is(err, facts.ErrBaselineExists) {
		t.Errorf("a plan stored over a name taken since: %v", err)
	}
	if _, err := store.PlanBaseline(context.Background(), "dev1", req); !errors.Is(err, facts.ErrBaselineExists) {
		t.Errorf("a plan of a name the device has: %v", err)
	}
	if got, err := store.Baselines(context.Background(), "dev1"); err != nil || len(got) != 1 {
		t.Errorf("%d baselines, %v; want the one", len(got), err)
	}
}
