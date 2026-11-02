// Tests of the shared fact rules (facts.go) and the Crawl CLI's file store
// (file.go), against a real directory.
package facts_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
)

// TestHash_IsTheSameWhateverCarriedTheValue: an int in process and the
// float64 it becomes after a child process's JSON hash alike, map order does
// not matter, and a different key or value hashes differently.
func TestHash_IsTheSameWhateverCarriedTheValue(t *testing.T) {
	inProcess := map[string]any{"b": 2, "a": []any{1, "x"}}
	var crossed any
	raw, _ := json.Marshal(inProcess)
	_ = json.Unmarshal(raw, &crossed)
	h1, err1 := facts.Hash("k", inProcess)
	h2, err2 := facts.Hash("k", crossed)
	if err1 != nil || err2 != nil || h1 != h2 || len(h1) != 64 {
		t.Fatalf("hashes %q, %q (%v, %v), want one SHA-256", h1, h2, err1, err2)
	}
	if h3, _ := facts.Hash("other", inProcess); h3 == h1 {
		t.Error("a different key hashed the same")
	}
	if h4, _ := facts.Hash("k", map[string]any{"b": 3, "a": []any{1, "x"}}); h4 == h1 {
		t.Error("a different value hashed the same")
	}
	if _, err := facts.Hash("k", func() {}); err == nil {
		t.Error("a value JSON cannot hold hashed")
	}
}

func TestPrepare_StoresOnlyWhatMayBeStored(t *testing.T) {
	observed := map[string]any{
		"ansible_hostname": "web1",
		"":                 "x",
		"has space":        "x",
		"a/b":              "x",
		"huge":             strings.Repeat("x", facts.MaxValueBytes+1),
		"leaky":            "token=s3cr3t-value",
		"leaky_quoted":     `he said "s3cr3t-value"`,
		"unencodable":      make(chan int),
		"count":            3,
	}
	entries, problems, err := facts.Prepare(observed, []string{"s3cr3t-value", ""})
	if err != nil {
		t.Fatal(err)
	}
	stored := map[string]bool{}
	for _, e := range entries {
		stored[e.Key] = true
		if len(e.Hash) != 64 {
			t.Errorf("%s has hash %q", e.Key, e.Hash)
		}
	}
	if len(entries) != 2 || !stored["ansible_hostname"] || !stored["count"] {
		t.Errorf("stored %v, want ansible_hostname and count", stored)
	}
	refused := map[string]bool{}
	for _, p := range problems {
		refused[p.Key] = true
		if p.Reason == "" {
			t.Errorf("%q refused with no reason", p.Key)
		}
	}
	for _, k := range []string{"", "has space", "a/b", "huge", "leaky", "leaky_quoted", "unencodable"} {
		if !refused[k] {
			t.Errorf("%q was not refused", k)
		}
	}
	tooMany := map[string]any{}
	for i := range facts.MaxFactsPerObservation + 1 {
		tooMany["k"+strings.Repeat("x", i%5)+string(rune('a'+i%26))+strings.Repeat("y", i/26)] = i
	}
	if _, _, err := facts.Prepare(tooMany, nil); !errors.Is(err, facts.ErrInvalid) {
		t.Errorf("an observation past the bound: %v, want ErrInvalid", err)
	}
}

func prepared(t *testing.T, observed map[string]any) []facts.Entry {
	t.Helper()
	entries, problems, err := facts.Prepare(observed, nil)
	if err != nil || len(problems) != 0 {
		t.Fatalf("Prepare: %v, %v", problems, err)
	}
	return entries
}

// TestFileStore_WritesOnChangeAndKeepsHistory: a value seen again moves its
// last-seen time and writes no line; a changed value is a new line; the
// current value is the newest line, and history is every value in order.
func TestFileStore_WritesOnChangeAndKeepsHistory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "facts")
	store, err := facts.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	t0 := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	device := "8d1e3c2c-0670-40cc-a914-e1194ff5857e"
	steps := []struct {
		observed map[string]any
		at       time.Time
	}{
		{map[string]any{"kernel": "6.1", "mem": 2048}, t0},
		{map[string]any{"kernel": "6.1", "mem": 2048}, t0.Add(time.Hour)},
		{map[string]any{"kernel": "6.2"}, t0.Add(2 * time.Hour)},
	}
	for _, step := range steps {
		if err := store.Record(ctx, device, prepared(t, step.observed), step.at); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, device+".jsonl")) // #nosec G304 -- the test's own directory
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(raw), "\n"); lines != 3 {
		t.Errorf("%d lines written, want 3 (kernel twice, mem once): %s", lines, raw)
	}
	current, err := store.Current(ctx, device)
	if err != nil || len(current) != 2 {
		t.Fatalf("Current = %+v, %v", current, err)
	}
	byKey := map[string]facts.Entry{}
	for _, e := range current {
		byKey[e.Key] = e
	}
	if k := byKey["kernel"]; k.Value != "6.2" || !k.ObservedAt.Equal(t0.Add(2*time.Hour)) || !k.LastSeenAt.Equal(t0.Add(2*time.Hour)) {
		t.Errorf("kernel = %+v", k)
	}
	if m := byKey["mem"]; m.Value != json.Number("2048") || !m.ObservedAt.Equal(t0) || !m.LastSeenAt.Equal(t0.Add(time.Hour)) {
		t.Errorf("mem = %+v, want observed at t0 and last seen an hour later", m)
	}
	history, err := store.History(ctx, device, "kernel")
	if err != nil || len(history) != 2 || history[0].Value != "6.1" || history[1].Value != "6.2" ||
		!history[0].LastSeenAt.Equal(t0.Add(2*time.Hour)) {
		t.Errorf("History(kernel) = %+v, %v", history, err)
	}
	if none, err := store.Current(ctx, "other-device"); err != nil || len(none) != 0 {
		t.Errorf("a device with no facts: %+v, %v", none, err)
	}
	if info, err := os.Stat(filepath.Join(dir, device+".jsonl")); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the history file's mode: %v, %v", info, err)
	}
}

// TestFileStore_RefusesWhatItCannotTrust: a device name that is not a file
// name, a link planted where a file goes, and a line that is not a stored
// fact are errors, never written through or skipped.
func TestFileStore_RefusesWhatItCannotTrust(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "facts")
	store, err := facts.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	entries := prepared(t, map[string]any{"k": "v"})
	for _, bad := range []string{"../escape", "", ".hidden", "a/b"} {
		if err := store.Record(ctx, bad, entries, time.Now()); !errors.Is(err, facts.ErrInvalid) {
			t.Errorf("device %q: %v, want ErrInvalid", bad, err)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "linked.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(ctx, "linked", entries, time.Now()); err == nil {
		t.Error("a write went through a link out of the directory")
	}
	if raw, _ := os.ReadFile(outside); len(raw) != 0 { // #nosec G304 -- the test's own file
		t.Errorf("the file outside was written: %q", raw)
	}
	if err := os.WriteFile(filepath.Join(dir, "corrupt.jsonl"), []byte("{\"key\":\"k\",\"hash\":\"h\"}\nnot json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Current(ctx, "corrupt"); err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Errorf("a corrupt history: %v, want line 2 named", err)
	}
}
