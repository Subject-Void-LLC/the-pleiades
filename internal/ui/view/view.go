// Package view is the web UI's resource registry: the one table every
// rendered view is looked up in, and the fourth consumer of the Section 25
// shared registry primitive after capabilities, Collection methods, device
// types, and sync plugins.
//
// It is deliberately shaped like internal/inventory/syncplugin, the most
// refined of those: a Descriptor carrying identity, a Status that defaults
// to "declared", cross-field validation in Register, and a sorted Names()
// on top of the registry's unordered snapshot. A resource package
// registers itself from its own init(), and is reachable only because
// internal/ui/resources/builtins.go blank-imports it -- the same
// arrangement, and the same hazard, recorded as FAILURE_PATTERNS.md #52
// and LESSONS_LEARNED.md #56: an init() that nothing imports never runs,
// and a package that compiles and passes its own tests while being
// invisible to the running binary is the worst failure shape available.
//
// This package is a leaf. It must never import internal/ui/render or
// internal/ui/resources, for the reason internal/inventory/record exists
// where it does: resource packages import this one to register, and the
// template package imports this one for its model types, so any edge back
// out of here is an import cycle. The presentation model types (Row,
// Values, FieldErrors) therefore live here rather than beside the
// templates that consume them.
package view

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/registry"
)

// Status says whether a registered view actually reaches a real backing
// port yet. It mirrors pkg/collection.Status and syncplugin.Status
// exactly, and for the same reason: a declared skeleton must be able to
// say out loud that it is a skeleton.
type Status string

const (
	// StatusDeclared means the view is registered and its shape is real,
	// but it has no handlers and renders an explicit not-implemented
	// panel. Every view the Forge generates starts here.
	StatusDeclared Status = "declared"

	// StatusImplemented means the view is backed by a real port and
	// genuinely renders real data.
	StatusImplemented Status = "implemented"
)

// namePattern is what a resource name must match. It becomes a URL path
// segment, a chi route-parameter value, and a metrics label, so anything
// outside this set is either a path-traversal question or an unbounded
// label-cardinality question, and neither is worth having.
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// Ops names the API endpoints backing each operation this view offers.
//
// Every entry points at a value in internal/apispec, which already
// carries the operation's method, URL pattern, required scope, and link
// relation. That is what lets a rendered button and a JSON _links entry be
// computed from one value by one authorization chain: the UI never
// declares a scope of its own, so it cannot advertise an action the router
// does not enforce. A nil entry means the view does not offer that
// operation, and no button for it is ever rendered.
type Ops struct {
	List   *apispec.Endpoint
	Get    *apispec.Endpoint
	Create *apispec.Endpoint
	Update *apispec.Endpoint
	Delete *apispec.Endpoint
}

// all returns every non-nil endpoint, in a stable order.
func (o Ops) all() []*apispec.Endpoint {
	out := make([]*apispec.Endpoint, 0, 5)
	for _, e := range []*apispec.Endpoint{o.List, o.Get, o.Create, o.Update, o.Delete} {
		if e != nil {
			out = append(out, e)
		}
	}
	return out
}

// Candidates converts this view's operations into the affordance
// candidates an auth.HATEOASGenerator evaluates.
//
// It carries no URL and no method, matching auth.Affordance's own
// contract: a generator decides which candidates an identity may
// exercise, and the caller owns the href and the verb, read back from
// these same Endpoint values. That is why a hostile or buggy generator
// cannot put an action into a page that the router would refuse.
func (o Ops) Candidates() []auth.Affordance {
	endpoints := o.all()
	out := make([]auth.Affordance, 0, len(endpoints))
	for _, e := range endpoints {
		out = append(out, auth.Affordance{Rel: e.Rel, Scope: e.Scope})
	}
	return out
}

// ChartSpec describes the one chart a view may render.
type ChartSpec struct {
	// Title is the chart's accessible name and its visible heading.
	Title string

	// Caption is the long description every non-trivial chart owes a
	// reader who cannot see it (WCAG SC 1.1.1). A chart's real text
	// alternative is the data table rendered beside it; this sentence
	// says what the chart is claiming.
	Caption string

	// DataPath is the UI-subtree path serving this chart's aggregates as
	// domain JSON. It is deliberately not an ECharts option document:
	// coupling a server endpoint to a charting library's schema is a
	// seam that can never be changed afterwards, and the same JSON has
	// to drive the table fallback anyway.
	DataPath string
}

