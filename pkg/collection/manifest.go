// Package collection implements the Phase 31 Collection registry: the
// namespaced method registry (Descriptor, Register, Lookup) and the
// Manifest format describing what a Collection method needs from a device
// and whether it has a real implementation yet.
//
// pkg/registry.Registry[T] (Phase 6's Section 25 "typed generic Registry")
// already exists and already has two consumers (pkg/capability's
// capability vocabulary, internal/inventory/record's device-type table).
// This package is its third consumer, not a second Registry
// implementation: a shared primitive has exactly one implementation in
// this codebase, and a second one is a defect, not a variation.
package collection

import "github.com/Subject-Void-LLC/the-pleiades/pkg/capability"

// Status records whether a registered Collection method has a real
// implementation yet, or only a declared Manifest whose stub returns an
// explicit "not implemented" error until it does. docs/hephaestus.md
// calls this the "declared is not implemented" guardrail: the gap between
// a catalog entry and its real behavior is data the tooling reads, not
// knowledge people carry.
type Status string

const (
	// StatusDeclared means the Manifest is registered but its method has
	// no real implementation yet; calling it must return an explicit
	// "not implemented" error, never a silent success.
	StatusDeclared Status = "declared"

	// StatusImplemented means the method behind this Manifest actually
	// runs.
	StatusImplemented Status = "implemented"
)

// PlatformTarget narrows a Manifest to a specific vendor, model, firmware
// range, or deployment context, matched against a device's classification
// and fact data at plan time. It is never a capability: Phase 32's
// capability-granularity decision draws this line explicitly: capabilities
// stop at the protocol/family floor (AptCapable, JunosCapable), and a
// specific SKU, firmware range, or deployment context (a carrier-only
// build, a version pinned to exactly x.y.z) belongs here instead. An empty
// field on a PlatformTarget means that dimension is unconstrained.
type PlatformTarget struct {
	Vendor            string `json:"vendor,omitempty"`
	Model             string `json:"model,omitempty"`
	VersionRange      string `json:"versionRange,omitempty"`
	DeploymentContext string `json:"deploymentContext,omitempty"`
}

// ExecutionContext describes what environment a Collection method needs
// beyond its transport and capability requirements.
type ExecutionContext struct {
	// RequiresElevation reports whether this method needs elevated
	// (root/administrator) privileges on the target device.
	RequiresElevation bool `json:"requiresElevation,omitempty"`
}

// Reversibility is a method's static answer to one question: can this
// method ever produce an instruction that undoes what it did?
//
// IT IS DELIBERATELY NOT A DESCRIPTION OF THE INVERSE, and an earlier
// version of this type was, which is the mistake worth recording. That
// version named an inverse FQCN and the prior-state keys a rollback would
// have to feed it. It could not work, because the true inverse is almost
// never a property of the METHOD. It is a property of the RUN.
//
// Three examples, each breaking it a different way. Starting a service
// that was already running must undo to nothing at all, not to a stop,
// and a static declaration naming "stop" would tell a rollback to break
// something the run never touched. Removing a file is reversible only if
// the content happened to be captured, which the method knows and the
// manifest cannot. And an HTTP request is read-only or destructive
// depending on a parameter, so one declaration covering every invocation
// has to describe the worst case and is useless for the common one.
//
// So the split is: this type says WHETHER, once, at registration. The run
// says WHAT, every time, by emitting a concrete already-parameterized
// instruction through sdk.RecordInverse. A rollback engine then reads
// something it can execute rather than a template it has to reconstruct.
//
// NOTHING PERFORMS A ROLLBACK YET. There is no journal and no rollback
// engine. What this buys today is that the values an undo needs are
// captured by the forward run, which is the only thing in a position to
// capture them, and that is why it is worth declaring before the engine
// exists rather than after.
type Reversibility struct {
	// Reversible reports whether this method can ever emit an inverse.
	//
	// False is a real and common answer: a method whose effect this
	// platform cannot observe or reconstruct should say so plainly rather
	// than declare an inverse that would do something merely similar.
	// True does not promise that every invocation emits one; a run that
	// changed nothing correctly emits nothing to undo.
	Reversible bool `json:"reversible"`

	// Notes explains the limits in plain words. Required when Reversible
	// is false, because "this cannot be undone" is the answer an operator
	// most needs a reason for, and it is the easiest answer to reach for
	// when the real one is "I did not want to work out the captures."
	// Worth writing when Reversible is true as well, to say what the
	// inverse does NOT restore.
	Notes string `json:"notes,omitempty"`
}

