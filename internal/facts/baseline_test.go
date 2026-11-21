// Tests of the Crawl CLI's baselines file (Phase 41d): a baseline line is
// bound to its device and place, the file holds sealed lines and nothing
// else, a store with no keys takes and reads none, a baseline is never
// replaced, and a torn last line is ignored and then cut off. The shared
// helpers of the baseline and drift tests are here too.
package facts_test

import (
	"bytes"
	"context"
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
	if _, err := keyless.DriftOf(context.Background(), "dev1", "", baselineAt); !errors.Is(err, facts.ErrNoKeys) {
		t.Errorf("DriftOf with no keys: %v, want ErrNoKeys rather than a report", err)
	}
	for _, name := range []string{"dev2.baselines.jsonl", "dev2.lock"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("a keyless store made %s: %v", name, err)
		}
	}
}

// TestBaseline_EveryLineOpensWhereItIsAndNothingElseIsThere: each line of a
// baselines file opens with OpenLine as its device's baseline line at its
// own byte offset and decodes as the baseline taken, in order, and the file
// holds those lines and nothing else: no header, no line that is not a
// baseline, no byte after the last newline.
func TestBaseline_EveryLineOpensWhereItIsAndNothingElseIsThere(t *testing.T) {
	store, dir := baselineStore(t)
	observe(t, store, "dev1", baselineAt, map[string]any{"os.kernel.release": "6.1", "package.a": "1"})
	names := []string{"first", "second", "third"}
	for _, n := range names {
		take(t, store, "dev1", n, "")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "dev1.baselines.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		t.Fatalf("the file does not end with a whole line: %q", raw[max(0, len(raw)-16):])
	}
	lines := bytes.SplitAfter(raw, []byte("\n"))
	// SplitAfter leaves an empty last element after the final newline, and
	// nothing else may follow it.
	if last := lines[len(lines)-1]; len(last) != 0 {
		t.Fatalf("%d byte(s) after the last line", len(last))
	}
	lines = lines[:len(lines)-1]
	if len(lines) != len(names) {
		t.Fatalf("%d line(s), want one per baseline (%d): the file holds something else", len(lines), len(names))
	}
	var offset int64
	for i, line := range lines {
		body := bytes.TrimSuffix(line, []byte("\n"))
		if bytes.ContainsAny(body, "\n\r") || len(body) == 0 {
			t.Fatalf("line %d is not one sealed line", i+1)
		}
		plain, err := factstest.Keys().OpenLine("dev1", facts.RoleBaseline, offset, body)
		if err != nil {
			t.Fatalf("line %d does not open at offset %d: %v", i+1, offset, err)
		}
		if !bytes.Contains(plain, []byte(`"name":"`+names[i]+`"`)) {
			t.Errorf("line %d opens as %s, want baseline %q", i+1, plain, names[i])
		}
		if b, err := facts.DecodeBaselineLine(factstest.Keys(), "dev1", offset, line); err != nil || b.Name != names[i] {
			t.Errorf("line %d decodes as %q, %v", i+1, b.Name, err)
		}
		offset += int64(len(line))
	}
	if offset != int64(len(raw)) {
		t.Errorf("the lines cover %d of the file's %d bytes", offset, len(raw))
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
