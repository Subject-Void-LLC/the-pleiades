// Package launchable_test's router coverage: the one launch path, and what
// it refuses to be built with.
//
// The strictness at construction is most of what is worth testing here. A
// Router that builds with a gap turns a wiring mistake into an unattended
// failure hours later, when a schedule fires onto a type nothing can launch.
package launchable_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launchable"
)

// recordingLauncher stands in for a real launcher: it records what it was
// asked to launch and reports a run id, or fails.
type recordingLauncher struct {
	runID string
	err   error

	// preflightErr, when set, is what Preflight objects with. A separate
	// field from err because refusing in advance and failing to launch are
	// different answers to different questions.
	preflightErr error

	// seen is every request this launcher was handed, so a test can prove
	// the routing sent each target to the right one.
	seen []launchable.Request
}

// Launch records the request and reports the configured outcome.
func (l *recordingLauncher) Launch(_ context.Context, req launchable.Request) (launchable.Launched, error) {
	l.seen = append(l.seen, req)
	if l.err != nil {
		return launchable.Launched{}, l.err
	}
	// Deliberately reports the WRONG unified job type, so the test below can
	// prove the Router stamps the descriptor's own rather than trusting this.
	return launchable.Launched{RunID: l.runID, UnifiedJobType: "whatever-this-launcher-says"}, nil
}

// Preflight objects when configured to.
func (l *recordingLauncher) Preflight(_ context.Context, _ launchable.Request) error {
	return l.preflightErr
}

// unpreflightableLauncher implements Launcher and nothing else, which is the
// honest shape for a type with nothing to check in advance.
type unpreflightableLauncher struct{}

func (unpreflightableLauncher) Launch(_ context.Context, _ launchable.Request) (launchable.Launched, error) {
	return launchable.Launched{RunID: "ran"}, nil
}

// TestNewRouter_RefusesAGapInEitherDirection is the construction rule. Both
// halves are wiring mistakes that would otherwise surface far away: a
// registered type with no launcher fails when something launches it, and a
// launcher for an unregistered type is composed code that every picker and
// validator cannot see.
func TestNewRouter_RefusesAGapInEitherDirection(t *testing.T) {
	testTypes(t)

	if _, err := launchable.NewRouter(map[string]launchable.Launcher{
		"test_template": &recordingLauncher{runID: "job-1"},
	}); !errors.Is(err, launchable.ErrNoLauncher) {
		t.Errorf("NewRouter with a type left unlaunched = %v, want ErrNoLauncher", err)
	}

	if _, err := launchable.NewRouter(map[string]launchable.Launcher{
		"test_template": &recordingLauncher{runID: "job-1"},
		"test_sync":     &recordingLauncher{runID: "17"},
		"workflow":      &recordingLauncher{runID: "wf-1"},
	}); !errors.Is(err, launchable.ErrUnknownType) {
		t.Errorf("NewRouter with a launcher for an unregistered type = %v, want ErrUnknownType", err)
	}

	// A nil launcher is a gap too: composed in name only.
	if _, err := launchable.NewRouter(map[string]launchable.Launcher{
		"test_template": &recordingLauncher{runID: "job-1"},
		"test_sync":     nil,
	}); !errors.Is(err, launchable.ErrNoLauncher) {
		t.Errorf("NewRouter with a nil launcher = %v, want ErrNoLauncher", err)
	}

	// The control: the complete pairing builds and reports what it can run.
	router, err := launchable.NewRouter(map[string]launchable.Launcher{
		"test_template": &recordingLauncher{runID: "job-1"},
		"test_sync":     &recordingLauncher{runID: "17"},
	})
	if err != nil {
		t.Fatalf("NewRouter with both types = %v", err)
	}
	if got := router.Types(); len(got) != 2 || got[0] != "test_sync" || got[1] != "test_template" {
		t.Errorf("Types() = %v, want both, sorted", got)
	}
}

