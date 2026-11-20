// Tests of stored facts at rest (atrest.go): the keys, a sealed line, and the
// Crawl CLI's real file store searched byte for byte for what it must not
// hold.
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

// TestKeys_HashAndTokenAreKeyedAndBoundToTheDevice: the keyed hash is not the
// plain one, both it and the token differ between devices and between keys,
// and each is the same on every call, which is what lets a store see a change.
func TestKeys_HashAndTokenAreKeyedAndBoundToTheDevice(t *testing.T) {
	keys := factstest.Keys()
	other, err := facts.NewKeys(bytes.Repeat([]byte{7}, 32), "test")
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := facts.Hash("package.openssh", "9.6")
	hash, err := keys.Hash("dev1", "package.openssh", "9.6")
	if err != nil || len(hash) != 64 || hash == plain {
		t.Fatalf("Hash = %q, %v; want 64 hex characters that are not the plain hash", hash, err)
	}
	token, err := keys.Token("dev1", "package.openssh")
	if err != nil || len(token) != 64 || token == hash {
		t.Fatalf("Token = %q, %v", token, err)
	}
	if again, _ := keys.Hash("dev1", "package.openssh", "9.6"); again != hash {
		t.Error("the keyed hash of one value differs between calls")
	}
	if again, _ := factstest.Keys().Token("dev1", "package.openssh"); again != token {
		t.Error("the token of one key differs between Keys of one secret")
	}
	if changed, _ := keys.Hash("dev1", "package.openssh", "9.7"); changed == hash {
		t.Error("a changed value keeps its hash")
	}
	if h, _ := keys.Hash("dev2", "package.openssh", "9.6"); h == hash {
		t.Error("one value on two devices has one hash")
	}
	if tok, _ := keys.Token("dev2", "package.openssh"); tok == token {
		t.Error("one key on two devices has one token")
	}
	if h, _ := other.Hash("dev1", "package.openssh", "9.6"); h == hash {
		t.Error("two keys give one hash")
	}
	if tok, _ := other.Token("dev1", "package.openssh"); tok == token {
		t.Error("two keys give one token")
	}
	if keys.ID() == "" || keys.ID() == other.ID() || !strings.HasPrefix(keys.ID(), "test:") || keys.ID() != factstest.Keys().ID() {
		t.Errorf("IDs %q and %q: want each its own, led by its source, and stable", keys.ID(), other.ID())
	}
	// A NUL would let a device's end be read as a key's start.
	if _, err := keys.Hash("dev\x001", "k", "v"); err == nil {
		t.Error("a device holding a NUL was hashed")
	}
	if _, err := keys.Token("dev1", "k\x00x"); err == nil {
		t.Error("a key holding a NUL was tokenized")
	}
	if _, err := keys.SealLine("dev\x001", facts.RoleValues, 0, nil); err == nil {
		t.Error("a line was sealed for a device holding a NUL")
	}
	// A value that does not encode has no hash.
	if _, err := keys.Hash("dev1", "k", make(chan int)); err == nil {
		t.Error("a value that does not encode was hashed")
	}
	if _, err := keys.Rekey("dev1", []facts.Entry{{Key: "k", Value: make(chan int)}}); err == nil {
		t.Error("Rekey passed a value that does not encode")
	}
}

