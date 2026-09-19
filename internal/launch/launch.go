// Package launch is the Launchable abstraction: the saved, reusable
// definition of something this platform can run, and the rules for what a
// caller may change at the moment they run it.
//
// It exists because the launch surface was four scalars. Dispatching meant
// naming a group and a runbook, with no way to save the pairing, no way to
// vary it, and no record of the decisions somebody made about how to run
// it. AWX calls the saved thing a Job Template and Semaphore calls it a
// Task Template; the sentence both build around is that a template defines
// what to run, where to run it, and how to run it.
//
// The Kind is an OPEN registry key, not a closed enum, and that is the
// single most consequential decision in this package. PLAN.md Section 28
// names it explicitly, and the reason is downstream: a closed set forces a
// type switch at every consumer, and the consumers are schedules, workflow
// nodes, notification policies, approvals and the runner's own adapter
// selection. Each one would grow a case per kind, and each new kind would
// mean editing every one of them. With a registry, a kind is one file plus
// a line in builtins.go, exactly the shape Collections, device types and
// sync plugins already use here.
//
// It is pure domain: no ent, no HTTP, no runbook source, no filesystem. A
// Descriptor declares what a definition reference must *look* like; what a
// reference actually resolves to belongs to whoever owns that source. That
// keeps this package testable without a database and keeps the resolution
// rules in one place rather than one place per storage backend.
package launch

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/registry"
)

// ErrUnknownKind is returned when a kind names no registered descriptor.
//
// An unknown kind is refused rather than defaulted, because every default
// available is wrong: running a playbook as a runbook would hand a file to
// an executor that cannot read it, and running a runbook as a playbook
// would hand it to a sandbox that would try to.
var ErrUnknownKind = errors.New("launch: no such kind is registered")

// ErrInvalidTemplate is returned when a template could not be launched by
// anybody, however they configured it.
var ErrInvalidTemplate = errors.New("launch: template is not launchable")

// ErrInvalidField is returned when a supplied value is not what its field
// declares it must be.
var ErrInvalidField = errors.New("launch: value is not valid for this field")

// DefaultKind is what an absent kind resolves to: the native runbook.
//
// The rule lives here, in the package that owns the kind vocabulary, at
// exactly one place. It is what makes the kind an additive field on every
// record that carries one: a job or a dispatch created before the field
// existed carries none, and must still reach the executor it was always
// going to reach. It used to live only in internal/adapters/routing, which
// left the Controller's own fan-out with no statement of the rule at all,
// and a second statement would eventually disagree with the first.
const DefaultKind = "runbook"

// ResolveKind normalises a kind read off a stored record or the wire:
// trimmed, and DefaultKind when absent.
func ResolveKind(kind string) string {
	if trimmed := strings.TrimSpace(kind); trimmed != "" {
		return trimmed
	}
	return DefaultKind
}

// FieldType is the shape a launch field's value takes.
//
// A small closed set rather than reflection over an arbitrary Go type. The
// values here cross a process boundary as JSON, are typed into an HTML form
// by a person, and are validated in three places, so what matters is that
// all three agree on what "an integer between 0 and 4" means.
type FieldType string

// The field types.
const (
	// TypeString is a single line of text.
	TypeString FieldType = "string"

	// TypeInt is a whole number, bounded by the field's own Min and Max.
	TypeInt FieldType = "int"

	// TypeStringList is an ordered list of strings.
	TypeStringList FieldType = "list"

	// TypeMap is a set of key/value pairs, which is what extra variables
	// are. It merges rather than replaces when it is layered, because two
	// callers setting two different variables both mean it.
	TypeMap FieldType = "map"

	// TypeChoice is exactly one of the field's own Choices. Anything else
	// is refused wherever it arrives, a launch included, where a bad value
	// for another field is merely reported as ignored: for a choice like
	// the run mode, ignoring a misspelled "check" would fall back to a
	// real run.
	TypeChoice FieldType = "choice"
)

