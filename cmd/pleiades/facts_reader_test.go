// Phase 41's read port on the Crawl tier: the executor a run builds reads the
// project's stored facts, in a check as in a real run, and a read creates no
// store.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/facts"
	"github.com/Subject-Void-LLC/the-pleiades/internal/validate"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// runExecutorFor builds the executor `pleiades run` builds for dir.
func runExecutorFor(t *testing.T, dir string, mode collection.Mode) *engine.Executor {
	t.Helper()
	executor, closeAll, err := newRunExecutor(runOptions{dir: dir, forks: 1}, validate.WorldView{Mode: mode}, nil, mode)
	if err != nil {
		t.Fatalf("newRunExecutor: %v", err)
	}
	t.Cleanup(closeAll)
	return executor
}

func TestRunExecutor_ReadsWhatAnEarlierRunStored(t *testing.T) {
	dir := t.TempDir()
	store, err := facts.NewFileStore(factsDir(dir), factKeysToWrite(dir))
	if err != nil {
		t.Fatal(err)
	}
	entries, _, err := facts.Prepare(map[string]any{"os.family": "debian"}, facts.Guard{})
	if err != nil {
		t.Fatal(err)
	}
	seen := time.Now().Add(-time.Hour)
	if err := store.Record(context.Background(), "web1", facts.Observation{Entries: entries, At: seen}); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []collection.Mode{collection.ModeExecute, collection.ModeCheck} {
		got, err := runExecutorFor(t, dir, mode).ReadFact(context.Background(), "web1", "os.family", time.Now())
		if err != nil {
			t.Fatalf("%s: ReadFact: %v", mode, err)
		}
		if !got.Known || got.Value != "debian" || got.Age < 59*time.Minute {
			t.Fatalf("%s: ReadFact = %+v, want the stored debian, about an hour old", mode, got)
		}
	}
}

