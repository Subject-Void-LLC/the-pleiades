// The onboarding orchestrator: the lifecycle it drives, what it records,
// and what it refuses.
package onboard

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// The kinds of refusal Onboard reports, so a caller (the API) can answer
// each one differently.
var (
	// ErrNotOnboarded is returned for a device whose type has no prober:
	// its capabilities come from its Go type, so there is nothing to
	// discover.
	ErrNotOnboarded = errors.New("only a generic device type is onboarded")

	// ErrAdministratorState is returned for a device in a state a person
	// set, which onboarding does not overrule.
	ErrAdministratorState = errors.New("an administrator moves it out of that state before it is onboarded")

	// ErrProbe is returned when the probe proved nothing: the device was
	// unreachable, refused the credential, or did not speak its protocol.
	ErrProbe = errors.New("the probe proved nothing")
)

// SecretsFunc resolves a device's own credential, flattened under the
// wire.Secret* keys, or nil when none is stored.
type SecretsFunc func(ctx context.Context, device inventory.InventoryItem) (map[string]string, error)

// SecretsFrom resolves a device's credential from store by the device's
// name, the same lookup a run makes, flattened under the wire.Secret*
// keys. A device with no stored credential gets nil, which a probe that
// needs one refuses.
func SecretsFrom(store credential.Store) SecretsFunc {
	return func(ctx context.Context, device inventory.InventoryItem) (map[string]string, error) {
		cred, err := store.Lookup(ctx, device.Name())
		if errors.Is(err, credential.ErrNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return credential.Flatten(cred), nil
	}
}

// Result is what one onboarding did, in the model both the terminal and
// --json print.
type Result struct {
	Device        string         `json:"device"`
	Type          string         `json:"type"`
	Protocol      string         `json:"protocol"`
	PreviousState string         `json:"previous_state"`
	State         string         `json:"state"`
	Capabilities  []string       `json:"capabilities"`
	Added         []string       `json:"added"`
	Removed       []string       `json:"removed"`
	Facts         map[string]any `json:"facts"`
	Changed       bool           `json:"changed"`
	// Warnings says what each weakening the device's record allows means.
	// It is never empty for a device reached over deprecated TLS, legacy
	// ciphers or a plain-HTTP credential.
	Warnings []string `json:"warnings"`
	Error    string   `json:"error,omitempty"`
}

// discoveryWriter and stateChanger are the mutators every item the factory
// builds has, through record.Base; they are asserted structurally so
// pkg/inventory's item contract gains no setters.
type discoveryWriter interface {
	RecordDiscovery(inventory.Discovery)
}

type stateChanger interface {
	ChangeState(inventory.LifecycleState) bool
}

type deviceTyped interface {
	DeviceType() string
}

// onboardable is every state onboarding may start from. A quarantined,
// simulate-locked, decommissioning or archived device was put there by a
// person, and onboarding does not overrule that.
var onboardable = []inventory.LifecycleState{
	inventory.StateDiscovered,
	inventory.StateOnboarding,
	inventory.StateActive,
	inventory.StateUnreachable,
}

// Onboard probes the named device over its protocol and records the
// result. A discovered device moves to onboarding (saved, so the attempt
// is visible) and then, when the probe proves the device, to active with
// its discovery recorded as a revision. A failed probe leaves a new device
// onboarding and an onboarded one as it was, with the reason in the
// Result and the error. Re-onboarding an active device re-probes it and
// writes only when what it proved changed.
func Onboard(ctx context.Context, repo inv.Repository, name string, secrets SecretsFunc, now func() time.Time) (Result, error) {
	return onboardWith(ctx, repo, name, secrets, now, Lookup)
}

// onboardWith is Onboard with the prober lookup passed in, so a test can
// hand a prober settings (a test CA) the registered one does not have.
func onboardWith(ctx context.Context, repo inv.Repository, name string, secrets SecretsFunc, now func() time.Time, lookup func(string) (Prober, bool)) (Result, error) {
	item, err := repo.GetByName(ctx, name)
	if err != nil {
		return Result{Device: name}, err
	}
	typed, ok := item.(deviceTyped)
	if !ok {
		return Result{Device: name}, fmt.Errorf("device %s reports no type", name)
	}
	deviceType := typed.DeviceType()
	res := Result{Device: name, Type: deviceType, PreviousState: item.State().String(), State: item.State().String()}
	prober, ok := lookup(deviceType)
	if !ok {
		return res, fmt.Errorf("device %s is a %s: %w", name, deviceType, ErrNotOnboarded)
	}
	res.Protocol = prober.Protocol()
	if !slices.Contains(onboardable, item.State()) {
		return res, fmt.Errorf("device %s is %s: %w", name, item.State(), ErrAdministratorState)
	}

	if item.State() == inventory.StateDiscovered {
		if item, err = moveTo(ctx, repo, item, inventory.StateOnboarding); err != nil {
			return res, err
		}
		res.State = item.State().String()
	}

	creds, err := secrets(ctx, item)
	if err != nil {
		return fail(res, fmt.Errorf("resolving the credential for %s: %w", name, err))
	}
	probed, err := prober.Probe(ctx, item, creds)
	if err != nil {
		return fail(res, fmt.Errorf("%w: probing %s over %s: %w", ErrProbe, name, res.Protocol, err))
	}
	allowed := generic.Discoverable(deviceType)
	for _, c := range probed.Capabilities {
		if !slices.Contains(allowed, c) {
			return fail(res, fmt.Errorf("the %s prober reported %s, which a %s may not be granted", res.Protocol, c, deviceType))
		}
	}

	next := inventory.Discovery{Protocol: res.Protocol, Capabilities: sorted(probed.Capabilities), Facts: probed.Facts, ProbedAt: now().UTC()}
	if next.Facts == nil {
		next.Facts = map[string]any{}
	}
	// A type whose discovery holds only for the address it probed binds
	// it to the properties as they are now, so repointing the device later
	// voids it until it is onboarded again (Phase 117a, finding S2).
	if binder, ok := item.(inventory.DiscoveryBinder); ok {
		next.Binding = binder.DiscoveryBinding()
	}
	prev, _, err := inventory.DiscoveryFrom(item.Properties())
	if err != nil {
		return fail(res, err)
	}
	res.Warnings = probed.Warnings
	res.Capabilities = names(next.Capabilities)
	res.Added, res.Removed = diff(prev.Capabilities, next.Capabilities)
	res.Facts = next.Facts

	same := sameDiscovery(prev, next)
	if same && item.State() == inventory.StateActive {
		return res, nil
	}
	if !same {
		writer, ok := item.(discoveryWriter)
		if !ok {
			return fail(res, fmt.Errorf("device %s cannot record a discovery", name))
		}
		writer.RecordDiscovery(next)
	}
	if item, err = moveTo(ctx, repo, item, inventory.StateActive); err != nil {
		return fail(res, err)
	}
	res.State = item.State().String()
	res.Changed = true

	// The stored device, rebuilt by the real factory, must now hold every
	// capability the probe proved. generic's constructors and
	// internal/archtest make this true; checking it here means a
	// regression reports rather than leaving a device that claims less
	// than it was told.
	for _, c := range next.Capabilities {
		if !item.HasCapability(c) {
			return fail(res, fmt.Errorf("device %s was granted %s and does not hold it after saving", name, c))
		}
	}
	return res, nil
}

// moveTo changes item's state, saves it, and returns the stored item.
func moveTo(ctx context.Context, repo inv.Repository, item inventory.InventoryItem, state inventory.LifecycleState) (inventory.InventoryItem, error) {
	changer, ok := item.(stateChanger)
	if !ok {
		return nil, fmt.Errorf("device %s cannot change state", item.Name())
	}
	changer.ChangeState(state)
	if err := repo.Save(ctx, item); err != nil {
		return nil, fmt.Errorf("saving %s: %w", item.Name(), err)
	}
	return repo.GetByName(ctx, item.Name())
}

// fail records err on res and returns both.
func fail(res Result, err error) (Result, error) {
	res.Error = err.Error()
	return res, err
}

// sameDiscovery reports whether a and b prove the same thing, ignoring
// when each was probed. Facts are text or lists of text (Probed's
// contract), which every inventory store round-trips to the same Go
// values, so a stored discovery compares equal to a fresh one.
func sameDiscovery(a, b inventory.Discovery) bool {
	return a.Protocol == b.Protocol && slices.Equal(a.Capabilities, b.Capabilities) && reflect.DeepEqual(a.Facts, b.Facts) &&
		a.Binding == b.Binding
}

// sorted returns caps sorted and without duplicates.
func sorted(caps []capability.Name) []capability.Name {
	out := slices.Clone(caps)
	slices.Sort(out)
	return slices.Compact(out)
}

// names returns caps as text.
func names(caps []capability.Name) []string {
	out := make([]string, 0, len(caps))
	for _, c := range caps {
		out = append(out, string(c))
	}
	return out
}

// diff returns what next adds to prev and what it drops.
func diff(prev, next []capability.Name) (added, removed []string) {
	added, removed = []string{}, []string{}
	for _, c := range next {
		if !slices.Contains(prev, c) {
			added = append(added, string(c))
		}
	}
	for _, c := range prev {
		if !slices.Contains(next, c) {
			removed = append(removed, string(c))
		}
	}
	return added, removed
}