// FieldSpec declares one field a kind accepts.
//
// Declared as data rather than as struct fields on a Template, and that
// falls directly out of the open Kind. AWX can afford seventeen parallel
// ask_*_on_launch booleans because its kinds are closed and it knows every
// field at compile time. Here a kind arrives in a file this package has
// never seen, bringing fields nothing here declared, so a column per field
// cannot exist. What can exist is a declared set of names, which is what
// this is.
type FieldSpec struct {
	// Name is the wire name and the map key. Lowercase with underscores,
	// matching every other name that crosses this platform's boundaries.
	Name string

	// Type is what a value for it must be.
	Type FieldType

	// Label and Help are what a form renders. They live here rather than
	// in the UI because the field set is per kind: a view cannot carry
	// labels for fields it does not know exist.
	Label string
	Help  string

	// Min and Max bound a TypeInt field. Both zero means unbounded, which
	// is the right default for a field like forks where the useful ceiling
	// depends on the deployment rather than on this code.
	Min int
	Max int

	// Choices is every value a TypeChoice field accepts, in the order a
	// form offers them.
	Choices []string
}

// Bounded reports whether this field constrains its range.
func (f FieldSpec) Bounded() bool { return f.Min != 0 || f.Max != 0 }

// Descriptor is everything the platform needs to know about one kind of
// launchable thing.
type Descriptor struct {
	// Kind is the registry key and the value stored on a template.
	Kind string

	// Label is what a reader sees: "Runbook", "Playbook".
	Label string

	// BadgeClass paints the kind indicator. It is a class from the
	// stylesheet's closed set, never a colour: a runbook and a playbook
	// have different trust and performance stories, and an operator
	// scanning a list of templates should be able to tell which is which
	// without opening one.
	BadgeClass string

	// Summary is one sentence about what this kind is, shown where a
	// reader has to choose between kinds.
	Summary string

	// Adapter names the execution adapter that runs this kind.
	//
	// A name rather than a function, because this package is pure domain
	// and an adapter needs a transport, a device repository and a runbook
	// source. cmd/runner maps the name onto the concrete adapter it
	// composed. This is what retires the comment in cmd/runner explaining
	// that only one adapter is wired "since a real adapter-selection does
	// not exist yet to route dispatch on".
	Adapter string

	// Fields are the fields this kind accepts, in the order a form renders
	// them.
	Fields []FieldSpec

	// ValidateDefinition checks that a definition reference is the right
	// *shape* for this kind: a runbook id that could name a runbook, a
	// playbook path that could name a file inside the project.
	//
	// Shape, not existence. Whether the runbook exists is a question for
	// the runbook source, which this package deliberately does not import;
	// checking it here would mean either a fake source in every test or a
	// domain package that cannot be constructed without storage. What this
	// catches is the class of reference that could never resolve to
	// anything safe, which is exactly the check that has to happen before
	// the string reaches a filesystem or a subject name.
	ValidateDefinition func(reference string) error
}

// kinds is the open registry. It is the Section 25 shared primitive rather
// than a map of this package's own, so a kind registers the same way a
// Collection or a device type does.
var kinds = registry.New[Descriptor]()

// SnapshotForTest captures the process-wide launchable kind registry and returns a
// function that puts it back, for a test that registers into it.
//
// Without this a test's registration outlives the test, so a second
// iteration under `go test -count>1` fails on a duplicate registration
// rather than starting clean. Call it once at the top of such a test:
//
//	t.Cleanup(launch.SnapshotForTest())
//
// It is exported rather than living in an export_test.go because a
// _test.go file cannot be imported across package boundaries, and tests in
// other packages register here too. internal/archtest forbids production
// code from calling it.
func SnapshotForTest() func() {
	return kinds.SnapshotForTest()
}

