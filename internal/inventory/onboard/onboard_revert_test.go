// Tests that no failed or interrupted onboarding leaves a record onboarding:
// a cancelled attempt goes back to discovered, a record an earlier attempt
// left onboarding starts over and goes back too, and a record another writer
// moved on during the attempt is never clobbered by the way back.
package onboard

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// funcProber runs probe as its Probe.
type funcProber struct {
	probe func(ctx context.Context, device inventory.InventoryItem) (Probed, error)
}

func (funcProber) Protocol() string { return "http" }

func (p funcProber) Probe(ctx context.Context, device inventory.InventoryItem, _ map[string]string) (Probed, error) {
	return p.probe(ctx, device)
}

// newAPIRepo is a project holding one discovered generic_http device.
func newAPIRepo(t *testing.T) inv.Repository {
	t.Helper()
	repo, _ := newRepo(t, "api1", generic.TypeHTTP, map[string]inventory.PropertyValue{generic.BaseURLProperty: "https://192.0.2.10"})
	return repo
}

// stateOf is name's stored state.
func stateOf(t *testing.T, repo inv.Repository, name string) inventory.LifecycleState {
	t.Helper()
	item, err := repo.GetByName(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return item.State()
}

// TestOnboard_AnInterruptedAttemptReturnsToDiscovered proves an attempt
// whose context ends mid-probe (an interrupt, a deadline) still saves the
// record back as discovered, though the context it ran under is done.
func TestOnboard_AnInterruptedAttemptReturnsToDiscovered(t *testing.T) {
	repo := newAPIRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	interrupted := funcProber{probe: func(ctx context.Context, _ inventory.InventoryItem) (Probed, error) {
		cancel()
		return Probed{}, ctx.Err()
	}}
	res, err := onboardWith(ctx, repo, "api1", staticSecrets(nil), fixedNow, only(interrupted), options{})
	if !errors.Is(err, context.Canceled) || res.State != "discovered" {
		t.Fatalf("an interrupted attempt: %v, result %+v", err, res)
	}
	if got := stateOf(t, repo, "api1"); got != inventory.StateDiscovered {
		t.Errorf("the record is %s after an interrupted attempt, want discovered", got)
	}
}

// TestOnboard_ARecordLeftOnboardingStartsOver proves a record an attempt
// left onboarding (the process killed mid-attempt, which no revert outlives)
// is not stuck: the next attempt says so and starts over, going back to
// discovered when it fails and to active when it succeeds.
func TestOnboard_ARecordLeftOnboardingStartsOver(t *testing.T) {
	repo := newAPIRepo(t)
	ctx := context.Background()
	left, _ := repo.GetByName(ctx, "api1")
	if _, err := moveTo(ctx, repo, left, inventory.StateOnboarding); err != nil {
		t.Fatal(err)
	}

	failing := funcProber{probe: func(context.Context, inventory.InventoryItem) (Probed, error) {
		return Probed{}, errors.New("the API refused the credential (401)")
	}}
	res, err := onboardWith(ctx, repo, "api1", staticSecrets(nil), fixedNow, only(failing), options{})
	if err == nil || res.PreviousState != "onboarding" || res.State != "discovered" || !strings.Contains(strings.Join(res.Warnings, "\n"), "left onboarding by an attempt that did not finish") {
		t.Fatalf("a failed attempt on a record left onboarding: %v, result %+v", err, res)
	}
	if got := stateOf(t, repo, "api1"); got != inventory.StateDiscovered {
		t.Errorf("the record is %s, want discovered", got)
	}

	// Left onboarding a second time, as a second killed attempt would leave it.
	again, _ := repo.GetByName(ctx, "api1")
	if _, err := moveTo(ctx, repo, again, inventory.StateOnboarding); err != nil {
		t.Fatal(err)
	}
	proving := funcProber{probe: func(context.Context, inventory.InventoryItem) (Probed, error) {
		return Probed{Capabilities: []capability.Name{capability.NameHTTPAPI}, Facts: map[string]any{"status": "200"}}, nil
	}}
	res, err = onboardWith(ctx, repo, "api1", staticSecrets(nil), fixedNow, only(proving), options{})
	if err != nil || res.PreviousState != "onboarding" || res.State != "active" {
		t.Fatalf("a successful attempt on a record left onboarding: %v, result %+v", err, res)
	}
}

// TestOnboard_TheWayBackNeverClobbersAnotherWriter proves a failed attempt
// whose record another writer saved meanwhile leaves that writer's record as
// it is, onboarding, and says the way back failed, rather than saving its
// own stale copy over it.
func TestOnboard_TheWayBackNeverClobbersAnotherWriter(t *testing.T) {
	repo := newAPIRepo(t)
	ctx := context.Background()
	racing := funcProber{probe: func(ctx context.Context, device inventory.InventoryItem) (Probed, error) {
		other, err := repo.GetByName(ctx, device.Name())
		if err != nil {
			return Probed{}, err
		}
		if err := other.AddInfo("note", "set by another writer", true); err != nil {
			return Probed{}, err
		}
		if err := repo.Save(ctx, other); err != nil {
			return Probed{}, err
		}
		return Probed{}, errors.New("the API refused the credential (401)")
	}}
	res, err := onboardWith(ctx, repo, "api1", staticSecrets(nil), fixedNow, only(racing), options{})
	if !errors.Is(err, inv.ErrVersionConflict) || !strings.Contains(err.Error(), "another writer already saved api1 as onboarding") || res.State != "onboarding" {
		t.Fatalf("a failed attempt racing another writer: %v, result %+v", err, res)
	}
	item, _ := repo.GetByName(ctx, "api1")
	if note, _ := item.Properties().String("note"); note != "set by another writer" || item.State() != inventory.StateOnboarding {
		t.Errorf("the other writer's record was changed: state %s, note %q", item.State(), note)
	}
}

// stallingRepo is a store whose Save, once stall is set, waits for its context
// and returns that context's error: a store that has stopped answering.
type stallingRepo struct {
	inv.Repository
	// stall makes Save wait until its context ends, as a context-honouring store would.
	stall bool
	// block, when set, makes Save and GetByName wait on it and ignore their
	// context, as the file store does with its lock.
	block chan struct{}
}

// Save blocks as stall or block says.
func (r *stallingRepo) Save(ctx context.Context, item inventory.InventoryItem) error {
	if r.block != nil {
		<-r.block
		return errors.New("the store was released")
	}
	if r.stall {
		<-ctx.Done()
		return ctx.Err()
	}
	return r.Repository.Save(ctx, item)
}

// GetByName blocks as block says, ignoring its context.
func (r *stallingRepo) GetByName(ctx context.Context, name string) (inventory.InventoryItem, error) {
	if r.block != nil {
		<-r.block
		return nil, errors.New("the store was released")
	}
	return r.Repository.GetByName(ctx, name)
}

// TestOnboard_TheWayBackIsBoundedWhenTheStoreStalls proves a failed attempt
// whose store stops answering during the way back returns within the revert
// deadline, naming the failure that started it, rather than waiting forever
// for a write that never completes.
func TestOnboard_TheWayBackIsBoundedWhenTheStoreStalls(t *testing.T) {
	old := revertTimeout
	revertTimeout = 50 * time.Millisecond
	t.Cleanup(func() { revertTimeout = old })

	repo := &stallingRepo{Repository: newAPIRepo(t)}
	stalls := funcProber{probe: func(ctx context.Context, device inventory.InventoryItem) (Probed, error) {
		repo.stall = true
		return Probed{}, errors.New("the API refused the credential (401)")
	}}
	res, err := runBounded(t, repo, stalls)
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "so it is now onboarding") || res.State != "onboarding" {
		t.Fatalf("a stalled way back did not name the failure and the record's state: %v, result %+v", err, res)
	}
}

