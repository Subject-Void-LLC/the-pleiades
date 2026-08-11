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
	"context"
	"fmt"
	"net/url"
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

// ChartBucket is one categorical measurement: a label, a count, and the
// badge class that colours both the chart segment and the table row.
//
// The class comes from the same closed set every status badge draws from,
// so a chart cannot introduce a colour the stylesheet has not already
// proven contrast for -- and the label travels with it, so the chart never
// encodes meaning in colour alone (WCAG SC 1.4.1).
type ChartBucket struct {
	Label string `json:"label"`
	Count int    `json:"count"`
	Class string `json:"class"`
}

// ChartData is what a chart endpoint serves and what its table renders.
//
// It is domain JSON and deliberately not an ECharts option document.
// Coupling a server endpoint to a charting library's schema is a seam that
// can never be changed afterwards -- swapping the library would become an
// API break -- and the same values have to drive the table equivalent
// anyway, which no option document could.
type ChartData struct {
	Buckets []ChartBucket `json:"buckets"`
}

// Total is the sum of every bucket, rendered as the table's footer so the
// figures a reader is given add up to something they can check.
func (c ChartData) Total() int {
	total := 0
	for _, b := range c.Buckets {
		total += b.Count
	}
	return total
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

	// Data produces the aggregates. It is called once to render the table
	// server-side and once more by the chart endpoint the script fetches,
	// so it must be cheap and must not mutate anything.
	//
	// There is deliberately no author-supplied URL here. The path is
	// derived from the mount prefix and the resource name, because a
	// resource that wrote its own absolute URL would be hardcoding a
	// decision that belongs to the composition root.
	Data func(context.Context) (ChartData, error)
}

// StreamSpec declares that a resource's records have a live event stream.
//
// It is a declaration rather than a handler because the route table is
// fixed: /{resource}/{id}/logs exists once, for every resource that will
// ever declare one of these, and 404s for every resource that does not.
type StreamSpec struct {
	// Title is the streaming page's heading.
	Title string

	// PathPattern is the server-sent-events endpoint, containing the
	// literal token {id} where a record's identifier belongs -- for
	// example "/api/v1/jobs/{id}/logs".
	//
	// A pattern rather than a func(id string) string, deliberately. A
	// function would put URL construction in every resource author's
	// hands, and each of them would have to remember to escape the
	// identifier; substituting into a validated pattern means the escape
	// happens once, here, in code that is tested once.
	PathPattern string
}

// idToken is what StreamSpec.PathPattern must contain and what a record's
// escaped identifier replaces.
const idToken = "{id}"

// StreamPath builds one record's stream URL, escaping the identifier.
func (s StreamSpec) StreamPath(id string) string {
	return strings.ReplaceAll(s.PathPattern, idToken, url.PathEscape(id))
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

	// Stream optionally declares that each of this resource's records has
	// a live event stream, which is what makes /{resource}/{id}/logs
	// resolve for this resource and 404 for every other one.
	Stream *StreamSpec

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

// ListsRecords reports whether this view renders a table of records.
//
// A view may legitimately have none. The dashboard is a chart and nothing
// else, which is a real shape rather than an unfinished one, so the list
// page renders whichever of its sections exist instead of assuming a table
// is always the point.
func (d Descriptor) ListsRecords() bool {
	return d.Handlers != nil && d.Handlers.List != nil
}

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
	if err := validateChart(d.Name, d.Chart); err != nil {
		return err
	}
	if err := validateStream(d.Name, d.Stream); err != nil {
		return err
	}

	switch {
	case d.Implemented() && d.Handlers == nil:
		return fmt.Errorf("view %q claims status %q but carries no handlers", d.Name, StatusImplemented)
	case !d.Implemented() && d.Handlers != nil:
		return fmt.Errorf("view %q is declared but carries handlers", d.Name)

	// A chart or a stream on a declared view is the same contradiction as
	// a handler: both reach live data, and a view that says it is not
	// implemented while serving live data is exactly the ambiguity the
	// declared status exists to remove.
	case !d.Implemented() && d.Chart != nil:
		return fmt.Errorf("view %q is declared but carries a chart", d.Name)
	case !d.Implemented() && d.Stream != nil:
		return fmt.Errorf("view %q is declared but carries a stream", d.Name)

	// A view that lists records renders a detail link on every row, so
	// without a Get endpoint each of those links is a button this UI drew
	// for a route nobody mounted -- the exact failure the endpoint check
	// above exists to prevent, one level down.
	case d.ListsRecords() && d.Ops.Get == nil:
		return fmt.Errorf("view %q lists records but declares no Get endpoint", d.Name)

	// A row's detail link is built from the IDField's value, so a view
	// that renders rows without naming one would link every record to the
	// same empty path.
	case d.ListsRecords() && d.IDField == "":
		return fmt.Errorf("view %q lists records but names no id field", d.Name)

	// A view that neither lists nor reads a record has no way to be
	// reached at all. A summary view is legitimate -- the dashboard is
	// one, a chart with no table beneath it -- but it still has to name
	// the endpoint whose scope gates it.
	case d.Implemented() && d.Ops.List == nil && d.Ops.Get == nil:
		return fmt.Errorf("view %q claims status %q but declares no List or Get endpoint", d.Name, StatusImplemented)
	}

	if d.Handlers.Writable() && d.Ops.Create == nil {
		return fmt.Errorf("view %q has write handlers but declares no Create endpoint", d.Name)
	}

	return views.Register(d.Name, d)
}

// validateChart refuses a chart that could not be read by everyone.
func validateChart(name string, chart *ChartSpec) error {
	if chart == nil {
		return nil
	}
	if strings.TrimSpace(chart.Title) == "" {
		return fmt.Errorf("view %q has a chart with no title", name)
	}
	if strings.TrimSpace(chart.Caption) == "" {
		// A chart with no long description is a chart that is simply
		// unavailable to a screen reader user.
		return fmt.Errorf("view %q has a chart with no caption", name)
	}
	if chart.Data == nil {
		// A declared chart with no data function renders an empty canvas
		// and an empty table, which reads as "nothing is happening"
		// rather than as "this is not wired up".
		return fmt.Errorf("view %q has a chart with no data function", name)
	}
	return nil
}

// validateStream refuses a stream declaration that could not produce a
// usable URL.
func validateStream(name string, stream *StreamSpec) error {
	if stream == nil {
		return nil
	}
	if strings.TrimSpace(stream.Title) == "" {
		return fmt.Errorf("view %q has a stream with no title", name)
	}
	if !strings.HasPrefix(stream.PathPattern, "/") {
		// A relative pattern would resolve against whatever page happened
		// to be open, and a scheme-bearing one would let a resource point
		// the browser's EventSource at another origin.
		return fmt.Errorf("view %q stream path %q is not an absolute path", name, stream.PathPattern)
	}
	if strings.HasPrefix(stream.PathPattern, "//") {
		return fmt.Errorf("view %q stream path %q is protocol-relative", name, stream.PathPattern)
	}
	if !strings.Contains(stream.PathPattern, idToken) {
		// Without the token every record would stream the same URL, which
		// is a subtle enough bug to be worth refusing outright.
		return fmt.Errorf("view %q stream path %q contains no %s token", name, stream.PathPattern, idToken)
	}
	return nil
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