// TestRouter_LaunchSendsEachTargetToItsOwnLauncher is the routing itself,
// and the reason this package exists: two different sorts of launchable go
// through one call, and neither the caller nor this router branches on which.
func TestRouter_LaunchSendsEachTargetToItsOwnLauncher(t *testing.T) {
	testTypes(t)

	templates := &recordingLauncher{runID: "job-1"}
	syncs := &recordingLauncher{runID: "17"}
	router, err := launchable.NewRouter(map[string]launchable.Launcher{
		"test_template": templates,
		"test_sync":     syncs,
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	ctx := context.Background()

	launched, err := router.Launch(ctx, launchable.Request{
		Target: target("test_template"), Actor: "somebody", SavedConfigID: 3,
	})
	if err != nil {
		t.Fatalf("launching a template = %v", err)
	}
	if launched.RunID != "job-1" {
		t.Errorf("run id = %q, want the template launcher's", launched.RunID)
	}
	// Stamped from the descriptor, not from what the launcher claimed.
	if launched.UnifiedJobType != launchable.UnifiedJobJob {
		t.Errorf("unified job type = %q, want the type's own %q", launched.UnifiedJobType, launchable.UnifiedJobJob)
	}
	if len(templates.seen) != 1 || len(syncs.seen) != 0 {
		t.Fatalf("the template went to the wrong launcher: template saw %d, sync saw %d", len(templates.seen), len(syncs.seen))
	}
	if templates.seen[0].Actor != "somebody" || templates.seen[0].SavedConfigID != 3 {
		t.Errorf("the launcher received %+v, want the actor and configuration unchanged", templates.seen[0])
	}

	launched, err = router.Launch(ctx, launchable.Request{
		Target: target("test_sync"), Actor: "scheduler:4",
	})
	if err != nil {
		t.Fatalf("launching a sync = %v", err)
	}
	if launched.RunID != "17" || launched.UnifiedJobType != launchable.UnifiedJobProjectUpdate {
		t.Errorf("sync launch = %+v, want the sync launcher's run, typed as a project update", launched)
	}
	if len(syncs.seen) != 1 {
		t.Errorf("the sync launcher saw %d requests, want 1", len(syncs.seen))
	}
}

// TestRouter_LaunchRefusesWhatNoTypeAllows covers the checks that belong to
// every type alike, re-made here because this is the last point before
// something actually runs.
func TestRouter_LaunchRefusesWhatNoTypeAllows(t *testing.T) {
	testTypes(t)

	syncs := &recordingLauncher{runID: "17"}
	router, err := launchable.NewRouter(map[string]launchable.Launcher{
		"test_template": &recordingLauncher{runID: "job-1"},
		"test_sync":     syncs,
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	ctx := context.Background()

	if _, err := router.Launch(ctx, launchable.Request{Target: target("test_sync"), Actor: " "}); err == nil {
		t.Error("a launch naming no actor was accepted, so a run would be recorded against nobody")
	}
	if _, err := router.Launch(ctx, launchable.Request{
		Target: target("test_sync"), Actor: "somebody", SavedConfigID: 3,
	}); !errors.Is(err, launchable.ErrSavedConfigRefused) {
		t.Errorf("a sync carrying a saved configuration = %v, want ErrSavedConfigRefused", err)
	}
	if _, err := router.Launch(ctx, launchable.Request{
		Target: target("workflow"), Actor: "somebody",
	}); !errors.Is(err, launchable.ErrUnknownType) {
		t.Errorf("launching an unregistered type = %v, want ErrUnknownType", err)
	}
	if len(syncs.seen) != 0 {
		t.Errorf("the launcher was reached %d times despite every request being refused", len(syncs.seen))
	}
}

// TestRouter_Preflight proves a refusal can be had without launching, and
// that a launcher with nothing to check in advance is not an error.
func TestRouter_Preflight(t *testing.T) {
	testTypes(t)

	refusing := &recordingLauncher{preflightErr: launchable.Refusal{
		Field: "unified_job_template", Message: "this project has no fetchable source",
	}}
	router, err := launchable.NewRouter(map[string]launchable.Launcher{
		"test_sync":     refusing,
		"test_template": unpreflightableLauncher{},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	ctx := context.Background()

	var refusal launchable.Refusal
	err = router.Preflight(ctx, launchable.Request{Target: target("test_sync"), Actor: "somebody"})
	if !errors.As(err, &refusal) {
		t.Fatalf("Preflight() = %v, want a Refusal naming a field", err)
	}
	if refusal.Field != "unified_job_template" {
		t.Errorf("refusal field = %q, want the control that caused it", refusal.Field)
	}
	if len(refusing.seen) != 0 {
		t.Error("Preflight launched the target it was asked about")
	}

	if err := router.Preflight(ctx, launchable.Request{Target: target("test_template"), Actor: "somebody"}); err != nil {
		t.Errorf("Preflight of a type with nothing to check = %v, want nil", err)
	}

	// A saved configuration the type cannot use is refused in advance too,
	// which is the point: the refusal lands at the write rather than at the
	// first fire.
	if err := router.Preflight(ctx, launchable.Request{
		Target: target("test_sync"), Actor: "somebody", SavedConfigID: 3,
	}); !errors.Is(err, launchable.ErrSavedConfigRefused) {
		t.Errorf("Preflight of a sync with a configuration = %v, want ErrSavedConfigRefused", err)
	}
}