// TestOnboard_TheWayBackIsBoundedWhenTheStoreIgnoresItsContext proves a failed
// attempt whose store ignores its context on both writes and reads, as the
// file store's lock does, still returns at the revert deadline. It reports the
// record's state as unknown, never as onboarding, since the store could not say.
func TestOnboard_TheWayBackIsBoundedWhenTheStoreIgnoresItsContext(t *testing.T) {
	old := revertTimeout
	revertTimeout = 50 * time.Millisecond
	t.Cleanup(func() { revertTimeout = old })

	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	repo := &stallingRepo{Repository: newAPIRepo(t)}
	locked := funcProber{probe: func(ctx context.Context, device inventory.InventoryItem) (Probed, error) {
		repo.block = block
		return Probed{}, errors.New("the API refused the credential (401)")
	}}
	res, err := runBounded(t, repo, locked)
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "its state is unknown") || res.State != "unknown" {
		t.Fatalf("a store that ignores its context did not leave the state unknown: %v, result %+v", err, res)
	}
}

// runBounded runs a failing onboarding of api1 over repo and fails the test if
// it has not returned within five seconds, which a missing deadline would cause.
func runBounded(t *testing.T, repo inv.Repository, p funcProber) (Result, error) {
	t.Helper()
	type outcome struct {
		res Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := onboardWith(context.Background(), repo, "api1", staticSecrets(nil), fixedNow, only(p), options{})
		done <- outcome{res, err}
	}()
	select {
	case o := <-done:
		return o.res, o.err
	case <-time.After(5 * time.Second):
		t.Fatal("the way back waited on a stalled store with no deadline")
		return Result{}, nil
	}
}