// Descriptor is everything the UI needs to render one resource.
type Descriptor struct {
	// Name is the registration key and the URL path segment.
	Name string

	// Title is the page <title> and its single <h1>.
	Title string

	// NavLabel is this view's sidebar text.
	NavLabel string

	// NavOrder sorts the sidebar. Ties break on Name, since the registry
	// snapshot is an unordered map and a nav that reshuffles between
	// page loads is a nav nobody can build muscle memory for.
	NavOrder int

	// Summary is one line rendered under the heading.
	Summary string

	// Status is whether this view is backed by anything real. An empty
	// Status reads as StatusDeclared, so a descriptor that forgets the
	// field is treated as a skeleton rather than silently claiming to
	// work.
	Status Status

	// Ops names the API endpoints backing each operation.
	Ops Ops

	// Fields is the one declaration driving columns, forms, validation,
	// the detail list, and the mobile card layout.
	Fields []Field

	// IDField names the Field whose value identifies a record.
	IDField string

	// Chart optionally adds one chart to this view.
	Chart *ChartSpec

	// Applies optionally withdraws an affordance for one particular
	// record -- an archived device offers no delete to anyone, however
	// broadly scoped. It is api.LinkFilter's contract, re-expressed per
	// row. Nil means every permitted affordance applies to every row.
	Applies func(Row, auth.LinkRel) bool

	// Handlers is the erased operation set, built by MustBind. It is nil
	// for a declared view, and Register enforces that in both
	// directions.
	Handlers *Handlers
}

// Implemented reports whether this view reaches a real port. Callers use
// it rather than comparing Status directly, so the empty-means-declared
// default lives in one place.
func (d Descriptor) Implemented() bool { return d.Status == StatusImplemented }

// ListFields returns the fields that appear as list columns, in
// declaration order.
func (d Descriptor) ListFields() []Field { return filterFields(d.Fields, Field.listed) }

// FormFields returns the fields that appear as form controls, in
// declaration order.
func (d Descriptor) FormFields() []Field { return filterFields(d.Fields, Field.Writable) }

// PrimaryField returns the field a narrow viewport uses as each card's
// heading, falling back to the identity field when none is marked.
func (d Descriptor) PrimaryField() Field {
	for _, f := range d.Fields {
		if f.MobilePrimary {
			return f
		}
	}
	for _, f := range d.Fields {
		if f.Name == d.IDField {
			return f
		}
	}
	return Field{}
}

func (f Field) listed() bool { return f.InList }

func filterFields(fields []Field, keep func(Field) bool) []Field {
	out := make([]Field, 0, len(fields))
	for _, f := range fields {
		if keep(f) {
			out = append(out, f)
		}
	}
	return out
}

// views is the process-wide view table, built on the same Section 25
// registry primitive as every other extension point rather than a fifth
// hand-rolled map.
var views = registry.New[Descriptor]()

// MustRegister adds d to the view registry, panicking on an invalid or
// duplicate descriptor. Resource packages call it from their own init(),
// so a malformed view fails at process start rather than as a broken page
// somebody finds later.
func MustRegister(d Descriptor) {
	if err := Register(d); err != nil {
		panic("view: " + err.Error())
	}
}

