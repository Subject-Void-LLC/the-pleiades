// Tests of baselines and drift over the Crawl CLI's file store (Phase 41d):
// a baseline line is bound to its device and place, a golden baseline is
// hashed again for its target, a value keyed for one device is not
// comparable across devices, a volatile key never drifts, a device that
// cannot be compared is unknown, and a baseline is never replaced.
package facts_test

import (
	"bytes"
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

// baselineAt is the fixed time the baseline tests observe and take at.
var baselineAt = time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)

// baselineStore is a file store over a fresh directory, and the directory.
func baselineStore(t *testing.T) (*facts.FileStore, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "facts")
	store, err := facts.NewFileStore(dir, factstest.Keys())
	if err != nil {
		t.Fatal(err)
	}
	return store, dir
}

// observe records observed on device at at, each key stable unless named in
// volatile.
func observe(t *testing.T, store *facts.FileStore, device string, at time.Time, observed map[string]any, volatile ...string) {
	t.Helper()
	entries := prepared(t, observed)
	for i := range entries {
		for _, v := range volatile {
			if entries[i].Key == v {
				entries[i].Kind = facts.KindVolatile
			}
		}
	}
	if err := store.Record(context.Background(), device, facts.Observation{Entries: entries, At: at}); err != nil {
		t.Fatalf("Record on %s: %v", device, err)
	}
}

// take takes a baseline of device named name, from from when it is not empty.
func take(t *testing.T, store *facts.FileStore, device, name, from string) facts.Baseline {
	t.Helper()
	b, err := store.TakeBaseline(context.Background(), device, facts.BaselineRequest{Name: name, Reason: "before the change", By: "the-tester", From: from, At: baselineAt})
	if err != nil {
		t.Fatalf("TakeBaseline %s on %s: %v", name, device, err)
	}
	return b
}

// driftOf is device's drift against its newest baseline.
func driftOf(t *testing.T, store *facts.FileStore, device string) facts.DriftReport {
	t.Helper()
	rep, err := store.DriftOf(context.Background(), device, "", baselineAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("DriftOf %s: %v", device, err)
	}
	return rep
}

