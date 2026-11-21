// Phase 41's read port on the Crawl tier: the executor a run builds reads the
// project's stored facts, in a check as in a real run, and a read creates no
// store.
package main

import (
	"context"
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
// names keys and gets each one's current value; a key the device's observer
// (facts.gather) declares and never stored is absent, one nothing that
// observed it declares is unknown; a store that is a file is an error.
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
	for _, v := range []string{"debian", "redhat"} {
		entries, _, _ := facts.Prepare(map[string]any{"os.family": v}, facts.Guard{})
		obs := facts.Observation{Entries: entries, At: time.Now(), By: facts.Provenance{Method: "facts.gather"}}
		if err := store.Record(context.Background(), "web1", obs); err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.ReadFacts(context.Background(), "web1", []string{"os.family", "os.codename", "acme.widget"})
	if err != nil {
		t.Fatal(err)
	}
	if a := got["os.family"]; a.State != engine.FactPresent || a.Value != "redhat" {
		t.Errorf("os.family = %+v, want its current value, redhat", a)
	}
	if a := got["os.codename"]; a.State != engine.FactAbsent {
		t.Errorf("os.codename, which facts.gather declares and did not store = %+v, want absent", a)
	}
	if a := got["acme.widget"]; a.State != engine.FactUnknown {
		t.Errorf("acme.widget, which nothing that observed web1 declares = %+v, want unknown", a)
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