// TestKeys_NoKeysFailClosed: no Keys, a secret that is too short and one that
// cannot be read each answer every call with ErrNoKeys, and have no identity.
func TestKeys_NoKeysFailClosed(t *testing.T) {
	short, err := facts.NewKeys([]byte("too short"), "test")
	if short != nil || !errors.Is(err, facts.ErrNoKeys) {
		t.Fatalf("NewKeys with a short secret = %v, %v", short, err)
	}
	unreadable := facts.NewLazyKeys(func() ([]byte, error) { return nil, errors.New("no master key") }, "project")
	for name, keys := range map[string]*facts.Keys{"nil": nil, "unreadable": unreadable} {
		if _, err := keys.Hash("d", "k", "v"); !errors.Is(err, facts.ErrNoKeys) {
			t.Errorf("%s Hash: %v", name, err)
		}
		if _, err := keys.Token("d", "k"); !errors.Is(err, facts.ErrNoKeys) {
			t.Errorf("%s Token: %v", name, err)
		}
		if _, err := keys.Rekey("d", []facts.Entry{{Key: "k", Value: "v"}}); !errors.Is(err, facts.ErrNoKeys) {
			t.Errorf("%s Rekey: %v", name, err)
		}
		if _, err := keys.SealLine("d", facts.RoleValues, 0, []byte("x")); !errors.Is(err, facts.ErrNoKeys) {
			t.Errorf("%s SealLine: %v", name, err)
		}
		if _, err := keys.OpenLine("d", facts.RoleValues, 0, []byte("v1.x")); !errors.Is(err, facts.ErrNoKeys) {
			t.Errorf("%s OpenLine: %v", name, err)
		}
		if keys.ID() != "" {
			t.Errorf("%s has an identity: %q", name, keys.ID())
		}
	}
	if !strings.Contains(func() string { _, err := unreadable.Token("d", "k"); return err.Error() }(), "no master key") {
		t.Error("the reason the secret could not be read is not named")
	}
}

// TestKeys_ASealedLineOpensOnlyWhereItWasSealed: a line opens as its device's
// line of its role at its offset under its key, and as nothing else.
func TestKeys_ASealedLineOpensOnlyWhereItWasSealed(t *testing.T) {
	keys := factstest.Keys()
	plain := []byte(`{"key":"package.openssh","value":"9.6"}`)
	line, err := keys.SealLine("dev1", facts.RoleValues, 128, plain)
	if err != nil || bytes.Contains(line, []byte("openssh")) || bytes.ContainsAny(line, "\n") {
		t.Fatalf("SealLine = %q, %v", line, err)
	}
	if len(line)+1 != facts.SealedLineBytes(len(plain)) {
		t.Errorf("a %d byte line sealed to %d bytes, SealedLineBytes says %d with its newline", len(plain), len(line), facts.SealedLineBytes(len(plain)))
	}
	if again, _ := keys.SealLine("dev1", facts.RoleValues, 128, plain); bytes.Equal(again, line) {
		t.Error("one line sealed twice is the same bytes: the nonce repeats")
	}
	if got, err := keys.OpenLine("dev1", facts.RoleValues, 128, line); err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("OpenLine = %q, %v", got, err)
	}
	other, _ := facts.NewKeys(bytes.Repeat([]byte{7}, 32), "test")
	flipped := append([]byte{}, line...)
	flipped[len(flipped)-1] ^= 1
	for name, open := range map[string]func() ([]byte, error){
		"another device": func() ([]byte, error) { return keys.OpenLine("dev2", facts.RoleValues, 128, line) },
		"another file":   func() ([]byte, error) { return keys.OpenLine("dev1", facts.RoleSeen, 128, line) },
		"another place":  func() ([]byte, error) { return keys.OpenLine("dev1", facts.RoleValues, 0, line) },
		"another key":    func() ([]byte, error) { return other.OpenLine("dev1", facts.RoleValues, 128, line) },
		"an edited line": func() ([]byte, error) { return keys.OpenLine("dev1", facts.RoleValues, 128, flipped) },
		"plaintext":      func() ([]byte, error) { return keys.OpenLine("dev1", facts.RoleValues, 128, plain) },
		"not base64":     func() ([]byte, error) { return keys.OpenLine("dev1", facts.RoleValues, 128, []byte("v1.!!")) },
		"too short":      func() ([]byte, error) { return keys.OpenLine("dev1", facts.RoleValues, 128, []byte("v1.AAAA")) },
		"nothing":        func() ([]byte, error) { return keys.OpenLine("dev1", facts.RoleValues, 128, nil) },
	} {
		if got, err := open(); !errors.Is(err, facts.ErrSealed) {
			t.Errorf("%s opened: %q, %v", name, got, err)
		}
	}
}