// Manifest is the full declared contract for one namespaced Collection
// method. Its JSON tags are the stable serialized form: Part X's Phase 42
// later embeds this exact document as an OCI artifact's config layer, so
// this struct's JSON encoding is the contract, not a Go-only convenience
// that has to be re-described elsewhere.
//
// The moment a runbook task calls the method this Manifest describes,
// RequiredCapabilities and PlatformTargets become that task's plan-time
// constraint automatically: a runbook author never hand-writes a
// requires: block for the common case. A runbook or task-level
// requirement may narrow that constraint further, via intersection, but
// can never loosen it.
type Manifest struct {
	// SupportedTransports names the transports this method can run over
	// (for example "ssh" or "winrm"). It is a plain string set rather
	// than a reference to a concrete transport type: several transports
	// exist, but no phase has yet claimed typing this field against them.
	// Nothing checks it against a device either, so today it documents a
	// method rather than gating one; RequiredCapabilities is what gates.
	SupportedTransports []string `json:"supportedTransports,omitempty"`

	// RequiredCapabilities is what a device must structurally implement
	// for this method to apply. It reuses pkg/capability's typed Name so
	// an unknown capability is a type someone constructed incorrectly,
	// not a bare string a compiler can't check.
	RequiredCapabilities []capability.Name `json:"requiredCapabilities,omitempty"`

	ExecutionContext ExecutionContext `json:"executionContext,omitempty"`
	PlatformTargets  []PlatformTarget `json:"platformTargets,omitempty"`

	// EngineVersion is a minimum core engine version constraint. It is a
	// bare, unparsed string: the five fields above answer what this
	// method needs from a device, and this one answers whether it can
	// run against a given build, a question that does not arise while
	// every Collection is compiled into the binary that runs it. No
	// semver library is added this phase, since nothing enforces this
	// field yet; adding it now, before Part X lets a Collection arrive
	// from somewhere else, is free.
	EngineVersion string `json:"engineVersion,omitempty"`

	Status Status `json:"status"`

	// Reversibility says whether this method can ever produce an
	// instruction that undoes it. Every implemented method answers,
	// including the read-only ones, which answer false because they
	// changed nothing there is anything to undo. The instruction itself is
	// emitted at run time; see the Reversibility type for why it cannot
	// live here.
	Reversibility Reversibility `json:"reversibility"`

	// SupportsCheck reports whether this method can run in check mode:
	// read the device, compare it against what the task asked for, and
	// report whether a real run would change anything, without changing
	// it. Descriptor.Check is the function behind the answer, and
	// Register refuses a descriptor where the two disagree.
	//
	// It carries no omitempty, so every serialized manifest states it,
	// false included. "This method cannot be checked" is the answer an
	// operator planning a dry run needs to see, and an absent field would
	// read as nobody having asked.
	SupportsCheck bool `json:"supportsCheck"`

	// NoCheckReason says why a method without check support cannot say
	// what it would change without changing it, in words an operator
	// planning a dry run can act on: what a check would have to know, and
	// why only running the change tells. It is empty for a method that
	// supports check, and Register refuses one that carries both, since a
	// reason not to check contradicts a check.
	//
	// It is what a check run's "could not check" line, validation's
	// refusal of check_mode, and the method's reference page all say in
	// place of a bare "does not declare check support". Every built-in
	// implemented method without check support carries one; a test holds
	// the catalog to that. It is optional for an external Collection's
	// method, which gets the bare answer without it.
	NoCheckReason string `json:"noCheckReason,omitempty"`

	// EndsLoginSession reports that a real run of this method can change
	// what a login to the device carries (the account's groups, its shell,
	// the account itself), so a connection logged in before it no longer
	// means what a fresh login would. After such a method runs, the engine
	// closes any connection it kept open to that device, and the device's
	// next task logs in again. It matters only when connections persist
	// between tasks; without that every task logs in afresh anyway.
	EndsLoginSession bool `json:"endsLoginSession,omitempty"`

	// SeedsLogin names the parameter, if any, whose value names an
	// inventory device this method creates a machine for, seeding the
	// machine with that device's stored login. The engine resolves the
	// device's credential and hands the method only what a machine needs
	// to admit it (wire.SecretSeedUsername, SecretSeedAuthorizedKey and
	// SecretSeedPasswordHash): the username, the key's public half and a
	// freshly salted hash of the password, never the key or the password.
	// A tier that cannot resolve it refuses the method rather than
	// running it with no login to seed, and Register refuses it on an
	// external Collection's method.
	SeedsLogin string `json:"seedsLogin,omitempty"`

	// Doc is this method's human-facing reference documentation. See
	// the Doc type's own comment for what a declared method carries
	// versus an implemented one.
	Doc Doc `json:"doc,omitempty"`
}
