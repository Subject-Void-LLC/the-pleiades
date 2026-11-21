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

func TestProjectFactReader_History(t *testing.T) {
	dir := t.TempDir()
	r := projectFactReader{dir: factsDir(dir), keys: factKeysToRead(dir)}
	if h, err := r.History(context.Background(), "web1", "os.family"); err != nil || len(h) != 0 {
		t.Fatalf("History with no store = %v, %v", h, err)
	}
	store, err := facts.NewFileStore(factsDir(dir), factKeysToWrite(dir))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"debian", "redhat"} {
		entries, _, _ := facts.Prepare(map[string]any{"os.family": v}, facts.Guard{})
		if err := store.Record(context.Background(), "web1", facts.Observation{Entries: entries, At: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	h, err := r.History(context.Background(), "web1", "os.family")
	if err != nil || len(h) != 2 || h[0].Value != "debian" || h[1].Value != "redhat" {
		t.Fatalf("History = %+v, %v; want debian then redhat", h, err)
	}
	if err := os.RemoveAll(factsDir(dir)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(factsDir(dir), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.History(context.Background(), "web1", "os.family"); err == nil {
		t.Fatal("History over a store that is a file was not an error")
	}
	if _, err := r.Current(context.Background(), "web1"); err == nil {
		t.Fatal("Current over a store that is a file was not an error")
	}
}
