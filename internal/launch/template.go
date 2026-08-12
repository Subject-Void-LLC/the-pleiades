package launch

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Launchable is anything this platform can run on request.
//
// Three methods, and each one exists because a consumer needs it without
// knowing what it is holding. A scheduler needs Kind to route a dispatch; a
// launch endpoint needs Resolve to turn a template plus a caller's wishes
// into one concrete run; a plan-time capability check needs
// RequiredCapabilities before anything is dispatched anywhere.
//
// What the interface deliberately does not offer is a way to ask "are you a
// runbook". PLAN.md Section 28's own requirement, and Phase 21's
// adversarial gate, is that no consumer type-switches on kind: the moment
// one does, every future kind means editing that consumer, which is the
// closed-enum cost the open registry exists to avoid.
type Launchable interface {
	// Kind is the registry key of what this is.
	Kind() string

	// Resolve folds a caller's configuration over this template's own
	// defaults and returns the concrete run, plus every value the caller
	// supplied that this template does not permit them to set.
	//
	// It does not fail on a value the template locked. Silently applying
	// an unopened override is a privilege escalation; silently dropping
	// one is a lie about what ran. Reporting it is the only remaining
	// option, and it is why the second return value is not an error.
	Resolve(ctx context.Context, cfg Config) (Resolved, []IgnoredField, error)

	// RequiredCapabilities is what a device must be able to do for this to
	// run against it.
	RequiredCapabilities() []string
}

// Template is the saved definition: what to run, where to run it, and how
// to run it.
//
// The three clauses are AWX's own sentence about a Job Template, and they
// map onto Definition, InventoryID and Defaults respectively. Nothing here
// is a "configuration": that word named the launch-time override bundle in
// this project's own spec, and the view that used it for the saved
// definition had taken its name from the overrides.
type Template struct {
	ID int

	// Name is unique within an organization. Two tenants both having a
	// "patch the edge routers" template is the ordinary case.
	Name        string
	Description string

	// KindName is the registry key: what sort of thing this runs.
	KindName string

	// Definition is the reference this kind resolves: a runbook id, a
	// playbook path. Its shape is checked by the kind's own
	// ValidateDefinition, never by this type, which does not know what a
	// kind considers a valid reference.
	Definition string

	// InventoryID is where it runs. Required, and it is what gives a job
	// its organization: an inventory carries a required organization edge,
	// so a job launched from a template inherits a tenant rather than
	// being written with none.
	InventoryID int

	// OrganizationName and InventoryName accompany the two ids, read off
	// edges the store already loaded, so a list renders "Network" and
	// "Edge routers" rather than two integers a reader has to resolve
	// themselves (FAILURE_PATTERNS.md #107). Empty when the edge was not
	// loaded, never a fallback to the id.
	OrganizationName string
	InventoryName    string

	// OrganizationID is that tenant, resolved when the template is saved.
	//
	// Stored rather than joined at launch, because it is what a job is
	// tagged with and a job's tenancy must not change retroactively when
	// an inventory is edited. It is derived, never submitted: a caller who
	// could set it directly could tag their jobs with somebody else's
	// tenant.
	OrganizationID int

	// Defaults are how to run it: the values this template was saved with,
	// keyed by field name.
	Defaults Fields

	// Prompts names the fields a launch may override. Everything else is
	// locked to what Defaults says.
	//
	// A list of names rather than AWX's parallel ask_*_on_launch booleans,
	// which is forced by the open Kind: a boolean per field cannot be
	// declared for fields this package has never seen. See FieldSpec.
	Prompts []string

	// Survey asks a launching operator for values, which merge into extra
	// variables.
	Survey Survey

	// AllowSimultaneous permits more than one job from this template to
	// run at once. Semaphore calls it "allow parallel tasks" and defaults
	// it off, which is the right default here too: two runs of the same
	// change against the same fleet is more often a mistake than an
	// intention.
	AllowSimultaneous bool

	// RequiredCaps is what a device must be able to do for this to run,
	// recorded when the template is saved.
	//
	// Recorded rather than computed on demand, because computing it means
	// compiling the definition, which needs the runbook source this
	// package deliberately does not import. The staleness that buys is
	// real and bounded: a runbook edited after a template was saved can
	// leave this behind, which is why it is a plan-time hint and the
	// executor still acquires capabilities for real at run time.
	RequiredCaps []string
}

// Kind implements Launchable.
func (t Template) Kind() string { return t.KindName }

// RequiredCapabilities implements Launchable.
func (t Template) RequiredCapabilities() []string {
	return append([]string(nil), t.RequiredCaps...)
}

