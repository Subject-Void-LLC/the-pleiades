package collection

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/registry"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Method is a Collection method's real implementation: the function a
// runbook task actually invokes.
//
// device is the inventory item this invocation targets, and is nil only for
// a task with no target at all. A method needs it for the same reason
// Ansible modules need a host: a method that cannot tell which device it is
// acting on can only act on something hardcoded. It is pkg/inventory's
// InventoryItem specifically, so a Collection package still imports nothing
// outside pkg/, which is the constraint a third-party Collection will have
// to satisfy once Part X's OCI distribution exists.
//
// It returns Result rather than a bare error so a method can report whether
// it changed anything, which is the Ansible changed/ok distinction the
// engine's convergence principle depends on. Facts and stats flow through
// the RunbookContext instead, since a method may emit many of those and
// they are not the method's return value.
type Method func(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (Result, error)

// Result is what a Method reports.
type Result struct {
	// Changed reports whether this invocation altered real state. A
	// read-only method (a facts gatherer) always reports false: reading is
	// not changing, and a method that claimed otherwise would make every
	// run look like it converged something.
	Changed bool
}

// Descriptor is one entry in the collection registry: a fully namespaced
// method name, the Manifest describing what it needs and whether it is
// implemented yet, and the function that runs it.
type Descriptor struct {
	Name     string
	Manifest Manifest

	// Invoke is the method's implementation, or nil for a declared stub
	// that has none yet.
	//
	// It lives on the descriptor rather than in a second parallel registry
	// because the alternative is two tables keyed by the same string that
	// can disagree: one saying a method exists and another saying how to
	// run it. Registration already has the function in scope (a generated
	// package's init sits in the same file as its method), so carrying it
	// here costs nothing and removes a whole class of "registered but not
	// dispatchable" drift.
	//
	// A nil Invoke on a manifest claiming StatusImplemented is a
	// contradiction, and Register rejects it rather than letting it surface
	// at run time as a nil-pointer panic three tasks into a runbook.
	Invoke Method

	// Check is the method's check-mode implementation, or nil for a method
	// that cannot say what it would change without changing it.
	//
	// It takes exactly the arguments Invoke does and must change nothing
	// on the device. It reads the state Invoke would have read, compares
	// it against what the task asked for, and reports Result.Changed as
	// "a real run would change something". It may record stats, including
	// the ones Invoke records. A diff it records (sdk.RecordDiff) holds the
	// state it read as Before and the state a real run would leave as
	// After: predicted rather than read back, since nothing was written,
	// which is exactly the "what would this do" answer a dry run exists to
	// give. It must never record an inverse through
	// sdk.RecordInverse: nothing was done, so there is nothing to undo, and
	// the engine refuses a check that records one rather than letting a
	// future rollback read an undo for a change that never happened.
	//
	// It sits beside Invoke for the same reason Invoke sits here at all:
	// one table, so "declared to support check" and "has a Check function"
	// cannot drift apart. Register refuses a Check whose manifest does not
	// set SupportsCheck, and the reverse.
	Check Method

	// CheckCall is, for a method whose Check covers only some calls, the
	// part of its answer that reads nothing but the call's parameters:
	// nil when a call with params can be checked, and an error (a
	// CannotCheckError, through CannotCheck) saying why when it cannot.
	// It is nil for a method that can check every call.
	//
	// It exists so a runbook can be told before it runs. Validation calls
	// it to refuse check_mode on a call that could only ever be reported
	// unchecked, which is exactly what the check_mode key promises not to
	// do. Check must give the same answer for the same parameters, and
	// the simple way to keep that true is for Check to call this very
	// function, as exec.command's does. Register refuses it on a method
	// that does not declare check support, since it would be answering a
	// question nothing asks.
	//
	// It never contacts a device and must not depend on one. A method
	// provided by an external Collection program has none, since a
	// program's description carries only data, and its call-level answer
	// arrives from its Check at run time instead.
	CheckCall func(params map[string]any) error

	// DeviceCall is, for a method whose device is optional
	// (ExecutionContext.Device is DeviceOptional), the answer to whether a
	// call with params acts on a device: http.request with a path on a
	// device's API does, and with a full URL does not. It reads nothing
	// but the parameters, so validation can ask it before a run, and it is
	// nil for every other method. It follows CheckCall's precedent: a
	// question about a call answered from the call alone. Register refuses
	// it on a method whose device is not optional, and refuses an optional
	// device without it (checkExecutionContext).
	DeviceCall func(params map[string]any) bool

	// Provider is nil for a method compiled into this binary. For a method
	// an external Collection program provides, it names that program. Only
	// the loader that runs the program sets it, when it registers the
	// method: nothing a program says about itself does, since a
	// description carries only a name and a manifest. The engine reads it
	// to keep a third party's Check, which nothing has proven only reads,
	// away from a simulate-locked device.
	Provider *Provider
}

// Provider is where a method's code comes from when it is not compiled
// into this binary: an external Collection program.
type Provider struct {
	// Program is the path of the external Collection program.
	Program string

	// Digest is the SHA-256 digest ("sha256:<hex>") the program was
	// loaded with.
	Digest string
}

// collections holds every registered Collection method. It is built on
// pkg/registry.Registry (Phase 6's Section 25 "typed generic Registry"),
// so this package is that primitive's third consumer, not a second
// Registry implementation sitting beside it.
var collections = registry.New[Descriptor]()

// SnapshotForTest captures the process-wide Collection method registry and returns a
// function that puts it back, for a test that registers into it.
//
// Without this a test's registration outlives the test, so a second
// iteration under `go test -count>1` fails on a duplicate registration
// rather than starting clean. Call it once at the top of such a test:
//
//	t.Cleanup(collection.SnapshotForTest())
//
// It is exported rather than living in an export_test.go because a
// _test.go file cannot be imported across package boundaries, and tests in
// other packages register here too. internal/archtest forbids production
// code from calling it.
func SnapshotForTest() func() {
	return collections.SnapshotForTest()
}

// Register adds d under d.Name, rejecting a bare (non-namespaced) name, an
// empty namespace or method segment, an unknown required capability, a
// documented parameter the engine reads for itself (see
// checkReservedParams), a check declaration that contradicts itself (see
// checkCheckSupport), or a duplicate name. It returns an error rather than panicking, for genuine
// runtime registration where a rejection is a data problem the caller must
// handle, not a process-ending programmer error.
//
// Decision, recorded per this phase's own checklist requirement: a
// duplicate name is always rejected, never resolved by first-write-wins or
// last-write-wins. Part X's Phase 44 records that a decentralized registry
// makes namespace collision a routine outcome, not a bug, because no
// registry can enforce ownership of a name it has never heard of.
// First-write-wins is a hijack primitive once a colliding name can arrive
// from a stranger; last-write-wins is worse. Neither wins here: the second
// registration simply fails, and the caller decides what that means for
// its own workflow.
func Register(d Descriptor) error {
	namespace, method, ok := strings.Cut(d.Name, ".")
	if !ok || namespace == "" || method == "" {
		return fmt.Errorf("collection: %q is not namespaced (requires <namespace>.<method>)", d.Name)
	}

	for _, name := range d.Manifest.RequiredCapabilities {
		if _, known := capability.Lookup(name); !known {
			return fmt.Errorf("collection: %q requires unknown capability %q", d.Name, name)
		}
	}

	if err := checkTransportNames(d); err != nil {
		return err
	}

	// A method claiming to be implemented must carry an implementation.
	// Catching it here turns what would be a nil-pointer panic partway
	// through a runbook into a refusal at process start, which is the same
	// "type safety moves left" rule the rest of this registry follows.
	if d.Manifest.Status == StatusImplemented && d.Invoke == nil {
		return fmt.Errorf("collection: %q claims status %q but carries no implementation", d.Name, StatusImplemented)
	}

	if err := checkReversibility(d); err != nil {
		return err
	}

	if err := checkReservedParams(d); err != nil {
		return err
	}

	if err := checkCheckSupport(d); err != nil {
		return err
	}

	if err := checkSeedsLogin(d); err != nil {
		return err
	}

	if err := checkExecutionContext(d); err != nil {
		return err
	}

	return collections.Register(d.Name, d)
}

// checkSeedsLogin refuses a SeedsLogin that names no required string
// parameter, and any SeedsLogin on an external Collection's method: a
// program outside this binary gets the credentials bound to its run and
// its target's own, never material derived from another device's.
func checkSeedsLogin(d Descriptor) error {
	name := d.Manifest.SeedsLogin
	if name == "" {
		if d.Manifest.SeedsLoginPassword {
			return fmt.Errorf("collection: %q sets SeedsLoginPassword without SeedsLogin, so it names no login to seed", d.Name)
		}
		return nil
	}
	if d.Provider != nil {
		return fmt.Errorf("collection: %q is an external Collection's method, which cannot seed another device's login", d.Name)
	}
	for _, p := range d.Manifest.Doc.Params {
		if p.Name == name && p.Type == "string" && p.Required {
			return nil
		}
	}
	return fmt.Errorf("collection: %q seeds the login named by parameter %q, which it does not declare as a required string", d.Name, name)
}

// MustRegister calls Register and panics on error. This is for
// compile-time-known registrations made from a Collection package's own
// init(), mirroring pkg/capability's and pkg/registry's own convention: a
// malformed or duplicate built-in name is a programming error that must
// fail loudly at process start, never silently pick one registration over
// the other.
func MustRegister(d Descriptor) {
	if err := Register(d); err != nil {
		panic("collection: " + err.Error())
	}
}

// Lookup returns the Descriptor registered under name, and whether it was
// found.
func Lookup(name string) (Descriptor, bool) {
	return collections.Get(name)
}

// BuiltinNamespaces returns, sorted, the namespace (the first dotted part
// of the name) of every registered method compiled into this binary,
// that is every method with no Provider, implemented or declared. A
// loader of external Collections reserves these, so a name in one of
// them can only ever mean code that ships with Pleiades.
func BuiltinNamespaces() []string {
	seen := map[string]bool{}
	for name, d := range collections.All() {
		if d.Provider != nil {
			continue
		}
		if ns, _, ok := strings.Cut(name, "."); ok {
			seen[ns] = true
		}
	}
	namespaces := make([]string, 0, len(seen))
	for ns := range seen {
		namespaces = append(namespaces, ns)
	}
	sort.Strings(namespaces)
	return namespaces
}
