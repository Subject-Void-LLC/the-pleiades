// The onboarding orchestrator: the lifecycle it drives, what it records,
// and what it refuses.
package onboard

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	inv "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// The kinds of refusal Onboard reports, so a caller (the API) can answer
// each one differently.
var (
	// ErrNotOnboarded is returned for a device whose type has no prober:
	// its capabilities come from its Go type, so there is nothing to
	// discover.
	ErrNotOnboarded = errors.New("only a device type with a prober is onboarded")

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

// KubernetesLookup finds a credential that may be lent to a cluster the
// per-device store holds none for (the Controller's credential store,
// resolve.BorrowedKubernetesSecrets): nil when there is none. It is handed
// the whole device, since whether a credential may be lent depends on more
// than the API server: the device's organization, route and pinned
// authority.
type KubernetesLookup func(ctx context.Context, cluster inventory.InventoryItem) (map[string]string, error)

// WithKubernetesFallback is primary, except that a Kubernetes cluster for
// which primary has no credential is given the one lookup finds for its
// API server. Any other device, and a cluster primary has a credential for,
// is answered by primary alone.
func WithKubernetesFallback(primary SecretsFunc, lookup KubernetesLookup) SecretsFunc {
	return func(ctx context.Context, device inventory.InventoryItem) (map[string]string, error) {
		secrets, err := primary(ctx, device)
		if err != nil || secrets != nil {
			return secrets, err
		}
		if _, ok := device.(capability.KubernetesCapable); !ok {
			return nil, nil
		}
		return lookup(ctx, device)
	}
}

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
	// FirstUse is the pin this onboarding recorded on first use, for a
	// person to review before confirming it; absent when it recorded none.
	FirstUse *FirstUsePin `json:"tls_first_use,omitempty"`
	Error    string       `json:"error,omitempty"`
	// Grants is what each loaded external program's probe did for the
	// capability it probes (the command line runs them after its own
	// probe; a Controller runs none, since it loads no program).
	Grants []ProbedGrant `json:"grants,omitempty"`
}