// Descriptor returns the registered kind this template runs as.
func (t Template) Descriptor() (Descriptor, error) {
	d, ok := Lookup(t.KindName)
	if !ok {
		return Descriptor{}, fmt.Errorf("%w: %q", ErrUnknownKind, t.KindName)
	}
	return d, nil
}

// Promptable reports whether a launch may set this field.
func (t Template) Promptable(name string) bool {
	for _, p := range t.Prompts {
		if p == name {
			return true
		}
	}
	return false
}

// Validate refuses a template that could not be launched by anybody.
//
// Checked at the write rather than at the launch, for the reason every
// other write path here gives: a record that cannot be used is one somebody
// will find out about at the worst possible moment, which for a template is
// the moment they are trying to run something.
func (t Template) Validate() error {
	if strings.TrimSpace(t.Name) == "" {
		return fmt.Errorf("%w: a template needs a name", ErrInvalidTemplate)
	}

	d, err := t.Descriptor()
	if err != nil {
		return err
	}

	if strings.TrimSpace(t.Definition) == "" {
		return fmt.Errorf("%w: a %s template needs something to run", ErrInvalidTemplate, d.Label)
	}
	if d.ValidateDefinition != nil {
		if err := d.ValidateDefinition(t.Definition); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidTemplate, err)
		}
	}
	if t.InventoryID <= 0 {
		// Required, and this is the line that closes a recorded gap: a job
		// launched with no inventory has no organization to be tagged
		// with, which is why Job.organization_id had no writer.
		return fmt.Errorf("%w: a template needs an inventory to run against", ErrInvalidTemplate)
	}

	// Every default has to be a value this kind accepts, or the template
	// is saved carrying a field that will be dropped at launch with
	// nothing to say it was.
	for _, name := range t.Defaults.Names() {
		spec, ok := d.Field(name)
		if !ok {
			return fmt.Errorf("%w: a %s template has no field %q", ErrInvalidTemplate, d.Label, name)
		}
		if _, err := normalize(spec, t.Defaults[name]); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidTemplate, err)
		}
	}

	// A promptable field the kind does not accept is a control that would
	// render on the launch form and be ignored by the resolver, which is
	// the shape of affordance this project has shipped before and
	// recorded.
	for _, name := range t.Prompts {
		if _, ok := d.Field(name); !ok {
			return fmt.Errorf("%w: a %s template cannot prompt for %q, which it has no field for",
				ErrInvalidTemplate, d.Label, name)
		}
	}

	return t.Survey.Validate()
}

// Resolved is one concrete run: everything the dispatcher needs and nothing
// a caller still has to decide.
type Resolved struct {
	// Kind and Adapter are what runs it, and what runs it with. Adapter is
	// carried rather than looked up again downstream so the runner routes
	// on a value that travelled with the dispatch.
	Kind    string
	Adapter string

	Definition string

	// InventoryID and OrganizationID are where it runs and whose it is.
	InventoryID    int
	OrganizationID int

	// Fields are the final values, every one of them either the
	// template's own or an override the template opened.
	Fields Fields

	// ExtraVars is the merged variable set: template defaults, then the
	// saved configuration, then survey answers, then this launch's own.
	ExtraVars map[string]any

	AllowSimultaneous bool
}

// IgnoredField is one value a caller supplied that was not applied.
//
// It carries the reason as well as the name, because the two reasons mean
// different things to whoever reads the response. "This template does not
// let you set that" is a decision somebody made, and the fix is to edit the
// template. "This kind has no such field" is a mistake in the request, and
// the fix is to stop sending it.
type IgnoredField struct {
	// Name is the field the caller supplied.
	Name string

	// Layer is where they supplied it: the saved configuration, or this
	// launch. A saved configuration that has drifted out of what its
	// template permits is worth telling somebody about, and it is not the
	// launching operator's mistake.
	Layer string

	// Reason is why it was not applied, in words a caller can act on.
	Reason string
}

// The layers a value can arrive from, named once so a report and a test
// cannot spell them differently.
const (
	LayerTemplate = "template"
	LayerSaved    = "saved configuration"
	LayerSurvey   = "survey"
	LayerLaunch   = "launch"
)

// Reasons a supplied value was not applied.
const (
	// ReasonLocked is the template's own decision.
	ReasonLocked = "this template does not allow that field to be set at launch"

	// ReasonUnknownField is the caller's mistake.
	ReasonUnknownField = "this kind of template has no such field"
)

// sortIgnored puts a report in a stable order, so two identical launches
// produce two identical responses.
func sortIgnored(ignored []IgnoredField) {
	sort.Slice(ignored, func(i, j int) bool {
		if ignored[i].Layer != ignored[j].Layer {
			return ignored[i].Layer < ignored[j].Layer
		}
		return ignored[i].Name < ignored[j].Name
	})
}
