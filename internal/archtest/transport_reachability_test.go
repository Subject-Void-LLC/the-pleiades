// This file is the transport half of the registry consistency sweep.
//
// registry_sweep_test.go's TestImplementedCollectionCapabilitiesAreSatisfiable
// walks catalogdata.Collections, which is the Collection catalog and
// nothing else. A transport fqcn ("ssh_exec", "serial_exec", ...) is not
// a Collection method and appears nowhere in that table, so the whole
// transport dispatch path sat outside that guard's coverage: Phase 73
// shipped four capabilities, three bindings and three fqcns that no
// registered device type could satisfy, in the same commit that added
// the Collection-side guard against exactly this class of defect.
//
// The checks below close that gap for the second half of the dispatch
// surface. They share satisfiableCapabilities and
// acceptedUnsatisfiableCapabilities with the Collection sweep on
// purpose: one definition of "can a real device do this", one allowlist,
// so the two halves cannot drift into disagreeing about the same
// capability.
package archtest

import (
	"sort"
	"testing"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/inventory"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/catalogdata"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestDispatchableTransportCapabilitiesAreSatisfiable proves every fqcn
// in engine.ActionCapability requires a capability at least one
// registered device type structurally implements.
//
// engine.ActionCapability is the table both halves of the platform key
// off: validate.CapabilityRule rejects a runbook whose target device
// does not have the named capability, and transportActionExecutor's
// binding refuses the same task at run time. So an entry naming a
// capability no device type can satisfy is not a latent gap, it is a
// task that can never run and a runbook that can never validate,
// against every device the platform is able to build.
func TestDispatchableTransportCapabilitiesAreSatisfiable(t *testing.T) {
	satisfiable := satisfiableCapabilities(t)

	if len(engine.ActionCapability) == 0 {
		t.Fatal("engine.ActionCapability is empty, so this test proved nothing")
	}

	for _, fqcn := range unsatisfiableActions(engine.ActionCapability, satisfiable) {
		t.Errorf("fqcn %q requires capability %q, but no registered device type structurally implements it, so validate.CapabilityRule would reject every runbook using it and transportActionExecutor would refuse every dispatch of it -- either wire a device type to it, or add it to acceptedUnsatisfiableCapabilities with a citation to an honest disclosure in source",
			fqcn, engine.ActionCapability[fqcn])
	}
}

// TestBoundTransportCapabilitiesAreSatisfiable asks the same question of
// the registry the composition roots really build, rather than of the
// table that registry reads its values from.
//
// The two cannot disagree today: engine.CheckActionCapabilityBindings
// runs at startup in cmd/pleiades/run.go and refuses a binding whose
// capability is absent from, or disagrees with, engine.ActionCapability.
// This test is what keeps that reasoning honest rather than assumed. It
// walks engine.NewDefaultTransportBindings' real output, so a binding
// added with a capability nobody put in the table fails here even if the
// startup check were ever relaxed.
//
// The four transports are passed as nil. Nothing below dials anything:
// TransportBinding.Capability is data on the binding, decided by
// NewDefaultTransportBindings itself, and no code path here reads
// Transport at all. Standing up a real SSH client, serial port and
// Telnet dialer to read a struct field would make this sweep depend on
// hardware to answer a question about a map, which is the same reasoning
// registry_sweep_test.go's registerViewsForSweep already records for its
// zero-valued ports.
func TestBoundTransportCapabilitiesAreSatisfiable(t *testing.T) {
	satisfiable := satisfiableCapabilities(t)

	bindings := engine.NewDefaultTransportBindings(nil, nil, nil, nil).All()
	if len(bindings) == 0 {
		t.Fatal("engine.NewDefaultTransportBindings registered nothing, so this test proved nothing")
	}

	table := make(map[string]capability.Name, len(bindings))
	for fqcn, binding := range bindings {
		table[fqcn] = binding.Capability
	}

	for _, fqcn := range unsatisfiableActions(table, satisfiable) {
		t.Errorf("transport binding %q requires capability %q, but no registered device type structurally implements it, so its Target function's type assertion fails for every device and the fqcn is refused unconditionally",
			fqcn, table[fqcn])
	}
}

// unsatisfiableActions returns, sorted, every fqcn in table whose
// required capability is neither satisfiable by a real device type nor
// allowlisted in acceptedUnsatisfiableCapabilities.
//
// It exists as a separate function so the rule can be exercised against
// a table this test controls. An assertion that only ever runs against
// the real table cannot tell "nothing is wrong" apart from "the check
// looks at nothing", which is the failure
// TestUnsatisfiableActionsDetectsAnUnreachableFQCN below rules out.
func unsatisfiableActions(table map[string]capability.Name, satisfiable map[capability.Name]bool) []string {
	var bad []string
	for fqcn, name := range table {
		if satisfiable[name] {
			continue
		}
		if _, accepted := acceptedUnsatisfiableCapabilities[name]; accepted {
			continue
		}
		bad = append(bad, fqcn)
	}
	sort.Strings(bad)
	return bad
}

// TestUnsatisfiableActionsDetectsAnUnreachableFQCN is the negative
// control for the two tests above: it proves the rule they share really
// does report an fqcn nothing can dispatch, rather than passing because
// it examines nothing.
//
// Phase 73's own guard was negative-controlled by temporarily un-wiring
// a device type from internal/inventory/builtins.go, which is a real
// control but a manual one that runs once and leaves no trace. This one
// runs on every CI job, because the unreachable capability is
// synthesized here instead of removed from the tree.
func TestUnsatisfiableActionsDetectsAnUnreachableFQCN(t *testing.T) {
	satisfiable := satisfiableCapabilities(t)

	// A capability name nothing registers, so nothing can satisfy it.
	const ghost = capability.Name("ArchtestNobodyImplementsThisCapable")
	if satisfiable[ghost] {
		t.Fatalf("%q is satisfiable, so it cannot serve as this control's unreachable case", ghost)
	}
	if _, accepted := acceptedUnsatisfiableCapabilities[ghost]; accepted {
		t.Fatalf("%q is allowlisted, so it cannot serve as this control's unreachable case", ghost)
	}

	// A capability a real device type does satisfy, so a caught failure
	// is attributable to the ghost rather than to the rule reporting
	// everything it is shown.
	const real = capability.NameSSHTransport
	if !satisfiable[real] {
		t.Fatalf("%q is not satisfiable, so this control's positive case is not positive", real)
	}

	got := unsatisfiableActions(map[string]capability.Name{
		"archtest_ghost_exec": ghost,
		"archtest_real_exec":  real,
	}, satisfiable)

	if len(got) != 1 || got[0] != "archtest_ghost_exec" {
		t.Fatalf("unsatisfiableActions() = %v, want exactly [archtest_ghost_exec]", got)
	}
}

// requiredCapabilities returns every capability name anything in this
// binary can actually ask for: a Collection manifest's
// RequiredCapabilities, or an engine.ActionCapability entry.
//
// Both registries are read live rather than from catalogdata, so a
// method registered through some path that skips the data table still
// counts as a consumer.
func requiredCapabilities(t *testing.T) map[capability.Name]bool {
	t.Helper()

	required := make(map[capability.Name]bool)
	for _, name := range engine.ActionCapability {
		required[name] = true
	}
	for _, cfg := range catalogdata.Collections {
		desc, ok := collection.Lookup(cfg.Name)
		if !ok {
			continue
		}
		for _, name := range desc.Manifest.RequiredCapabilities {
			required[name] = true
		}
	}
	if len(required) == 0 {
		t.Fatal("nothing requires any capability, so this test proved nothing")
	}
	return required
}

// TestRegisteredCapabilitiesAreReachable is the third sweep, and it
// covers the one class the other two structurally cannot see: a
// capability that is registered in the vocabulary, satisfied by no
// device type, AND required by nothing.
//
// The other two sweeps both start from a consumer and ask whether a
// device can satisfy it. A capability with no consumer at all is invisible
// to both, so it can sit in the published vocabulary
// (docs/reference/capabilities.md lists every registered name) looking
// like something an operator could classify a device as, while no method
// and no transport would ever key off it. FileTransferCapable was exactly
// that, and a reader following docs/03-migrating-from-ansible.md would
// have classified a device with it expecting archive.extract to match,
// which requires POSIXFileSystemCapable instead.
//
// Being unreachable is allowed. Being unreachable and undisclosed is not:
// an entry here has to point at a real disclosure in the capability's own
// declaration, the convention AptCapable set and this repository has
// followed since.
func TestRegisteredCapabilitiesAreReachable(t *testing.T) {
	satisfiable := satisfiableCapabilities(t)
	required := requiredCapabilities(t)

	all := capability.All()
	if len(all) == 0 {
		t.Fatal("capability.All() returned nothing, which means pkg/capability was not linked in")
	}

	for _, name := range unreachableCapabilities(all, satisfiable, required) {
		t.Errorf("capability %q is registered and published in the capability vocabulary, but no device type can satisfy it and no Collection manifest or transport fqcn requires it, so it is a name a user can read and classify against that nothing will ever match -- either wire it to something, or add it to acceptedUnreachableCapabilities citing a disclosure in its own declaration", name)
	}
}

// unreachableCapabilities returns, sorted, every registered capability
// name that no device type satisfies, nothing requires, and the
// allowlist does not excuse.
//
// It is a separate function so the rule can be run against a vocabulary
// this test controls, which is what
// TestUnreachableCapabilitiesDetectsAnOrphan below does. A sweep that
// only ever runs against a tree already satisfying it cannot tell
// "nothing is wrong" from "the rule matches nothing".
func unreachableCapabilities(all map[capability.Name]capability.Descriptor, satisfiable, required map[capability.Name]bool) []string {
	var unreachable []string
	for name := range all {
		if satisfiable[name] || required[name] {
			continue
		}
		if _, accepted := acceptedUnreachableCapabilities[name]; accepted {
			continue
		}
		unreachable = append(unreachable, string(name))
	}
	sort.Strings(unreachable)
	return unreachable
}

// TestUnreachableCapabilitiesDetectsAnOrphan is the negative control for
// the sweep above: it proves the rule reports a capability nothing can
// reach, and stays quiet for one a device satisfies and one something
// requires, so a pass is a real result rather than an empty query.
func TestUnreachableCapabilitiesDetectsAnOrphan(t *testing.T) {
	const (
		orphan    = capability.Name("ArchtestOrphanCapable")
		satisfied = capability.Name("ArchtestSatisfiedCapable")
		wanted    = capability.Name("ArchtestRequiredCapable")
	)

	all := map[capability.Name]capability.Descriptor{
		orphan:    {Name: orphan},
		satisfied: {Name: satisfied},
		wanted:    {Name: wanted},
	}

	got := unreachableCapabilities(all,
		map[capability.Name]bool{satisfied: true},
		map[capability.Name]bool{wanted: true},
	)

	if len(got) != 1 || got[0] != string(orphan) {
		t.Fatalf("unreachableCapabilities() = %v, want exactly [%s]", got, orphan)
	}
}

// acceptedUnreachableCapabilities is this sweep's own allowlist, kept
// separate from acceptedUnsatisfiableCapabilities because the two answer
// different questions. That map holds capabilities a real method
// requires but no device satisfies (a live refusal, disclosed). This one
// holds capabilities nothing requires at all AND no device satisfies,
// which refuse nothing and mislead only a reader.
//
// Every entry cites the disclosure in the capability's own declaration,
// not in this file: removing the map and reading the cited comment must
// tell the same story.
//
// It is empty, and that is its correct state today. It held two entries
// until Phase 77. FileTransferCapable's became dead when linux.Server
// gained FileTransferRoot; RFC2217Capable's had been dead since
// console_device began satisfying it, and its own text admitted as much
// ("guards only the no-consumer half"). Neither was ever consulted once
// its capability was satisfiable, because unreachableCapabilities skips
// a satisfiable capability before it looks here, and the staleness guard
// below only noticed an entry that had become satisfiable AND required.
// It now notices either, so an entry can no longer outlive its reason.
var acceptedUnreachableCapabilities = map[capability.Name]string{}

// TestAcceptedUnreachableCapabilitiesAreNotStale is the allowlist's own
// drift guard, the same role TestAcceptedUnsatisfiableCapabilitiesAreNotStale
// plays for its sibling: an entry that quietly became reachable must be
// removed, not left to accumulate into an exemption nobody rechecks.
func TestAcceptedUnreachableCapabilitiesAreNotStale(t *testing.T) {
	satisfiable := satisfiableCapabilities(t)
	required := requiredCapabilities(t)

	for _, name := range staleUnreachableEntries(acceptedUnreachableCapabilities, satisfiable, required) {
		t.Errorf("capability %q is allowlisted as unreachable, but a device type now satisfies it or something now requires it, so the entry is never consulted -- remove it, and update the disclosure it cites", name)
	}
}

// staleUnreachableEntries returns, sorted, every allowlisted capability
// that is no longer unreachable. An entry excuses a capability that is
// neither satisfiable nor required, so it goes stale the moment EITHER
// becomes true: from then on unreachableCapabilities never reads it.
//
// It is a separate function for the same reason unreachableCapabilities
// is, so its rule can be run against data this file controls
// (TestStaleUnreachableEntriesDetectsEitherDirection).
func staleUnreachableEntries(allow map[capability.Name]string, satisfiable, required map[capability.Name]bool) []string {
	var stale []string
	for name := range allow {
		if satisfiable[name] || required[name] {
			stale = append(stale, string(name))
		}
	}
	sort.Strings(stale)
	return stale
}

// TestStaleUnreachableEntriesDetectsEitherDirection is the negative
// control for the staleness guard. The guard it replaced required BOTH
// conditions and so missed both of the entries it was later found to
// be carrying.
func TestStaleUnreachableEntriesDetectsEitherDirection(t *testing.T) {
	const (
		nowSatisfied = capability.Name("ArchtestNowSatisfiedCapable")
		nowRequired  = capability.Name("ArchtestNowRequiredCapable")
		stillOrphan  = capability.Name("ArchtestStillOrphanCapable")
	)
	allow := map[capability.Name]string{nowSatisfied: "x", nowRequired: "x", stillOrphan: "x"}
	got := staleUnreachableEntries(allow,
		map[capability.Name]bool{nowSatisfied: true},
		map[capability.Name]bool{nowRequired: true},
	)
	if len(got) != 2 || got[0] != string(nowRequired) || got[1] != string(nowSatisfied) {
		t.Fatalf("staleUnreachableEntries() = %v, want exactly [%s %s]", got, nowRequired, nowSatisfied)
	}
}