// sealedProject records a host's facts through the real file store: a
// package's version, a user, and a volatile value, then a second gather that
// changes only the package. It returns the store and its directory.
func sealedProject(t *testing.T) (*facts.FileStore, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "facts")
	store, err := facts.NewFileStore(dir, factstest.Keys())
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	for i, version := range []string{"1:9.6p1-3ubuntu13.5", "1:9.6p1-3ubuntu13.5", "1:9.9p2-zq7build"} {
		stable := prepared(t, map[string]any{"package.openssh-server": version, "user.zq9operator": map[string]any{"shell": "/bin/zq9sh"}})
		up := prepared(t, map[string]any{"host.zq9uptime": 4242 + i})
		up[0].Kind = facts.KindVolatile
		obs := facts.Observation{Entries: append(stable, up...), At: at.Add(time.Duration(i) * time.Hour),
			By: facts.Provenance{Run: "r", Order: facts.Order{Source: facts.ClockSource, Token: uint64(i + 1)}}}
		if err := store.Record(context.Background(), "dev1", obs); err != nil {
			t.Fatal(err)
		}
	}
	return store, dir
}

// TestFileStore_NothingStoredIsPlaintext: after real gathers, no file under
// the fact directory holds a fact's key, a value, a field of one or a plain
// hash of one; the store still reads all of it back, sees the one change and
// only that one, and its chain verifies.
func TestFileStore_NothingStoredIsPlaintext(t *testing.T) {
	store, dir := sealedProject(t)
	ctx := context.Background()
	plainHash, _ := facts.Hash("package.openssh-server", "1:9.6p1-3ubuntu13.5")
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	searched := 0
	for _, f := range files {
		raw, err := os.ReadFile(filepath.Join(dir, f.Name())) // #nosec G304 -- the test's own directory
		if err != nil {
			t.Fatal(err)
		}
		if len(raw) > 0 {
			searched++
		}
		for _, plain := range []string{"package", "openssh", "ubuntu13", "9.6p1", "zq7build", "zq9operator", "zq9sh", "zq9uptime", "4242", "shell", plainHash, `"key"`, `"value"`} {
			if bytes.Contains(raw, []byte(plain)) {
				t.Errorf("%s holds %q in plaintext", f.Name(), plain)
			}
		}
	}
	// The control: the three files were there to search.
	if searched < 3 {
		t.Fatalf("searched %d files with content, want the history, the observations and the state", searched)
	}
	current, err := store.Current(ctx, "dev1")
	if err != nil || len(current) != 3 {
		t.Fatalf("Current = %+v, %v", current, err)
	}
	for _, e := range current {
		if e.Hash == plainHash || len(e.Hash) != 64 {
			t.Errorf("%s reads back with hash %q, want a keyed one", e.Key, e.Hash)
		}
	}
	// Drift still shows: the package changed once, the user never.
	if h, err := store.History(ctx, "dev1", "package.openssh-server"); err != nil || len(h) != 2 || h[0].Value != "1:9.6p1-3ubuntu13.5" || h[1].Value != "1:9.9p2-zq7build" {
		t.Fatalf("History(package) = %+v, %v; want the two versions", h, err)
	}
	if h, err := store.History(ctx, "dev1", "user.zq9operator"); err != nil || len(h) != 1 {
		t.Fatalf("History(user) = %+v, %v; want one value, confirmed twice", h, err)
	}
	if v, err := store.Verify(ctx, "dev1"); err != nil || v.Break != nil || v.Values != 3 {
		t.Fatalf("Verify = %+v, %v; want three chained values", v, err)
	}
}