// Register adds a kind. It is called from a kind's own file, reached only
// by a blank import in builtins.go, which is the deliberate one-line step
// that makes a kind visible to the running binary.
func Register(d Descriptor) error {
	if strings.TrimSpace(d.Kind) == "" {
		return fmt.Errorf("launch: a kind descriptor with no kind cannot be registered")
	}
	if strings.TrimSpace(d.Adapter) == "" {
		// Refused rather than defaulted. A kind with no adapter is one the
		// runner would accept a dispatch for and then have nothing to run
		// it with, which surfaces as a job that is neither refused nor
		// completed.
		return fmt.Errorf("launch: kind %q declares no execution adapter", d.Kind)
	}
	if err := validateFieldSpecs(d); err != nil {
		return err
	}
	return kinds.Register(d.Kind, d)
}

// MustRegister is Register for a compile-time-known built-in, panicking on
// a duplicate or malformed descriptor at process start rather than
// silently picking one of two registrations.
func MustRegister(d Descriptor) {
	if err := Register(d); err != nil {
		panic("launch: " + err.Error())
	}
}

// validateFieldSpecs refuses a descriptor whose fields could not be
// resolved unambiguously.
func validateFieldSpecs(d Descriptor) error {
	seen := make(map[string]bool, len(d.Fields))
	for _, f := range d.Fields {
		name := strings.TrimSpace(f.Name)
		if name == "" {
			return fmt.Errorf("launch: kind %q declares a field with no name", d.Kind)
		}
		if seen[name] {
			return fmt.Errorf("launch: kind %q declares field %q twice, so which one a value binds to would depend on iteration order", d.Kind, name)
		}
		switch f.Type {
		case TypeString, TypeInt, TypeStringList, TypeMap:
		case TypeChoice:
			if len(f.Choices) == 0 {
				return fmt.Errorf("launch: kind %q declares choice field %q with no choices, which no value satisfies", d.Kind, name)
			}
			offered := map[string]bool{}
			for _, c := range f.Choices {
				if strings.TrimSpace(c) == "" || offered[c] {
					return fmt.Errorf("launch: kind %q declares choice field %q with an empty or repeated choice %q", d.Kind, name, c)
				}
				offered[c] = true
			}
		default:
			return fmt.Errorf("launch: kind %q declares field %q with unknown type %q", d.Kind, name, f.Type)
		}
		if f.Type == TypeInt && f.Bounded() && f.Min > f.Max {
			return fmt.Errorf("launch: kind %q declares field %q with min %d above max %d, which no value satisfies", d.Kind, name, f.Min, f.Max)
		}
		seen[name] = true
	}
	return nil
}

// Lookup returns the descriptor registered for kind.
func Lookup(kind string) (Descriptor, bool) { return kinds.Get(kind) }

// Kinds returns every registered kind, in a stable order.
//
// Sorted rather than in registration order, because registration order is a
// map iteration and a form whose options move between page loads is one
// nobody trusts.
func Kinds() []Descriptor {
	all := kinds.All()
	out := make([]Descriptor, 0, len(all))
	for _, d := range all {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}

// Normalize checks one submitted value against the field it names and
// returns it in that field's own type.
//
// Exported for the launch form, which has to refuse a bad value while the
// operator is still standing at the control rather than after the launch.
// The resolver reports such a value as ignored, which is right for an API
// caller who chose their own request body and wrong for a form: the form
// offered the control, so a value it will not apply has to come back as a
// message beside it, not as a run that quietly used something else.
//
// One definition rather than two, which is the whole reason this is here:
// a form that carried its own copy of "an integer between 1 and 1000"
// would be a second answer to what a field accepts, and the two would
// drift the first time a kind changed its bounds.
func (d Descriptor) Normalize(name string, value any) (any, error) {
	spec, ok := d.Field(name)
	if !ok {
		return nil, fmt.Errorf("%w: %s has no field %q", ErrInvalidField, d.Label, name)
	}
	return normalize(spec, value)
}

// Field returns the named field's spec, and whether this kind accepts it at
// all.
func (d Descriptor) Field(name string) (FieldSpec, bool) {
	for _, f := range d.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return FieldSpec{}, false
}