// ProbedGrant is what one program's probe did for one capability it
// defines.
type ProbedGrant struct {
	// Capability is the data capability probed.
	Capability string `json:"capability"`
	// Program is the program that defines it and ran the probe.
	Program string `json:"program"`
	// Probe is the probe method.
	Probe string `json:"probe"`
	// Granted says the device answered the probe and now holds it.
	Granted bool `json:"granted"`
	// Withdrawn says the probe found the device no longer answering, and
	// the grant an earlier probe made was taken back.
	Withdrawn bool `json:"withdrawn,omitempty"`
	// Error is why the probe could not decide.
	Error string `json:"error,omitempty"`
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
// its discovery recorded as a revision. A failed or interrupted attempt
// returns a new device to discovered, saved, and leaves an onboarded one as
// it was, with the reason in the Result and the error, so no failure leaves
// a record onboarding (revertOnboarding). Re-onboarding an active device
// re-probes it and writes only when what it proved changed.
func Onboard(ctx context.Context, repo inv.Repository, name string, secrets SecretsFunc, now func() time.Time, opts ...Option) (Result, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	return onboardWith(ctx, repo, name, secrets, now, Lookup, o)
}

// onboardWith is Onboard with the prober lookup passed in, so a test can
// hand a prober settings (a test CA) the registered one does not have.
func onboardWith(ctx context.Context, repo inv.Repository, name string, secrets SecretsFunc, now func() time.Time, lookup func(string) (Prober, bool), o options) (Result, error) {
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
		return res, fmt.Errorf("device %s is a %s, which has no prober: %w (%s)", name, deviceType, ErrNotOnboarded, strings.Join(Types(), ", "))
	}
	res.Protocol = prober.Protocol()
	if !slices.Contains(onboardable, item.State()) {
		return res, fmt.Errorf("device %s is %s: %w", name, item.State(), ErrAdministratorState)
	}

	// A record already onboarding is what an attempt the process did not
	// live to finish (killed, or its machine lost) left behind: this one
	// starts over, and its failure returns the record to discovered too.
	if item.State() == inventory.StateOnboarding {
		res.Warnings = append(res.Warnings, fmt.Sprintf("device %s was left onboarding by an attempt that did not finish; this one starts over", name))
	}
	var pristine inventory.InventoryItem
	if item.State() == inventory.StateDiscovered || item.State() == inventory.StateOnboarding {
		if item.State() == inventory.StateDiscovered {
			if item, err = moveTo(ctx, repo, item, inventory.StateOnboarding); err != nil {
				return res, err
			}
		}
		res.State = item.State().String()
		// A copy as stored now, before the probe's findings are written onto
		// item, is what a failure saves back as discovered: nothing the
		// failed attempt learned is kept, and a record another writer moved
		// on since is refused (inventory.ErrVersionConflict), never clobbered.
		if pristine, err = repo.GetByName(ctx, name); err != nil {
			return res, err
		}
	}
	fail := func(res Result, err error) (Result, error) {
		return revertOnboarding(ctx, repo, pristine, res, err)
	}

	creds, err := secrets(ctx, item)
	if err != nil {
		return fail(res, fmt.Errorf("resolving the credential for %s: %w", name, err))
	}
	// The probe's own verified handshakes say which certificate a TLS
	// device presented (devicetls.WithPresented), with no connection of
	// onboarding's own.
	ctx, presented := devicetls.WithPresented(ctx)
	probe := func(target inventory.InventoryItem) (Probed, error) {
		if pp, ok := prober.(PeerProber); ok {
			return pp.ProbeWithPeers(ctx, target, creds, repoPeers{repo: repo, secrets: secrets, route: o.route})
		}
		return prober.Probe(ctx, target, creds)
	}
	probed, firstUse, trusted, err := probeWithFirstUse(probe, item, o)
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
	res.Warnings = append(res.Warnings, probed.Warnings...)
	res.Warnings = append(res.Warnings, certificateFacts(name, devicetls.For(trusted), presented, now(), next.Facts)...)
	pinChanged, warning, err := recordPinChange(item, firstUse)
	if err != nil {
		return fail(res, err)
	}
	// A type whose discovery holds only for the address it probed binds
	// it to the properties as they are now, the pin just recorded among
	// them, so repointing the device later voids it until it is onboarded
	// again (Phase 117a, finding S2).
	if binder, ok := item.(inventory.DiscoveryBinder); ok {
		next.Binding = binder.DiscoveryBinding()
	}
	prev, _, err := inventory.DiscoveryFrom(item.Properties())
	if err != nil {
		return fail(res, err)
	}
	res.Capabilities = names(next.Capabilities)
	res.Added, res.Removed = diff(prev.Capabilities, next.Capabilities)
	res.Facts = next.Facts

	same := sameDiscovery(prev, next)
	if same && !pinChanged && item.State() == inventory.StateActive {
		return res, nil
	}
	if !same {
		writer, ok := item.(discoveryWriter)
		if !ok {
			return fail(res, fmt.Errorf("device %s cannot record a discovery", name))
		}
		writer.RecordDiscovery(next)
	}
	// The device rebuilt by the real factory from what this onboarding wrote
	// must hold every capability the probe proved. generic's constructors
	// and internal/archtest make this true; checking it before anything is
	// saved means a regression refuses the onboarding whole, rather than
	// leaving a stored discovery or pin a failed result does not report.
	rebuilt, err := inv.NewItemFactory().Rebuilt(item)
	if err != nil {
		return fail(res, fmt.Errorf("device %s cannot be rebuilt with what the probe proved: %w", name, err))
	}
	for _, c := range next.Capabilities {
		if !rebuilt.HasCapability(c) {
			return fail(res, fmt.Errorf("device %s was granted %s and does not hold it when rebuilt; nothing was saved", name, c))
		}
	}
	if _, err = moveTo(ctx, repo, item, inventory.StateActive); err != nil {
		return fail(res, err)
	}
	res.State = inventory.StateActive.String()
	res.Changed = true
	// Reported only once it is stored, so a failed save never lists a pin
	// it did not record.
	if warning != "" {
		res.Warnings = append(res.Warnings, warning)
	}
	res.FirstUse = firstUse
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

// revertTimeout bounds the way back to discovered. The way back runs after an
// interrupt, so it has no caller's deadline to rely on, and a stalled store
// must not hold it forever. A variable so a test can shorten it.
var revertTimeout = 10 * time.Second

// revertOnboarding is fail for an attempt that moved the record to
// onboarding: pristine, the record as stored at onboarding before the probe,
// is saved back as discovered, so a failed attempt never leaves a record
// onboarding. It runs whatever ended the attempt, a cancelled context (an
// interrupt, a deadline) included, since the save that moved the record was
// made. A nil pristine (an onboarded device, which stays as it was) reverts
// nothing. A revert that fails, a record another writer moved on since among
// them, is said in the error, and the result then says onboarding.
func revertOnboarding(ctx context.Context, repo inv.Repository, pristine inventory.InventoryItem, res Result, err error) (Result, error) {
	if pristine != nil {
		revertCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), revertTimeout)
		defer cancel()
		revertErr := storeCall(revertCtx, func() error {
			_, e := moveTo(revertCtx, repo, pristine, inventory.StateDiscovered)
			return e
		})
		if revertErr == nil {
			res.State = inventory.StateDiscovered.String()
		} else {
			res, err = describeFailedRevert(ctx, repo, pristine, res, err, revertErr)
		}
	}
	return fail(res, err)
}