// TestFileStore_WithoutItsKeyStoresAndReadsNothing: a store with no key, or
// with a key that cannot be read, refuses to record and writes no file; one
// reading a sealed project refuses every read, and one holding another key
// opens nothing.
func TestFileStore_WithoutItsKeyStoresAndReadsNothing(t *testing.T) {
	ctx := context.Background()
	obs := facts.Observation{Entries: prepared(t, map[string]any{"package.openssh-server": "9.6"}), At: time.Now()}
	unreadable := facts.NewLazyKeys(func() ([]byte, error) { return nil, errors.New("no master key") }, "project")
	for name, keys := range map[string]*facts.Keys{"no key": nil, "an unreadable key": unreadable} {
		dir := filepath.Join(t.TempDir(), "facts")
		store, err := facts.NewFileStore(dir, keys)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Record(ctx, "dev1", obs); !errors.Is(err, facts.ErrNoKeys) {
			t.Errorf("Record with %s: %v, want ErrNoKeys", name, err)
		}
		if left, _ := os.ReadDir(dir); len(left) != 0 {
			t.Errorf("Record with %s wrote %d file(s)", name, len(left))
		}
	}
	_, dir := sealedProject(t)
	keyless, err := facts.OpenFileStore(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := keyless.Current(ctx, "dev1"); !errors.Is(err, facts.ErrNoKeys) {
		t.Errorf("Current with no key: %v", err)
	}
	if _, err := keyless.History(ctx, "dev1", "package.openssh-server"); !errors.Is(err, facts.ErrNoKeys) {
		t.Errorf("History with no key: %v", err)
	}
	if _, err := keyless.Verify(ctx, "dev1"); !errors.Is(err, facts.ErrNoKeys) {
		t.Errorf("Verify with no key: %v", err)
	}
	wrong, _ := facts.NewKeys(bytes.Repeat([]byte{7}, 32), "test")
	other, err := facts.OpenFileStore(dir, wrong)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Current(ctx, "dev1"); !errors.Is(err, facts.ErrSealed) {
		t.Errorf("Current under another key: %v, want ErrSealed", err)
	}
	if err := other.Record(ctx, "dev1", obs); !errors.Is(err, facts.ErrSealed) {
		t.Errorf("Record under another key: %v, want ErrSealed, never a second history", err)
	}
}

// TestFileStore_AMovedLineDoesNotOpen: a device's files copied to another
// device, a history line put in another place, and a state put where the
// history goes each fail to open, since every line is bound to its device,
// its file and its place.
func TestFileStore_AMovedLineDoesNotOpen(t *testing.T) {
	ctx := context.Background()
	store, dir := sealedProject(t)
	read := func(name string) []byte {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(dir, name)) // #nosec G304 -- the test's own directory
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	write := func(name string, raw []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// The whole history, moved to another device.
	write("dev2.jsonl", read("dev1.jsonl"))
	if _, err := store.Current(ctx, "dev2"); !errors.Is(err, facts.ErrSealed) {
		t.Errorf("another device's history read as dev2's: %v", err)
	}
	if _, err := store.Verify(ctx, "dev2"); !errors.Is(err, facts.ErrSealed) {
		t.Errorf("another device's history verified as dev2's: %v", err)
	}
	// Its observations moved too: each is left out, as a damaged one is, and
	// none confirms anything.
	_ = os.Remove(filepath.Join(dir, "dev2.jsonl"))
	write("dev2.seen.jsonl", read("dev1.seen.jsonl"))
	if current, err := store.Current(ctx, "dev2"); err != nil || len(current) != 0 {
		t.Errorf("another device's observations alone: %+v, %v; want no facts", current, err)
	}
	// Its state moved.
	write("dev3.state.json", read("dev1.state.json"))
	if _, err := store.Current(ctx, "dev3"); !errors.Is(err, facts.ErrSealed) {
		t.Errorf("another device's state read as dev3's: %v", err)
	}
	// Two lines of one history, swapped.
	lines := bytes.SplitAfter(read("dev1.jsonl"), []byte("\n"))
	if len(lines) < 3 {
		t.Fatalf("%d history lines, want at least two and the empty tail", len(lines))
	}
	lines[0], lines[1] = lines[1], lines[0]
	write("dev1.jsonl", bytes.Join(lines, nil))
	if _, err := store.Current(ctx, "dev1"); !errors.Is(err, facts.ErrSealed) || !strings.Contains(err.Error(), "line 1") {
		t.Errorf("two swapped lines read: %v, want ErrSealed at line 1", err)
	}
}