// TestBaseline_ALineMovedToAnotherDeviceOrPlaceDoesNotOpen: a baseline line
// copied into another device's file, or two lines swapped so each sits at the
// other's offset, does not open, and the read is an error, never a baseline.
func TestBaseline_ALineMovedToAnotherDeviceOrPlaceDoesNotOpen(t *testing.T) {
	store, dir := baselineStore(t)
	for _, d := range []string{"dev1", "dev2"} {
		observe(t, store, d, baselineAt, map[string]any{"os.kernel.release": "6.1"})
	}
	take(t, store, "dev1", "first", "")
	take(t, store, "dev1", "second-baseline", "")
	src := filepath.Join(dir, "dev1.baselines.jsonl")
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := store.Baselines(context.Background(), "dev1"); err != nil || len(got) != 2 {
		t.Fatalf("the control: Baselines = %d, %v", len(got), err)
	}
	// Copied whole to another device.
	if err := os.WriteFile(filepath.Join(dir, "dev2.baselines.jsonl"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Baselines(context.Background(), "dev2"); !errors.Is(err, facts.ErrSealed) {
		t.Errorf("dev1's baselines read as dev2's: %d, %v; want ErrSealed", len(got), err)
	}
	// Swapped in place: each line at the other's offset.
	lines := bytes.SplitAfter(raw, []byte("\n"))
	swapped := append(append([]byte{}, lines[1]...), lines[0]...)
	if err := os.WriteFile(src, swapped, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Baselines(context.Background(), "dev1"); !errors.Is(err, facts.ErrSealed) {
		t.Errorf("swapped lines read: %d, %v; want ErrSealed", len(got), err)
	}
	// A values line where a baseline belongs does not open as one either.
	values, err := os.ReadFile(filepath.Join(dir, "dev1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := facts.DecodeBaselineLine(factstest.Keys(), "dev1", 0, values); !errors.Is(err, facts.ErrSealed) {
		t.Errorf("a values line decoded as a baseline: %v", err)
	}
}

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

// TestDrift_AVolatileKeyNeverDrifts: a volatile key that changes, or one that
// is volatile now and stable in the baseline, is never compared.
func TestDrift_AVolatileKeyNeverDrifts(t *testing.T) {
	store, _ := baselineStore(t)
	observe(t, store, "dev1", baselineAt, map[string]any{"os.kernel.release": "6.1", "host.uptime_seconds": 10}, "host.uptime_seconds")
	b := take(t, store, "dev1", "base", "")
	for _, p := range b.Pairs {
		if p.Key == "host.uptime_seconds" {
			t.Errorf("the baseline holds a volatile key: %+v", p)
		}
	}
	observe(t, store, "dev1", baselineAt.Add(time.Minute), map[string]any{"os.kernel.release": "6.1", "host.uptime_seconds": 70}, "host.uptime_seconds")
	if rep := driftOf(t, store, "dev1"); rep.State != facts.DriftInSync || len(rep.Changes) != 0 {
		t.Fatalf("a moved uptime drifted: %+v", rep)
	}
	// A baseline that holds the key as stable, against a device that now
	// reports it volatile: still not compared.
	b.Pairs = append(b.Pairs, facts.BaselinePair{Key: "host.uptime_seconds", Hash: "00"})
	current, err := store.Current(context.Background(), "dev1")
	if err != nil {
		t.Fatal(err)
	}
	if rep := facts.Drift(b, factstest.Keys().ID(), current, baselineAt); rep.State != facts.DriftInSync || rep.Compared != 1 {
		t.Errorf("a key volatile now was compared: %+v", rep)
	}
}

// TestDrift_ADeviceThatCannotBeComparedIsUnknown: a device never gathered,
// and a baseline taken under other keys, are unknown, never in sync.
func TestDrift_ADeviceThatCannotBeComparedIsUnknown(t *testing.T) {
	store, _ := baselineStore(t)
	observe(t, store, "dev1", baselineAt, map[string]any{"os.kernel.release": "6.1"})
	b := take(t, store, "dev1", "base", "")
	if rep := facts.Drift(b, factstest.Keys().ID(), nil, baselineAt); rep.State != facts.DriftUnknown {
		t.Errorf("never gathered: %+v, want unknown", rep)
	}
	current, err := store.Current(context.Background(), "dev1")
	if err != nil {
		t.Fatal(err)
	}
	other, err := facts.NewKeys(bytes.Repeat([]byte{7}, 32), "test")
	if err != nil {
		t.Fatal(err)
	}
	if rep := facts.Drift(b, other.ID(), current, baselineAt); rep.State != facts.DriftUnknown {
		t.Errorf("a baseline under other keys: %+v, want unknown", rep)
	}
	if rep := facts.Drift(b, factstest.Keys().ID(), current, baselineAt); rep.State != facts.DriftInSync {
		t.Errorf("the control: %+v, want in_sync", rep)
	}
	// A device with no baseline at all says so.
	observe(t, store, "dev2", baselineAt, map[string]any{"os.kernel.release": "6.1"})
	if rep := driftOf(t, store, "dev2"); rep.State != facts.DriftNoBaseline {
		t.Errorf("no baseline: %+v", rep)
	}
	// Neither can be taken of a device never gathered.
	if _, err := store.TakeBaseline(context.Background(), "dev3", facts.BaselineRequest{Name: "b", Reason: "r", By: "u", At: baselineAt}); !errors.Is(err, facts.ErrNeverGathered) {
		t.Errorf("a baseline of a device never gathered: %v", err)
	}
	if _, err := store.TakeBaseline(context.Background(), "dev1", facts.BaselineRequest{Name: "b", Reason: "r", By: "u", From: "dev3", At: baselineAt}); !errors.Is(err, facts.ErrNeverGathered) {
		t.Errorf("a baseline from a source never gathered: %v", err)
	}
}

// TestBaseline_HoldsNoPlaintextAndNeedsKeys: the baselines file names no key
// and no value in the clear, and a store with no keys takes and reads none
// and creates no file.
func TestBaseline_HoldsNoPlaintextAndNeedsKeys(t *testing.T) {
	store, dir := baselineStore(t)
	observe(t, store, "dev1", baselineAt, map[string]any{"package.zq9sealed": "1.2.3-zq9", "os.family": "alpine"})
	take(t, store, "dev1", "base", "")
	raw, err := os.ReadFile(filepath.Join(dir, "dev1.baselines.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// Each needle holds a character base64 never writes ('.', '-' or a
	// space), so a sealed line cannot hold one by chance.
	for _, plain := range []string{"package.zq9sealed", "1.2.3-zq9", "os.family", "before the change", "the-tester"} {
		if bytes.Contains(raw, []byte(plain)) {
			t.Errorf("the baselines file holds %q in plaintext", plain)
		}
	}
	keyless, err := facts.OpenFileStore(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := keyless.TakeBaseline(context.Background(), "dev1", facts.BaselineRequest{Name: "other", Reason: "r", By: "u", At: baselineAt}); !errors.Is(err, facts.ErrNoKeys) {
		t.Errorf("TakeBaseline with no keys: %v", err)
	}
	if _, err := keyless.Baselines(context.Background(), "dev2"); !errors.Is(err, facts.ErrNoKeys) {
		t.Errorf("Baselines with no keys: %v", err)
	}
	for _, name := range []string{"dev2.baselines.jsonl", "dev2.lock"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("a keyless store made %s: %v", name, err)
		}
	}
}

// TestBaseline_ANameIsNeverRetaken: taking a baseline under a name the device
// has is refused, and the earlier line is byte for byte as it was.
func TestBaseline_ANameIsNeverRetaken(t *testing.T) {
	store, dir := baselineStore(t)
	observe(t, store, "dev1", baselineAt, map[string]any{"os.kernel.release": "6.1"})
	take(t, store, "dev1", "base", "")
	path := filepath.Join(dir, "dev1.baselines.jsonl")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	observe(t, store, "dev1", baselineAt.Add(time.Minute), map[string]any{"os.kernel.release": "6.2"})
	if _, err := store.TakeBaseline(context.Background(), "dev1", facts.BaselineRequest{Name: "base", Reason: "again", By: "u", At: baselineAt}); !errors.Is(err, facts.ErrBaselineExists) {
		t.Fatalf("retaking a name: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("a refused retake changed the baselines file")
	}
	if b, err := store.Baseline(context.Background(), "dev1", "base"); err != nil || b.Pairs[0].Value != "6.1" {
		t.Errorf("the baseline after a refused retake = %+v, %v", b, err)
	}
}

// TestBaseline_ATornLastLineIsIgnoredThenCut: a line a crash left unfinished
// is not read, and the next baseline taken cuts it off before writing.
func TestBaseline_ATornLastLineIsIgnoredThenCut(t *testing.T) {
	store, dir := baselineStore(t)
	observe(t, store, "dev1", baselineAt, map[string]any{"os.kernel.release": "6.1"})
	take(t, store, "dev1", "first", "")
	path := filepath.Join(dir, "dev1.baselines.jsonl")
	whole, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Longer than the line the next writer appends, so only cutting it off,
	// not writing over it, leaves none of it behind.
	torn := append(append([]byte{}, whole...), []byte("v1."+strings.Repeat("A", 8000)+"torn")...)
	if err := os.WriteFile(path, torn, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Baselines(context.Background(), "dev1"); err != nil || len(got) != 1 {
		t.Fatalf("with a torn last line: %d baselines, %v; want the one whole", len(got), err)
	}
	take(t, store, "dev1", "second", "")
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(after, whole) || bytes.Contains(after, []byte("torn")) {
		t.Error("the next writer did not cut the torn line off")
	}
	if got, err := store.Baselines(context.Background(), "dev1"); err != nil || len(got) != 2 || got[1].Name != "second" {
		t.Errorf("after the cut: %+v, %v", got, err)
	}
	if b, err := store.Baseline(context.Background(), "dev1", ""); err != nil || b.Name != "second" {
		t.Errorf("the active baseline = %q, %v; want the newest", b.Name, err)
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
// not shown.
func TestBaseline_AKeyedDigestIsNotComparableAcrossDevices(t *testing.T) {
	store, _ := baselineStore(t)
	const key = "file.%2Fetc%2Fshadow"
	observe(t, store, "golden", baselineAt, map[string]any{key: keyedFile(t, "golden", key, "s3cret"), "os.family": "alpine"})
	observe(t, store, "web1", baselineAt, map[string]any{key: keyedFile(t, "web1", key, "s3cret"), "os.family": "alpine"})
	take(t, store, "web1", "gold", "golden")
	rep := driftOf(t, store, "web1")
	if rep.State != facts.DriftInSync || len(rep.NotComparable) != 1 || rep.NotComparable[0] != key || rep.Compared != 1 {
		t.Fatalf("a keyed digest under a golden baseline: %+v", rep)
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

// TestBaseline_KeysSelectWhatIsTakenAndCompared: --keys takes only the keys
// it names, and an added key it does not cover is not drift.
func TestBaseline_KeysSelectWhatIsTakenAndCompared(t *testing.T) {
	store, _ := baselineStore(t)
	observe(t, store, "dev1", baselineAt, map[string]any{"package.a": "1", "package.b": "2", "os.family": "alpine"})
	sel, err := facts.ParseKeySelector("package.*")
	if err != nil {
		t.Fatal(err)
	}
	b, err := store.TakeBaseline(context.Background(), "dev1", facts.BaselineRequest{Name: "pkgs", Reason: "r", By: "u", Keys: sel, At: baselineAt})
	if err != nil || len(b.Pairs) != 2 {
		t.Fatalf("a package baseline = %+v, %v", b, err)
	}
	observe(t, store, "dev1", baselineAt.Add(time.Minute), map[string]any{"package.a": "1", "package.c": "3", "os.family": "alpine", "os.kernel.release": "6.1"})
	rep := driftOf(t, store, "dev1")
	var got []string
	for _, c := range rep.Changes {
		got = append(got, string(c.Change)+" "+c.Key)
	}
	// package.b, which the second gather did not report, keeps its last value
	// (a store keeps no absence), so it is unconfirmed rather than removed.
	if len(got) != 1 || got[0] != "added package.c" || len(rep.Unconfirmed) != 1 || rep.Unconfirmed[0] != "package.b" {
		t.Errorf("changes = %v, unconfirmed %v; want package.c added and package.b unconfirmed", got, rep.Unconfirmed)
	}
	// A golden key the target has never reported is removed.
	observe(t, store, "golden", baselineAt, map[string]any{"package.a": "1", "package.z": "9"})
	b, err = store.TakeBaseline(context.Background(), "dev1", facts.BaselineRequest{Name: "gold", Reason: "r", By: "u", From: "golden", Keys: sel, At: baselineAt})
	if err != nil {
		t.Fatal(err)
	}
	rep = driftOf(t, store, "dev1")
	got = nil
	for _, c := range rep.Changes {
		got = append(got, string(c.Change)+" "+c.Key)
	}
	if len(got) != 3 || got[0] != "added package.b" || got[1] != "added package.c" || got[2] != "removed package.z" {
		t.Errorf("against the golden baseline: %v", got)
	}
	if c := rep.Changes[2]; c.BaselineValue != "9" || c.CurrentValue != nil || c.LastSeenAt != nil {
		t.Errorf("the removed key = %+v", c)
	}
	for _, bad := range []string{"", "a,,b", "pa*ck", "a b"} {
		if _, err := facts.ParseKeySelector(bad); bad != "" && err == nil {
			t.Errorf("ParseKeySelector(%q) accepted", bad)
		}
	}
}

// TestBaseline_RefusesABadRequest: a name, a reason or a taker that is not
// text a report can show is refused before anything is written.
func TestBaseline_RefusesABadRequest(t *testing.T) {
	store, dir := baselineStore(t)
	observe(t, store, "dev1", baselineAt, map[string]any{"os.family": "alpine"})
	for _, req := range []facts.BaselineRequest{
		{Name: "", Reason: "r", By: "u"},
		{Name: "-x", Reason: "r", By: "u"},
		{Name: "a/b", Reason: "r", By: "u"},
		{Name: "ok", Reason: " ", By: "u"},
		{Name: "ok", Reason: "line\nbreak", By: "u"},
		{Name: "ok", Reason: "r", By: ""},
	} {
		if _, err := store.TakeBaseline(context.Background(), "dev1", req); !errors.Is(err, facts.ErrInvalid) {
			t.Errorf("TakeBaseline(%+v) = %v, want ErrInvalid", req, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "dev1.baselines.jsonl")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused request wrote the file: %v", err)
	}
}