func TestRunExecutor_AProjectWithNoStoreReadsNoFactsAndMakesNone(t *testing.T) {
	dir := t.TempDir()
	got, err := runExecutorFor(t, dir, collection.ModeCheck).ReadFact(context.Background(), "web1", "os.family", time.Now())
	if err != nil || got.Known {
		t.Fatalf("ReadFact = %+v, %v; want no fact and no error", got, err)
	}
	if _, err := os.Lstat(factsDir(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a read made the store: %v", err)
	}
}

func TestRunExecutor_AStoreThatCannotBeReadIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(factsDir(dir)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(factsDir(dir), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runExecutorFor(t, dir, collection.ModeCheck).ReadFact(context.Background(), "web1", "os.family", time.Now()); err == nil {
		t.Fatal("a store that is a file read as a device with no facts")
	}
}

// TestProjectFactReader_AnswersOnlyTheNamedKeysAsTheEngineReadsThem: a read
// names keys and gets each one's current value. The store keeps what each
// observation emitted, never what it looked for, so no key it lacks reads as
// absent: not one facts.gather declares exactly (a filter may have left it
// out), not a package after a gather or after one pkg.install, not a file
// after a gather of another path, and not one nothing declares.
func TestProjectFactReader_AnswersOnlyTheNamedKeysAsTheEngineReadsThem(t *testing.T) {
	dir := t.TempDir()
	r := projectFactReader{dir: factsDir(dir), keys: factKeysToRead(dir)}
	if got, err := r.ReadFacts(context.Background(), "web1", []string{"os.family"}); err != nil || len(got) != 0 {
		t.Fatalf("a read with no store = %v, %v; want nothing known", got, err)
	}
	store, err := facts.NewFileStore(factsDir(dir), factKeysToWrite(dir))
	if err != nil {
		t.Fatal(err)
	}
	record := func(method string, observed map[string]any) {
		t.Helper()
		entries, _, _ := facts.Prepare(observed, facts.Guard{})
		if err := store.Record(context.Background(), "web1", facts.Observation{Entries: entries, At: time.Now(), By: facts.Provenance{Method: method}}); err != nil {
			t.Fatal(err)
		}
	}
	record("facts.gather", map[string]any{"os.family": "debian"})
	record("facts.gather", map[string]any{"os.family": "redhat", "file./etc/hosts": map[string]any{"exists": true}})
	record("pkg.install", map[string]any{"package.curl": map[string]any{"version": "8.5"}})
	keys := []string{"os.family", "os.codename", "package.nginx", "file./etc/passwd", "acme.widget"}
	got, err := r.ReadFacts(context.Background(), "web1", keys)
	if err != nil {
		t.Fatal(err)
	}
	if a := got["os.family"]; a.State != engine.FactPresent || a.Value != "redhat" {
		t.Errorf("os.family = %+v, want its current value, redhat", a)
	}
	for _, key := range keys[1:] {
		if a := got[key]; a.State != engine.FactUnknown {
			t.Errorf("%s, which no stored record says was looked for = %+v, want unknown", key, a)
		}
	}
	if err := os.RemoveAll(factsDir(dir)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(factsDir(dir), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReadFacts(context.Background(), "web1", []string{"os.family"}); err == nil {
		t.Fatal("a read of a store that is a file was not an error")
	}
}

// TestProjectFactReader_ALostKeyIsOneAnswerForEveryDevice: a project that
// stored facts and lost its master key cannot read any device's, one with no
// files of its own included, so a run never answers unavailable for one
// device and unknown for the next; a project that never stored a fact and
// has no key reads every device as unknown.
func TestProjectFactReader_ALostKeyIsOneAnswerForEveryDevice(t *testing.T) {
	t.Setenv("PLEIADES_MASTER_KEY", "")
	if err := os.Unsetenv("PLEIADES_MASTER_KEY"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	r := projectFactReader{dir: factsDir(dir), keys: factKeysToRead(dir)}
	store, err := facts.NewFileStore(factsDir(dir), factKeysToWrite(dir))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := r.ReadFacts(context.Background(), "web2", []string{"os.family"}); err != nil || len(got) != 0 {
		t.Fatalf("an empty store with no key = %v, %v; want every key unknown", got, err)
	}
	entries, _, _ := facts.Prepare(map[string]any{"os.family": "debian"}, facts.Guard{})
	if err := store.Record(context.Background(), "web1", facts.Observation{Entries: entries, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(vaultDir(dir), ".pleiades", "master.key")); err != nil {
		t.Fatal(err)
	}
	lost := projectFactReader{dir: factsDir(dir), keys: factKeysToRead(dir)}
	for _, device := range []string{"web1", "web2"} {
		if _, err := lost.ReadFacts(context.Background(), device, []string{"os.family"}); !errors.Is(err, facts.ErrNoKeys) {
			t.Errorf("%s with the key lost: %v, want the store unreadable (ErrNoKeys)", device, err)
		}
	}
}

// withoutMasterKeyEnv clears PLEIADES_MASTER_KEY for one test, so the project's
// own key file, or its absence, is what a read meets.
func withoutMasterKeyEnv(t *testing.T) {
	t.Helper()
	t.Setenv("PLEIADES_MASTER_KEY", "")
	if err := os.Unsetenv("PLEIADES_MASTER_KEY"); err != nil {
		t.Fatal(err)
	}
}

// storeFacts stores observed for device in dir's fact store, as a real run
// does, making the project's master key when it has none.
func storeFacts(t *testing.T, dir, device string, observed map[string]any) {
	t.Helper()
	store, err := facts.NewFileStore(factsDir(dir), factKeysToWrite(dir))
	if err != nil {
		t.Fatal(err)
	}
	entries, problems, err := facts.Prepare(observed, facts.Guard{})
	if err != nil || len(problems) != 0 {
		t.Fatalf("Prepare: %v, %v", problems, err)
	}
	if err := store.Record(context.Background(), device, facts.Observation{Entries: entries, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

// masterKeyFile is where dir's master key is kept.
func masterKeyFile(dir string) string {
	return filepath.Join(vaultDir(dir), ".pleiades", "master.key")
}

// TestRunExecutor_NoMasterKeyAndNoStoreReadsNoFactsInEitherMode: a project
// with neither reads as devices with no facts, in a real run as in a check,
// and the read makes no key. (A real run's executor makes the empty store
// directory its recorder writes into; a check makes none.)
func TestRunExecutor_NoMasterKeyAndNoStoreReadsNoFactsInEitherMode(t *testing.T) {
	withoutMasterKeyEnv(t)
	for _, mode := range []collection.Mode{collection.ModeExecute, collection.ModeCheck} {
		dir := t.TempDir()
		got, err := runExecutorFor(t, dir, mode).ReadFact(context.Background(), "web1", "os.family", time.Now())
		if err != nil || got.Known {
			t.Fatalf("%s: ReadFact = %+v, %v; want no fact and no error", mode, got, err)
		}
		if _, err := os.Lstat(masterKeyFile(dir)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s: a read made the master key: %v", mode, err)
		}
	}
}

// TestRunExecutor_AStoreThatWillNotOpenIsUnavailableNotAbsent: a store whose
// key is gone, whose lines another key sealed, or whose lines were never
// sealed is a failed read naming why, never a device with no facts.
func TestRunExecutor_AStoreThatWillNotOpenIsUnavailableNotAbsent(t *testing.T) {
	withoutMasterKeyEnv(t)
	otherKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	cases := []struct {
		name  string
		spoil func(t *testing.T, dir string)
		want  error
	}{
		{"the master key is gone", func(t *testing.T, dir string) {
			if err := os.Remove(masterKeyFile(dir)); err != nil {
				t.Fatal(err)
			}
		}, facts.ErrNoKeys},
		{"another key sealed it", func(t *testing.T, dir string) {
			if err := os.WriteFile(masterKeyFile(dir), []byte(otherKey), 0o600); err != nil {
				t.Fatal(err)
			}
		}, facts.ErrSealed},
		{"a release before sealing wrote it", func(t *testing.T, dir string) {
			plain := `{"key":"os.family","value":"debian","hash":"x","seq":1}` + "\n"
			if err := os.WriteFile(filepath.Join(factsDir(dir), "web1.jsonl"), []byte(plain), 0o600); err != nil {
				t.Fatal(err)
			}
		}, facts.ErrSealed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, mode := range []collection.Mode{collection.ModeExecute, collection.ModeCheck} {
				dir := t.TempDir()
				storeFacts(t, dir, "web1", map[string]any{"os.family": "debian"})
				tc.spoil(t, dir)
				got, err := runExecutorFor(t, dir, mode).ReadFact(context.Background(), "web1", "os.family", time.Now())
				if !errors.Is(err, tc.want) || got.Known {
					t.Fatalf("%s: ReadFact = %+v, %v; want a failed read wrapping %v", mode, got, err, tc.want)
				}
			}
		})
	}
}

// TestRunExecutor_ADeviceWithNoFactsIsUnknownNotAnError: beside a device with
// facts, one with none reads as unknown with no error, and never as another
// device's value.
func TestRunExecutor_ADeviceWithNoFactsIsUnknownNotAnError(t *testing.T) {
	withoutMasterKeyEnv(t)
	dir := t.TempDir()
	storeFacts(t, dir, "web1", map[string]any{"os.family": "debian"})
	x := runExecutorFor(t, dir, collection.ModeCheck)
	got, err := x.ReadFact(context.Background(), "web2", "os.family", time.Now())
	if err != nil || got.Known {
		t.Fatalf("ReadFact of a device with no facts = %+v, %v; want unknown and no error", got, err)
	}
	if got, err := x.ReadFact(context.Background(), "web1", "os.family", time.Now()); err != nil || got.Value != "debian" {
		t.Fatalf("ReadFact of the device with facts = %+v, %v; want debian", got, err)
	}
}

// TestRunExecutor_AStoredValueComesBackAsPlainData: a value holding terminal
// control bytes and template syntax is read back byte for byte, neither
// rendered, escaped nor masked, since what a reader does with it is its own
// business.
func TestRunExecutor_AStoredValueComesBackAsPlainData(t *testing.T) {
	withoutMasterKeyEnv(t)
	dir := t.TempDir()
	value := "\x1b]0;title\x07\x1b[31mred\x1b[0m\r\n{{ vars.secret }} {% if true %}x{% endif %} {# c #}\x08\x7f"
	storeFacts(t, dir, "web1", map[string]any{"motd.text": value})
	got, err := runExecutorFor(t, dir, collection.ModeExecute).ReadFact(context.Background(), "web1", "motd.text", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := got.Value.(string); !ok || s != value {
		t.Fatalf("ReadFact value = %q (%T), want %q byte for byte", got.Value, got.Value, value)
	}
}
