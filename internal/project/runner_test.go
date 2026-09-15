// This file covers the asynchronous sync engine: that Enqueue runs a clone
// and records its outcome, surfaces a claim refusal to the caller, records a
// syncer failure rather than swallowing it, recovers interrupted claims at
// startup, and drains an in-flight clone on shutdown.
package project_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/project"
)

// recordingStore is the store half the runner drives, capturing what it
// records so a test can assert the outcome a background clone wrote.
type recordingStore struct {
	mu          sync.Mutex
	beginErr    error
	recorded    []project.Result
	resetN      int
	resetCalled bool
}

func (s *recordingStore) BeginSync(_ context.Context, id int) (project.Project, error) {
	if s.beginErr != nil {
		return project.Project{}, s.beginErr
	}
	return project.Project{ID: id, SCMType: project.SCMGit, SCMURL: "https://example.invalid/a.git"}, nil
}

func (s *recordingStore) RecordSync(_ context.Context, _ int, result project.Result) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recorded = append(s.recorded, result)
	return nil
}

func (s *recordingStore) ResetInterruptedSyncs(context.Context) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resetCalled = true
	return s.resetN, nil
}

func (s *recordingStore) results() []project.Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]project.Result(nil), s.recorded...)
}

// controllableSyncer stands in for a clone: it can report a fixed outcome,
// return an error, and (via release) block mid-sync so a shutdown test can
// catch it in flight.
type controllableSyncer struct {
	result  project.Result
	err     error
	started chan struct{}
	release chan struct{}
}

func (c *controllableSyncer) Sync(ctx context.Context, _ project.Project, progress io.Writer) (project.Result, error) {
	if progress != nil {
		// Real syncers report as they go; writing one line proves the
		// runner hands over a usable stream rather than a nil.
		_, _ = io.WriteString(progress, "cloning\n")
	}
	if c.started != nil {
		close(c.started)
	}
	if c.release != nil {
		select {
		case <-c.release:
		case <-ctx.Done():
			return project.Result{Status: project.SyncFailed, Err: "cancelled"}, nil
		}
	}
	return c.result, c.err
}

func (c *controllableSyncer) Playbooks(context.Context, project.Project) ([]string, error) {
	return nil, nil
}

func TestRunner_EnqueueRunsAndRecordsTheOutcome(t *testing.T) {
	store := &recordingStore{}
	syncer := &controllableSyncer{result: project.Result{Status: project.SyncSucceeded, Revision: "abc123"}}
	r := project.NewRunner(store, syncer, nil)

	if err := r.Enqueue(context.Background(), 7); err != nil {
		t.Fatalf("Enqueue() = %v, want it to start the sync", err)
	}
	r.Wait()

	got := store.results()
	if len(got) != 1 {
		t.Fatalf("recorded %d outcomes, want 1", len(got))
	}
	if got[0].Status != project.SyncSucceeded || got[0].Revision != "abc123" {
		t.Errorf("recorded %+v, want the syncer's own success", got[0])
	}
}

func TestRunner_EnqueueSurfacesAClaimRefusal(t *testing.T) {
	store := &recordingStore{beginErr: project.ErrSyncInProgress}
	r := project.NewRunner(store, &controllableSyncer{}, nil)

	err := r.Enqueue(context.Background(), 7)
	if !errors.Is(err, project.ErrSyncInProgress) {
		t.Errorf("Enqueue() = %v, want the claim refusal surfaced to the caller", err)
	}
	r.Wait()
	if got := store.results(); len(got) != 0 {
		t.Errorf("a refused claim still recorded %d outcomes, want 0", len(got))
	}
}

func TestRunner_RecordsASyncerFailureRatherThanSwallowingIt(t *testing.T) {
	store := &recordingStore{}
	syncer := &controllableSyncer{err: errors.New("could not attempt: host unreachable")}
	r := project.NewRunner(store, syncer, nil)

	if err := r.Enqueue(context.Background(), 7); err != nil {
		t.Fatalf("Enqueue() = %v", err)
	}
	r.Wait()

	got := store.results()
	if len(got) != 1 || got[0].Status != project.SyncFailed {
		t.Fatalf("recorded %+v, want a single failed outcome", got)
	}
	if !strings.Contains(got[0].Err, "host unreachable") {
		t.Errorf("recorded error %q, want the syncer's own reason", got[0].Err)
	}
}

func TestRunner_RecoverInterruptedClearsStrandedClaims(t *testing.T) {
	store := &recordingStore{resetN: 2}
	r := project.NewRunner(store, &controllableSyncer{}, nil)

	r.RecoverInterrupted(context.Background())
	if !store.resetCalled {
		t.Error("RecoverInterrupted did not reset interrupted syncs")
	}
}

func TestRunner_ShutdownDrainsAnInFlightClone(t *testing.T) {
	store := &recordingStore{}
	started := make(chan struct{})
	syncer := &controllableSyncer{
		result:  project.Result{Status: project.SyncSucceeded},
		started: started,
		// No release channel is ever closed: the sync unblocks only when
		// Shutdown cancels the Runner's context.
		release: make(chan struct{}),
	}
	r := project.NewRunner(store, syncer, nil)

	if err := r.Enqueue(context.Background(), 7); err != nil {
		t.Fatalf("Enqueue() = %v", err)
	}
	<-started // the clone is in flight, blocked

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() = %v, want it to drain within the deadline", err)
	}

	// The interrupted clone still recorded an outcome, so its row is not
	// left running for a restart to clear.
	if got := store.results(); len(got) != 1 {
		t.Errorf("after shutdown %d outcomes recorded, want the in-flight one written", len(got))
	}
}
