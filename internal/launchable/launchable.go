// Package launchable is what a schedule, and later a workflow node or a
// notification policy, points at: one stable reference to a thing this
// platform can run, whatever sort of thing it is.
//
// # The axis this package owns
//
// AWX calls this UnifiedJobTemplate, and its subclasses are the job
// template, the project (whose run is a sync), the inventory source and the
// workflow. That is what a schedule attaches to there, which is why one
// mechanism schedules a project sync and a job template alike.
//
// internal/launch owns a different axis and the two used to be conflated
// (.SPECIFICATION/AWX_PARITY_ROADMAP.md section 1.1). A launch.Kind answers
// "which engine runs this definition", native runbook or ansible-playbook;
// AWX has no such axis because it always runs ansible-playbook. This
// package answers "what sort of object is this", which is the question a
// consumer that does not care how something runs still has to ask.
//
// So the vocabulary here is deliberately different: a TYPE is a launchable
// object type (job_template, project) and a KIND stays the engine. Both are
// open registries, for the reason PLAN.md Section 28 gives, and the keys
// here are AWX's own strings so an import resolves without translation.
//
// # What a consumer gets
//
// A Target is a reference plus the few facts every consumer needs (name and
// organization), read from one table, so nothing has to know which sort of
// object it holds. A Descriptor says what the type can do, which is what
// lets one form offer both and one validator refuse the wrong combination.
// A Router turns a Target plus an actor into a run, by a map lookup rather
// than a type switch: internal/archtest fails the build if any consumer
// branches on a type key.
//
// It is pure domain, except for ent_store.go, which reads the launchables
// table. Nothing here launches anything itself: a type's launcher is
// composed in cmd/controller, because launching a job template means the
// Dispatcher and launching a project sync means the project runner, and a
// package every consumer imports must not drag both in.
package launchable

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/registry"
)

// Common failures a caller distinguishes.
var (
	// ErrUnknownType names a type no descriptor is registered for.
	//
	// Refused rather than defaulted, for the reason launch.ErrUnknownKind
	// gives about engines and more sharply here: a default would launch
	// something other than what the caller named.
	ErrUnknownType = errors.New("launchable: no such launchable type is registered")

	// ErrNotFound is a reference to a launchable that does not exist, or no
	// longer does.
	ErrNotFound = errors.New("launchable: not found")

	// ErrInUse is a launchable something still points at: a schedule, and
	// later a workflow node. It is what makes deleting a scheduled template
	// or project a 409 that names the reason rather than an opaque
	// constraint failure.
	ErrInUse = errors.New("launchable: still referenced")
)

// The launchable types this build has, as keys.
//
// They are AWX's own subclass names, and they live here rather than in the
// packages that register them because a target's own store has to write its
// type key when it writes the target: internal/launch names TypeJobTemplate
// when it creates a template's launchable row. Importing the registering
// package to get the string would ALSO register the type as a side effect,
// which would make the composition root's blank import meaningless and the
// archtest that checks it vacuous. A key is vocabulary; a Descriptor is
// behavior, and only the second one registers.
const (
	// TypeJobTemplate is a saved job template (internal/launch).
	TypeJobTemplate = "job_template"

	// TypeProject is a project, whose run is a sync of its source
	// (internal/project).
	TypeProject = "project"
)

// UnifiedJobType is what one run of a launchable is called.
//
// AWX's own words, because these strings reach an API response and an
// operator's eye: a job template's run is a "job" and a project's is a
// "project_update". It is declared by the type rather than derived, since
// the two vocabularies are not mechanically related.
type UnifiedJobType string

// The run names this build produces.
const (
	// UnifiedJobJob is one run of a job template.
	UnifiedJobJob UnifiedJobType = "job"

	// UnifiedJobProjectUpdate is one sync of a project.
	UnifiedJobProjectUpdate UnifiedJobType = "project_update"
)

