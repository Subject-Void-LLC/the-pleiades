// Package collection implements the Phase 31 Collection registry: the
// namespaced method registry (Descriptor, Register, Lookup) and the
// Manifest format describing what a Collection method needs from a device
// and whether it has a real implementation yet.
//
// pkg/registry.Registry[T] (Phase 6's Section 25 "typed generic Registry")
// already exists and already has two consumers (pkg/capability's
// capability vocabulary, internal/inventory/record's device-type table).
// This package is its third consumer, not a second Registry
// implementation: PLAN.md Section 25's own rule is that a listed
// primitive has exactly one implementation in the codebase, and a second
// one is a defect, not a variation.
package collection

import "github.com/SubjectVoidLLC/the-pleiades/pkg/capability"

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

// Manifest is the full declared contract for one namespaced Collection
// method. Its JSON tags are the stable serialized form: Part X's Phase 42
// later embeds this exact document as an OCI artifact's config layer, so
// this struct's JSON encoding is the contract, not a Go-only convenience
// that has to be re-described elsewhere.
//
// The moment a runbook task calls the method this Manifest describes,
// RequiredCapabilities and PlatformTargets become that task's plan-time
// constraint automatically (PLAN.md Section 8's audience split: a runbook
// author never hand-writes a requires: block for the common case). A
// runbook or task-level requirement may narrow that constraint further
// (PLAN.md Section 25's intersection merge mode) but can never loosen it.
type Manifest struct {
	// SupportedTransports names the transports this method can run over
	// (for example "ssh"). It is a plain string set rather than a
	// reference to a concrete transport type: only one transport exists
	// in this codebase today, and binding this field to it ahead of a
	// second transport existing would be premature structure.
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
}
