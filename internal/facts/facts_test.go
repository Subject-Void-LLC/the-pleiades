// Tests of the shared fact rules (facts.go) and the Crawl CLI's file store
// (file.go), against a real directory.
package facts_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
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
	entries, problems, err := facts.Prepare(observed, facts.Guard{Secrets: []string{"s3cr3t-value", ""}})
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
	if _, _, err := facts.Prepare(tooMany, facts.Guard{}); !errors.Is(err, facts.ErrInvalid) {
		t.Errorf("an observation past the bound: %v, want ErrInvalid", err)
	}
}

func prepared(t *testing.T, observed map[string]any) []facts.Entry {
	t.Helper()
	entries, problems, err := facts.Prepare(observed, facts.Guard{})
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
	// Each value's own last confirmation: 6.1 was seen at t0 and an hour
	// later, never at the moment 6.2 was.
	history, err := store.History(ctx, device, "kernel")
	if err != nil || len(history) != 2 || history[0].Value != "6.1" || history[1].Value != "6.2" ||
		!history[0].LastSeenAt.Equal(t0.Add(time.Hour)) {
		t.Errorf("History(kernel) = %+v, %v", history, err)
	}
	if none, err := store.Current(ctx, "other-device"); err != nil || len(none) != 0 {
		t.Errorf("a device with no facts: %+v, %v", none, err)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(filepath.Join(dir, device+".jsonl")); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("the history file's mode: %v, %v", info, err)
		}
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
	if runtime.GOOS == "windows" {
		t.Skip("needs symbolic links, which Windows grants only to an elevated or developer-mode account")
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

// TestVerify_RechecksWhatARunnerPublished: a published entry is held to the
// rules again, a hash that does not describe its value is dropped, and a key
// given twice counts once.
func TestVerify_RechecksWhatARunnerPublished(t *testing.T) {
	good := prepared(t, map[string]any{"kernel": "6.1", "mem": 2048})
	published := append([]facts.Entry{}, good...)
	published = append(published,
		facts.Entry{Key: "kernel", Value: "6.9", Hash: good[0].Hash},
		facts.Entry{Key: "bad key", Value: "x", Hash: "x"},
		facts.Entry{Key: "forged", Value: "x", Hash: "0000"},
	)
	out, refused, err := facts.Verify(published, facts.Guard{})
	if err != nil || len(out) != 2 || refused != 3 {
		t.Fatalf("Verify = %+v, refused %d, %v; want the two good entries and three refused", out, refused, err)
	}
	for _, e := range out {
		if e.Key != "kernel" && e.Key != "mem" {
			t.Errorf("Verify passed %+v", e)
		}
	}
	many := make([]facts.Entry, 0, facts.MaxFactsPerObservation+1)
	for i := range facts.MaxFactsPerObservation + 1 {
		many = append(many, facts.Entry{Key: "k" + strings.Repeat("x", i), Value: i})
	}
	if _, _, err := facts.Verify(many, facts.Guard{}); !errors.Is(err, facts.ErrInvalid) {
		t.Errorf("a batch past the bound: %v", err)
	}
}

// TestFileStore_ALateObservationDoesNotReplaceANewerOne: a value observed
// earlier than the one confirmed after it, recorded late, joins the history
// without becoming current.
func TestFileStore_ALateObservationDoesNotReplaceANewerOne(t *testing.T) {
	store, err := facts.NewFileStore(filepath.Join(t.TempDir(), "facts"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	t0 := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	_ = store.Record(ctx, "dev", prepared(t, map[string]any{"k": "A"}), t0)
	_ = store.Record(ctx, "dev", prepared(t, map[string]any{"k": "A"}), t0.Add(5*time.Hour))
	if err := store.Record(ctx, "dev", prepared(t, map[string]any{"k": "B"}), t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	current, _ := store.Current(ctx, "dev")
	if len(current) != 1 || current[0].Value != "A" {
		t.Errorf("current = %+v, want A, confirmed after B was observed", current)
	}
	if history, _ := store.History(ctx, "dev", "k"); len(history) != 2 || history[1].Value != "B" {
		t.Errorf("history = %+v, want A then B", history)
	}
}

// TestFileStore_AnUnfinishedLineIsIgnoredAndCutOff: a crash in the middle of
// an append leaves a last line with no newline, which readers leave out and
// the next writer cuts off rather than refusing the file forever.
func TestFileStore_AnUnfinishedLineIsIgnoredAndCutOff(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "facts")
	store, err := facts.NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.Record(ctx, "dev", prepared(t, map[string]any{"k": "A"}), time.Now()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "dev.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0) // #nosec G304 -- the test's own file
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"key":"k","value":"B","ha`)
	_ = f.Close()
	if current, err := store.Current(ctx, "dev"); err != nil || len(current) != 1 || current[0].Value != "A" {
		t.Fatalf("with an unfinished line: %+v, %v", current, err)
	}
	if err := store.Record(ctx, "dev", prepared(t, map[string]any{"k": "C"}), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path) // #nosec G304 -- the test's own file
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	for _, l := range lines {
		if !json.Valid([]byte(l)) {
			t.Errorf("a line that is not whole JSON survived: %q", l)
		}
	}
	if len(lines) != 2 {
		t.Errorf("%d lines, want A and C: %q", len(lines), raw)
	}
}

// TestFileStore_ConcurrentWritersTakeTurns: two stores over one directory,
// as two CLI runs are, writing one device at once leave whole files with every
// value.
func TestFileStore_ConcurrentWritersTakeTurns(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "facts")
	ctx := context.Background()
	base := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	done := make(chan error, 2)
	for w := range 2 {
		go func() {
			store, err := facts.NewFileStore(dir)
			if err != nil {
				done <- err
				return
			}
			for i := range 50 {
				key := fmt.Sprintf("w%d_%d", w, i)
				if err := store.Record(ctx, "dev", prepared(t, map[string]any{key: i}), base.Add(time.Duration(i)*time.Second)); err != nil {
					done <- err
					return
				}
			}
			done <- nil
		}()
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	store, _ := facts.NewFileStore(dir)
	current, err := store.Current(ctx, "dev")
	if err != nil || len(current) != 100 {
		t.Fatalf("after two writers: %d facts, %v; want 100", len(current), err)
	}
	for _, e := range current {
		if e.LastSeenAt.IsZero() {
			t.Errorf("%s lost its last-seen time", e.Key)
		}
	}
}

// TestFileStore_ManyStoresCreateOneDevicesFilesAtOnce: eight stores, as eight
// CLI runs, each start writing the same forty new devices at the same moment,
// so each device's lock file is created by several at once. On macOS one such
// create failed with "no such file or directory" (fast-macos, 2026-11-02); the
// store tries that again, and the log says how often it had to here.
func TestFileStore_ManyStoresCreateOneDevicesFilesAtOnce(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "facts")
	if _, err := facts.NewFileStore(dir); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	at := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	const stores, devices = 8, 40
	before := facts.LockOpenRetries()
	start := make(chan struct{})
	errs := make(chan error, stores)
	for w := range stores {
		go func() {
			store, err := facts.NewFileStore(dir)
			if err != nil {
				errs <- err
				return
			}
			<-start
			for d := range devices {
				if err := store.Record(ctx, fmt.Sprintf("dev%d", d), prepared(t, map[string]any{fmt.Sprintf("w%d", w): d}), at); err != nil {
					errs <- err
					return
				}
			}
			errs <- nil
		}()
	}
	close(start)
	for range stores {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("lock opens tried again on %s: %d", runtime.GOOS, facts.LockOpenRetries()-before)
	store, _ := facts.NewFileStore(dir)
	for d := range devices {
		current, err := store.Current(ctx, fmt.Sprintf("dev%d", d))
		if err != nil || len(current) != stores {
			t.Fatalf("dev%d: %d facts, %v; want %d", d, len(current), err, stores)
		}
	}
}

// TestOpenLock_ADirectoryThatIsGoneFailsAtOnce: the retry is only for a
// directory that is still there.
func TestOpenLock_ADirectoryThatIsGoneFailsAtOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not remove a directory a handle holds open")
	}
	dir := filepath.Join(t.TempDir(), "gone")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	before := facts.LockOpenRetries()
	if f, err := facts.OpenLock(r, "dev.lock"); err == nil || !errors.Is(err, fs.ErrNotExist) {
		if f != nil {
			_ = f.Close()
		}
		t.Fatalf("a lock in a removed directory: %v, want not exist", err)
	}
	if n := facts.LockOpenRetries() - before; n != 0 {
		t.Errorf("tried again %d times for a directory that is gone", n)
	}
}

// TestFileStore_ReadingCreatesNothingAndRefusesALink: a read of a project no
// run has stored facts in finds none without making the directory, and a
// directory that is a link is refused for writing and reading alike.
func TestFileStore_ReadingCreatesNothingAndRefusesALink(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "facts")
	if _, err := facts.OpenFileStore(dir); !errors.Is(err, facts.ErrNoStore) {
		t.Errorf("a missing store: %v, want ErrNoStore", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("a read made the directory")
	}
	if runtime.GOOS == "windows" {
		return
	}
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := facts.NewFileStore(dir); err == nil {
		t.Error("a linked directory was accepted for writing")
	}
	if _, err := facts.OpenFileStore(dir); err == nil {
		t.Error("a linked directory was accepted for reading")
	}
}