// Target is one launchable object, by reference.
//
// ID is the launchables row's own id, which is stable and unique across
// every type: that is the whole point of the base row, and it is what a
// schedule stores. Name and OrganizationID travel with it because every
// consumer needs them (a picker renders the name, an authorization check
// compares the tenant) and a consumer that had to resolve them per type
// would be the type switch this package exists to prevent.
type Target struct {
	ID   int
	Type string

	Name             string
	OrganizationID   int
	OrganizationName string
}

// Descriptor declares what one launchable type is and what may be done with
// it.
type Descriptor struct {
	// Type is the registry key: AWX's own subclass name, "job_template" or
	// "project".
	Type string

	// Label is what a person reads: "Job template", "Project sync". It is
	// also how a picker groups its options, so it names the type rather
	// than the action.
	Label string

	// UnifiedJobType is what a run of this type is called.
	UnifiedJobType UnifiedJobType

	// LaunchScope is what a caller must hold to make this run.
	//
	// Declared by the type rather than checked at each consumer, because
	// the consumers are a schedule today and a workflow node and a
	// notification policy later, and a rule restated per consumer is a rule
	// that will eventually differ per consumer. It is what closes the hole
	// where schedule:write alone could run anything (FAILURE_PATTERNS.md
	// #268).
	LaunchScope auth.Scope

	// AcceptsSavedConfig says whether a launch of this type can carry a
	// saved launch configuration: a bundle of overrides and survey answers,
	// which only a template has. A type that takes none refuses one at the
	// write rather than ignoring it, so nobody saves a schedule carrying
	// values that will never be applied.
	AcceptsSavedConfig bool
}

// types holds every registered launchable type.
var types = registry.New[Descriptor]()

// SnapshotForTest freezes the registry and returns a restore func, so a test
// can register a type of its own without leaking it into every later test.
func SnapshotForTest() func() { return types.SnapshotForTest() }

// Register adds a launchable type.
//
// Every field is required, and each refusal is a thing that would otherwise
// fail far from here: an unlabelled type renders as a blank option, a type
// with no unified job name produces a run nothing can classify, and one
// with no launch scope would be launchable by anybody who could write a
// schedule.
func Register(d Descriptor) error {
	if strings.TrimSpace(d.Type) == "" {
		return fmt.Errorf("launchable: a type needs a key")
	}
	if strings.TrimSpace(d.Label) == "" {
		return fmt.Errorf("launchable: type %q needs a label somebody can read", d.Type)
	}
	if strings.TrimSpace(string(d.UnifiedJobType)) == "" {
		return fmt.Errorf("launchable: type %q needs a name for what one of its runs is", d.Type)
	}
	if strings.TrimSpace(string(d.LaunchScope)) == "" {
		return fmt.Errorf("launchable: type %q needs the scope required to launch it", d.Type)
	}
	return types.Register(d.Type, d)
}

// MustRegister is Register for a built-in, panicking on a programming error
// at init rather than returning an error nobody is positioned to handle.
func MustRegister(d Descriptor) {
	if err := Register(d); err != nil {
		panic(err)
	}
}

// Lookup returns the descriptor registered for a type.
func Lookup(targetType string) (Descriptor, bool) { return types.Get(targetType) }

// Types returns every registered type, in a stable order.
//
// Sorted by key rather than by registration order, so a picker's groups and
// a reference page's rows do not reorder themselves when an import moves.
func Types() []Descriptor {
	all := types.All()
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]Descriptor, 0, len(keys))
	for _, k := range keys {
		out = append(out, all[k])
	}
	return out
}

// Describe returns the descriptor for a target's type, or ErrUnknownType.
//
// The one place a type key becomes a descriptor for a caller holding a
// Target, so a consumer never writes the lookup-and-refuse pair itself.
func Describe(t Target) (Descriptor, error) {
	d, ok := Lookup(t.Type)
	if !ok {
		return Descriptor{}, fmt.Errorf("%w: %q", ErrUnknownType, t.Type)
	}
	return d, nil
}