// describeFailedRevert says, after a way back that failed, what the record is
// now. The read has a budget of its own, since the failed save may have spent
// the first. A record that cannot be read is unknown, never assumed onboarding:
// a racing writer may have moved it, and a save that timed out may still land.
func describeFailedRevert(ctx context.Context, repo inv.Repository, pristine inventory.InventoryItem, res Result, err, revertErr error) (Result, error) {
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), revertTimeout)
	defer cancel()
	var current inventory.InventoryItem
	readErr := storeCall(readCtx, func() error {
		var e error
		current, e = repo.GetByName(readCtx, pristine.Name())
		return e
	})
	if readErr != nil {
		res.State = "unknown"
		return res, fmt.Errorf("%w; and returning %s to discovered failed, so its state is unknown: %w", err, res.Device, revertErr)
	}
	res.State = current.State().String()
	if errors.Is(revertErr, inv.ErrVersionConflict) {
		return res, fmt.Errorf("%w; the way back to discovered was refused because another writer already saved %s as %s: %w", err, res.Device, res.State, revertErr)
	}
	return res, fmt.Errorf("%w; and returning %s to discovered failed, so it is now %s: %w", err, res.Device, res.State, revertErr)
}

// storeCall runs call and waits for it until ctx ends. A store may ignore its
// context (the file store takes its lock with none), so the caller stops
// waiting at the deadline and the call finishes, or never does, in the
// background; its result is then unknown to the caller.
func storeCall(ctx context.Context, call func() error) error {
	done := make(chan error, 1)
	go func() { done <- call() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
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

// repoPeers is Peers over the inventory being onboarded and its credential
// lookup.
type repoPeers struct {
	repo    inv.Repository
	secrets SecretsFunc
	route   RouteFunc
}

// Route returns a dialer through device's route, through the RouteFunc
// onboarding was given (WithRoutes), or nil.
func (p repoPeers) Route(ctx context.Context, device inventory.InventoryItem) (sdk.RouteDial, error) {
	if p.route == nil {
		return nil, nil
	}
	return p.route(ctx, device)
}

// RouteFunc resolves a device's route of SSH hops into a dialer, nil when
// it has none.
type RouteFunc func(ctx context.Context, device inventory.InventoryItem) (sdk.RouteDial, error)

// Option configures one onboarding.
type Option func(*options)

// options are an onboarding's settings.
type options struct {
	route RouteFunc
	// firstUse records the certificate a device presents as its pin when
	// nothing in its record trusts it (WithFirstUsePins).
	firstUse bool
}

// WithRoutes lets a prober reach a device through its route of SSH hops
// (a cluster behind a bastion), resolved by route.
func WithRoutes(route RouteFunc) Option {
	return func(o *options) { o.route = route }
}

// Device returns the device named name and its own credential.
func (p repoPeers) Device(ctx context.Context, name string) (inventory.InventoryItem, map[string]string, error) {
	item, err := p.repo.GetByName(ctx, name)
	if err != nil {
		return nil, nil, err
	}
	creds, err := p.secrets(ctx, item)
	if err != nil {
		return nil, nil, fmt.Errorf("resolving the credential for %s: %w", name, err)
	}
	return item, creds, nil
}