// Register adds d to the view registry, returning an error rather than
// panicking, for a genuinely runtime registration where a duplicate is a
// data problem the caller must handle.
//
// Every check here is a real failure mode rather than ceremony. The two
// worth naming: a status/handler mismatch is refused in both directions,
// which turns what would be a nil-pointer dereference three clicks into a
// session into a refusal at startup (the rule pkg/collection.Register
// already applies to a Collection method claiming an implementation it
// does not carry); and every declared operation must name an endpoint
// that actually exists in apispec.Endpoints, which is what stops this UI
// rendering a button for a route nobody mounted.
func Register(d Descriptor) error {
	if !namePattern.MatchString(d.Name) {
		return fmt.Errorf("name %q must match %s", d.Name, namePattern)
	}
	if strings.TrimSpace(d.Title) == "" {
		return fmt.Errorf("view %q has no title", d.Name)
	}
	if strings.TrimSpace(d.NavLabel) == "" {
		// A nav entry with no text is a link with no accessible name.
		return fmt.Errorf("view %q has no nav label", d.Name)
	}
	if err := validateFields(d.Fields, d.IDField); err != nil {
		return fmt.Errorf("view %q %s", d.Name, err)
	}
	if err := validateOps(d.Name, d.Ops); err != nil {
		return err
	}
	if d.Chart != nil {
		if strings.TrimSpace(d.Chart.Title) == "" {
			return fmt.Errorf("view %q has a chart with no title", d.Name)
		}
		if strings.TrimSpace(d.Chart.Caption) == "" {
			// A chart with no long description is a chart that is
			// simply unavailable to a screen reader user.
			return fmt.Errorf("view %q has a chart with no caption", d.Name)
		}
		if !strings.HasPrefix(d.Chart.DataPath, "/") {
			return fmt.Errorf("view %q chart data path %q is not absolute", d.Name, d.Chart.DataPath)
		}
	}

	switch {
	case d.Implemented() && d.Handlers == nil:
		return fmt.Errorf("view %q claims status %q but carries no handlers", d.Name, StatusImplemented)
	case d.Implemented() && d.Ops.Get == nil:
		return fmt.Errorf("view %q claims status %q but declares no Get endpoint", d.Name, StatusImplemented)
	case !d.Implemented() && d.Handlers != nil:
		return fmt.Errorf("view %q is declared but carries handlers", d.Name)
	}

	if d.Handlers.Writable() && d.Ops.Create == nil {
		return fmt.Errorf("view %q has write handlers but declares no Create endpoint", d.Name)
	}

	return views.Register(d.Name, d)
}

// validateOps checks that every endpoint a view names is one the API
// really declares, and that the view's relations are unambiguous.
func validateOps(name string, ops Ops) error {
	known := make(map[string]apispec.Endpoint, len(apispec.Endpoints))
	for _, e := range apispec.Endpoints {
		known[e.Name] = e
	}

	seenRel := make(map[auth.LinkRel]string, 5)
	for _, e := range ops.all() {
		declared, ok := known[e.Name]
		if !ok {
			return fmt.Errorf("view %q names endpoint %q, which is not in apispec.Endpoints", name, e.Name)
		}
		// Compare the fields the UI actually reads. A view holding a
		// stale copy of an endpoint would render the old scope's
		// buttons against the new scope's route.
		if declared.Method != e.Method || declared.Pattern != e.Pattern ||
			declared.Scope != e.Scope || declared.Rel != e.Rel {
			return fmt.Errorf("view %q holds a stale copy of endpoint %q", name, e.Name)
		}
		if e.Rel == "" {
			return fmt.Errorf("view %q names endpoint %q, which declares no link relation", name, e.Name)
		}
		if prev, dup := seenRel[e.Rel]; dup {
			// Two operations sharing a relation makes a permitted
			// result ambiguous: a template asking Can(rel) could not
			// tell which of the two it was told about.
			return fmt.Errorf("view %q uses relation %q for both %s and %s", name, e.Rel, prev, e.Name)
		}
		seenRel[e.Rel] = e.Name
	}
	return nil
}

// Lookup returns the descriptor registered under name.
func Lookup(name string) (Descriptor, bool) { return views.Get(name) }

// All returns a snapshot of every registered descriptor, keyed by name.
func All() map[string]Descriptor { return views.All() }

// Names returns every registered view name, sorted, for tests and for any
// caller needing a stable ordering.
func Names() []string {
	all := views.All()
	names := make([]string, 0, len(all))
	for name := range all {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Nav returns every registered descriptor in sidebar order: by NavOrder,
// then by Name. The caller filters the result by what the requesting
// identity may actually reach, so this function deliberately knows
// nothing about authorization -- ordering and permission are two
// questions, and folding them together is how a nav ends up with a
// permission table of its own.
func Nav() []Descriptor {
	all := views.All()
	out := make([]Descriptor, 0, len(all))
	for _, d := range all {
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].NavOrder != out[j].NavOrder {
			return out[i].NavOrder < out[j].NavOrder
		}
		return out[i].Name < out[j].Name
	})
	return out
}
