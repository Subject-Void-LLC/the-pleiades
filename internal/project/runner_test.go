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

	// claimedActor is the actor the last claim carried, so a test can check
	// the runner passes through what it was given rather than inventing one.
	claimedActor string
}

func (s *recordingStore) BeginSync(_ context.Context, id int, actor string) (project.Claim, error) {
	if s.beginErr != nil {
		return project.Claim{}, s.beginErr
	}
	s.mu.Lock()
	s.claimedActor = actor
	s.mu.Unlock()
	return project.Claim{
		Project:   project.Project{ID: id, SCMType: project.SCMGit, SCMURL: "https://example.invalid/a.git"},
		RunID:     id * 10,
		StartedAt: time.Now(),
	}, nil
}

func (s *recordingStore) actor() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.claimedActor
}

func (s *recordingStore) RecordSync(_ context.Context, _ int, result project.Result) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recorded = append(s.recorded, result)
	return nil
}

// ByLaunchable resolves a launchable reference into a project, standing in for
// the store's own lookup: the fake treats the launchable id as the project id,
// which is enough for the launch path under test.
func (s *recordingStore) ByLaunchable(_ context.Context, launchableID int) (project.Project, error) {
	if s.beginErr != nil {
		return project.Project{}, s.beginErr
	}
	return project.Project{
		ID: launchableID, SCMType: project.SCMGit, SCMURL: "https://example.invalid/a.git",
	}, nil
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

	if _, err := r.Enqueue(context.Background(), 7, "tester"); err != nil {
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

	_, err := r.Enqueue(context.Background(), 7, "tester")
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

	if _, err := r.Enqueue(context.Background(), 7, "tester"); err != nil {
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

	if _, err := r.Enqueue(context.Background(), 7, "tester"); err != nil {
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

// TestRunner_CancelStopsOneCloneAndRecordsItAsCancelled proves a person can
// stop a fetch that is taking too long, and that the record says who ended
// it rather than reporting whatever the transport said when its context went
// away.
func TestRunner_CancelStopsOneCloneAndRecordsItAsCancelled(t *testing.T) {
	store := &recordingStore{}
	started := make(chan struct{})
	syncer := &controllableSyncer{
		result:  project.Result{Status: project.SyncSucceeded, Revision: "abc123"},
		started: started,
		// Never released: the clone ends only when it is cancelled.
		release: make(chan struct{}),
	}
	r := project.NewRunner(store, syncer, nil)

	if _, err := r.Enqueue(context.Background(), 7, "tester"); err != nil {
		t.Fatalf("Enqueue() = %v", err)
	}
	<-started

	if !r.Cancel(7) {
		t.Fatal("Cancel() reported nothing running while a clone was in flight")
	}
	r.Wait()

	got := store.results()
	if len(got) != 1 {
		t.Fatalf("recorded %d outcomes, want the cancelled one", len(got))
	}
	if got[0].Status != project.SyncFailed {
		t.Errorf("cancelled sync recorded as %q, want failed", got[0].Status)
	}
	if !strings.Contains(got[0].Err, "cancelled") {
		t.Errorf("recorded reason %q, want it to say the sync was cancelled", got[0].Err)
	}
	// A cancelled clone did not land on a commit, so claiming one would be
	// worse than saying nothing.
	if got[0].Revision != "" {
		t.Errorf("cancelled sync recorded revision %q, want none", got[0].Revision)
	}
}

// TestRunner_CancelWithNothingRunningSaysSo keeps the control honest: a
// button that reported success against a project doing nothing would tell a
// reader it had stopped something it had not.
func TestRunner_CancelWithNothingRunningSaysSo(t *testing.T) {
	r := project.NewRunner(&recordingStore{}, &controllableSyncer{}, nil)
	if r.Cancel(7) {
		t.Error("Cancel() reported it stopped a clone with none running")
	}
}

// TestRunner_CancelLeavesOtherClonesAlone is why each clone gets a context of
// its own: cancelling one project must not stop everything in flight.
func TestRunner_CancelLeavesOtherClonesAlone(t *testing.T) {
	store := &recordingStore{}
	startedSeven := make(chan struct{})
	syncer := &controllableSyncer{
		result:  project.Result{Status: project.SyncSucceeded},
		started: startedSeven,
		release: make(chan struct{}),
	}
	r := project.NewRunner(store, syncer, nil)

	if _, err := r.Enqueue(context.Background(), 7, "tester"); err != nil {
		t.Fatalf("Enqueue() = %v", err)
	}
	<-startedSeven

	// A project with no clone of its own: cancelling it must not disturb 7.
	if r.Cancel(8) {
		t.Error("Cancel(8) reported stopping a clone that belonged to another project")
	}

	if !r.Cancel(7) {
		t.Error("Cancel(7) did not stop its own clone")
	}
	r.Wait()
}

// TestEnqueue_CarriesTheActorAndNamesTheAttempt proves the runner passes the
// caller's actor through to the claim untouched and hands back the attempt's
// id, which is what lets a schedule record which sync it started.
func TestEnqueue_CarriesTheActorAndNamesTheAttempt(t *testing.T) {
	store := &recordingStore{}
	runner := project.NewRunner(store, &controllableSyncer{
		result: project.Result{Status: project.SyncSucceeded, Revision: "abc123"},
	}, nil)
	defer runner.Shutdown(context.Background())

	runID, err := runner.Enqueue(context.Background(), 7, "scheduler:9")
	if err != nil {
		t.Fatalf("Enqueue() = %v", err)
	}
	if runID != 70 {
		t.Errorf("Enqueue() returned attempt %d, want the claim's own run id 70", runID)
	}
	if got := store.actor(); got != "scheduler:9" {
		t.Errorf("claimed actor = %q, want the actor the caller named", got)
	}

	runner.Wait()
	results := store.results()
	if len(results) != 1 {
		t.Fatalf("recorded %d outcomes, want 1", len(results))
	}
	if results[0].RunID != 70 {
		t.Errorf("recorded outcome names attempt %d, want 70: the outcome would open a second row", results[0].RunID)
	}
}

// TestEnqueue_RefusesASyncWithNoActor proves an unattributable sync never
// reaches the store or a goroutine.
func TestEnqueue_RefusesASyncWithNoActor(t *testing.T) {
	store := &recordingStore{}
	runner := project.NewRunner(store, &controllableSyncer{}, nil)
	defer runner.Shutdown(context.Background())

	if _, err := runner.Enqueue(context.Background(), 7, "  "); !errors.Is(err, project.ErrNoActor) {
		t.Errorf("Enqueue with a blank actor = %v, want ErrNoActor", err)
	}
	if got := store.actor(); got != "" {
		t.Errorf("the store was asked to claim for actor %q, want no claim at all", got)
	}
}
