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
	"time"

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

// Candidates is every affordance this descriptor could offer: its CRUD
// operations and its record actions together.
//
// Actions have to be here rather than only in Ops, and the reason is worth
// recording because the omission was invisible. An action carries its own
// endpoint with its own relation and scope; the permitted set a template
// consults is keyed by relation; so an action whose relation never entered
// the candidate set could never be permitted, and its control never
// rendered. The button was not refused, it simply did not exist, on every
// page, for every caller, with no error anywhere.
func (d Descriptor) Candidates() []auth.Affordance {
	out := d.Ops.Candidates()
	for _, a := range d.Actions {
		if a.Endpoint == nil {
			continue
		}
		out = append(out, auth.Affordance{Rel: a.Endpoint.Rel, Scope: a.Endpoint.Scope})
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

// Section is a table of related records rendered beneath a page's own
// content: under a record's fields on a detail page, and under the chart on
// a collection page.
//
// It exists because the interesting part of some records is not their own
// columns but what they contain. A job's own row says three dispatched and
// one failed; the question an operator actually has is *which* device
// failed and why, and that answer is already in hand -- dispatch.JobStore's
// Get returns every JobTask alongside the Job -- but nothing rendered it.
//
// It reuses Field and Row rather than introducing a second table
// vocabulary, so a section inherits the column rendering, the badge class
// validation and the mobile card layout that the main list already has. A
// resource author declares one and gets all of that; the shared template
// grew one loop.
type Section struct {
	// Title is the section's heading. It is required, because a table
	// appearing under a record with no heading gives a reader no way to
	// know what they are looking at.
	Title string

	// Summary is an optional line under the heading.
	Summary string

	// Status is whether this section is backed by anything real, mirroring
	// Descriptor.Status and defaulting the same way: empty reads as
	// declared.
	//
	// It exists because a section can be honest about a gap its parent view
	// cannot. A template's Notifications section has no backing entity
	// until the Notification Engine phase, and rendering it as an empty
	// table would say "no notification policies are configured", which is
	// indistinguishable from a working section with no records and is the
	// ambiguity this project has shipped twice. A declared section renders
	// the same panel a declared view does, and its Rows are never called.
	Status Status

	// Fields are this section's columns, declared exactly like a
	// resource's own. Only InList matters here; a section has no form.
	Fields []Field

	// Rows loads the related records.
	//
	// parentID is the record the section hangs off, or empty on a
	// collection page -- the dashboard's operator notices belong to the
	// dashboard itself, not to any row of it. A section that only makes
	// sense under a record should ignore an empty parent and return
	// nothing rather than every record in the system.
	//
	// It returns rows already erased, because a section is presentation
	// rather than a resource of its own: there is no page beneath it, no
	// create form, and nothing that would need the domain type back. A
	// section that genuinely needed those is a resource, and should be
	// registered as one.
	Rows func(ctx context.Context, parentID string) ([]Row, error)

	// Empty is what renders when there are none. It is required for the
	// same reason StatusDeclared exists: an empty table and a thing that
	// has not happened yet look identical, and "no devices have reported
	// yet" and "this job dispatched to nothing" are very different facts
	// about a job somebody is investigating.
	//
	// A declared section uses it as the panel's own sentence: what will be
	// here, and what owns it.
	Empty string
}

// Implemented reports whether this section reaches a real port, the same
// question Descriptor.Implemented answers and defaulted the same way.
func (s Section) Implemented() bool { return s.Status == StatusImplemented }

// RecordAction is a named operation offered on one record, beyond create,
// read, update and delete.
//
// CRUD does not describe what an automation control plane actually does to
// a record. You launch a runbook, cancel a job, resync an inventory,
// relaunch a failed run -- none of which is an edit, and all of which AWX
// and Spacelift put on the record itself rather than on a collection form.
// Dispatching is the first one: you run *this* runbook, so the control
// belongs where the runbook is, not on a "new job" form that would ask an
// operator to retype an id they just came from a page listing.
//
// An action may prompt or not. With no Fields it is a button that posts
// straight through; with Fields it renders the same shared, validated,
// accessible form every other write uses. That is deliberately the smallest
// thing that works: Phase 21's Launchable owns the real prompting matrix --
// survey specs, saved configurations, ignored-field contracts -- and this
// is the seam it will land in rather than a competing design.
type RecordAction struct {
	// Name is the URL segment: /{resource}/{id}/{name}. It must not
	// collide with the fixed segments the route table already owns.
	Name string

	// Label is the button text. "Run", not "New".
	Label string

	// Endpoint carries the scope and relation this action is gated on,
	// exactly as Ops does, so an action button and a JSON _links entry are
	// still the same value evaluated by the same chain.
	Endpoint *apispec.Endpoint

	// Heading is the form's own title when the action prompts.
	Heading string

	// Fields prompt before the action runs. Empty means no prompt.
	Fields []Field

	// FieldsFor resolves the prompt for one particular record, when the
	// controls differ from record to record. Nil means Fields is the whole
	// prompt for every record.
	//
	// It exists because a launch form is not the same form twice. A
	// template declares which of its fields a launch may override, and
	// every other one is locked to what the template was saved with, so a
	// form built from a static list would render controls that are then
	// reported as ignored -- an affordance that does nothing, which is the
	// shape this repository has shipped and recorded before. The fields a
	// record actually opens are a property of that record, so they are
	// resolved from it.
	//
	// The resolved set is what the submission is narrowed to and validated
	// against, not just what is drawn, so a caller cannot post a control
	// the form did not offer them.
	FieldsFor func(ctx context.Context, id string) ([]Field, error)

	// Submit performs the action and returns where to send the caller
	// afterwards. A FieldErrors result redisplays the form with the
	// message attached to the control that caused it, exactly as a create
	// does, so a mistyped value is not answered with an error page.
	Submit func(ctx context.Context, id string, v Values) (redirect string, errs FieldErrors, err error)
}

// Prompts reports whether this action renders a form before running.
//
// An action resolving its fields per record prompts by definition, even
// when a particular record opens nothing: the resulting form is a
// confirmation, which is the right answer for something that launches
// production work, and the alternative would be deciding whether to prompt
// before knowing which record was being acted on.
func (a RecordAction) Prompts() bool { return len(a.Fields) > 0 || a.FieldsFor != nil }

// ResolveFields returns the prompt for one record: the per-record set when
// this action declares one, and the static set otherwise.
func (a RecordAction) ResolveFields(ctx context.Context, id string) ([]Field, error) {
	if a.FieldsFor == nil {
		return a.Fields, nil
	}
	return a.FieldsFor(ctx, id)
}

// reservedRecordSegments are the path segments the fixed route table
// already owns beneath a record. An action may not take one of these: chi
// resolves a static segment before a parameter, so the action would
// register cleanly and then never be reachable -- the silent failure this
// check exists to convert into a refusal at startup.
var reservedRecordSegments = map[string]bool{
	"edit": true,
	"logs": true,
	"new":  true,
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

// RefreshSpec declares that a view's content changes on its own while
// somebody is looking at it, so the page should keep itself current.
//
// This is what makes the control plane usable while work is actually
// running. A fan-out to five hundred devices records its outcomes over
// seconds or minutes, and without this a reader watching it has a page that
// froze at the instant they opened it, with no indication that it had. The
// honest alternatives are worse: a manual reload button asks somebody
// supervising a production change to poll by hand, and a full-page auto
// reload throws away their scroll position and their focus every few
// seconds.
//
// The refresh is a fragment swap of the region that changed, requested from
// the same URL the page came from. There is no new route and no new
// endpoint: the handler content-negotiates on the request header HTMX sets,
// so a resource declaring a refresh adds exactly this struct and nothing
// else.
type RefreshSpec struct {
	// Interval is how often the region re-requests itself.
	//
	// Validated to at least one second. A sub-second poll from every open
	// tab is a denial of service a deployment aims at itself, and the
	// figures being watched here do not change faster than a person reads
	// them.
	Interval time.Duration

	// Active decides, per record, whether the refresh is still worth making.
	// Nil means always, which is right for a collection: a new job can
	// appear at any time.
	//
	// The stopping mechanism is worth stating because it is not obvious.
	// The swap replaces the region including its own attributes, so a
	// fragment rendered while Active reports false simply carries no
	// trigger, and the polling ends there. Nothing has to remember to
	// cancel a timer, and a record that reaches a terminal state stops
	// costing requests the moment its last refresh lands.
	Active func(Row) bool
}

// minRefreshInterval is the fastest a view may ask to be refreshed.
const minRefreshInterval = time.Second

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

	// NavGroup is the sidebar heading this view is listed under. The empty
	// group renders first, with no heading.
	NavGroup NavGroup

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

	// Sections are tables of related records: a job's per-device outcomes
	// on its detail page, the operator notices on the dashboard. They are
	// read-only and rendered by the same shared template as everything
	// else, on both the collection page and the record page.
	// Refresh declares that this view keeps itself current while somebody
	// is watching it. Nil means the page is a snapshot, which is right for
	// anything that only changes when a person changes it.
	Refresh *RefreshSpec

	Sections []Section

	// Actions are named operations offered on one record beyond CRUD:
	// running a runbook, and later cancelling or relaunching a job.
	Actions []RecordAction

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

// listed reports whether a field appears as a column.
//
// A password field never does, whatever it declares. The kind exists so a
// value is not shown, and honouring InList for it would make one forgotten
// flag enough to print a secret into a table.
func (f Field) listed() bool { return f.InList && f.Kind != KindPassword }

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
	if err := validateFields(d.Fields); err != nil {
		return fmt.Errorf("view %q %s", d.Name, err)
	}
	if err := validateOps(d.Name, d.Ops, d.Actions); err != nil {
		return err
	}
	if err := validateChart(d.Name, d.Chart); err != nil {
		return err
	}
	if err := validateNavGroup(d.Name, d.NavGroup); err != nil {
		return err
	}
	if err := validateRefresh(d.Name, d.Refresh); err != nil {
		return err
	}
	if err := validateStream(d.Name, d.Stream); err != nil {
		return err
	}
	if err := validateSections(d.Name, d.Sections); err != nil {
		return err
	}
	if err := validateActions(d.Name, d.Actions); err != nil {
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
	case !d.Implemented() && d.Refresh != nil:
		return fmt.Errorf("view %q is declared but carries a refresh", d.Name)
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
	// same empty path -- and one naming a field it never produces would
	// link every record to a 404.
	case d.ListsRecords() && validateIdentity(d.Fields, d.IDField) != nil:
		return fmt.Errorf("view %q lists records but %s", d.Name, validateIdentity(d.Fields, d.IDField))

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

// validateSections refuses a section that would render as an unlabelled or
// unexplained table.
func validateSections(name string, sections []Section) error {
	titles := make(map[string]bool, len(sections))
	for _, s := range sections {
		switch {
		case strings.TrimSpace(s.Title) == "":
			// An unheaded table under a record tells a reader nothing
			// about what they are looking at.
			return fmt.Errorf("view %q has a detail section with no title", name)
		case titles[s.Title]:
			// Two sections sharing a heading make the page ambiguous and
			// would collide on the element id the heading is referenced by.
			return fmt.Errorf("view %q declares the detail section %q twice", name, s.Title)
		case s.Implemented() && s.Rows == nil:
			return fmt.Errorf("view %q detail section %q has no Rows function", name, s.Title)
		case !s.Implemented() && s.Rows != nil:
			// The same contradiction Register refuses on a Descriptor: a
			// section that says it is not implemented while loading live
			// rows is exactly the ambiguity the declared status removes.
			return fmt.Errorf("view %q detail section %q is declared but carries a Rows function", name, s.Title)
		case strings.TrimSpace(s.Empty) == "":
			// "Nothing here yet" and "nothing was ever attempted" look
			// identical as a blank table, and on a job under investigation
			// they are very different facts.
			return fmt.Errorf("view %q detail section %q has no empty-state text", name, s.Title)
		}
		titles[s.Title] = true

		if err := validateFields(s.Fields); err != nil {
			return fmt.Errorf("view %q detail section %q %s", name, s.Title, err)
		}
	}
	return nil
}

// validateActions refuses an action that could not be reached or could not
// be gated.
func validateActions(name string, actions []RecordAction) error {
	seen := make(map[string]bool, len(actions))
	for _, a := range actions {
		switch {
		case !namePattern.MatchString(a.Name):
			return fmt.Errorf("view %q action name %q must match %s", name, a.Name, namePattern)
		case reservedRecordSegments[a.Name]:
			// chi resolves a static segment before a parameter, so this
			// action would register cleanly and never be reachable. Better
			// a refusal at startup than a button that silently opens the
			// edit form.
			return fmt.Errorf("view %q action %q collides with a reserved path segment", name, a.Name)
		case seen[a.Name]:
			return fmt.Errorf("view %q declares action %q twice", name, a.Name)
		case strings.TrimSpace(a.Label) == "":
			// A button with no text has no accessible name.
			return fmt.Errorf("view %q action %q has no label", name, a.Name)
		case a.Endpoint == nil:
			// Without an endpoint there is no scope to enforce and no
			// relation to gate the control on, so the button would render
			// for everybody and the route would be unguarded.
			return fmt.Errorf("view %q action %q names no endpoint, so nothing gates it", name, a.Name)
		case a.Submit == nil:
			return fmt.Errorf("view %q action %q has no Submit function", name, a.Name)
		case a.Prompts() && strings.TrimSpace(a.Heading) == "":
			return fmt.Errorf("view %q action %q prompts but has no heading", name, a.Name)
		}
		seen[a.Name] = true

		if err := validateFields(a.Fields); err != nil {
			return fmt.Errorf("view %q action %q %s", name, a.Name, err)
		}
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
func validateOps(name string, ops Ops, actions []RecordAction) error {
	known := make(map[string]apispec.Endpoint, len(apispec.Endpoints))
	for _, e := range apispec.Endpoints {
		known[e.Name] = e
	}

	// Operations and actions share one relation namespace, because
	// Affordances is keyed by relation and a template asks Can(rel). Two
	// entries sharing a relation would make permitting either permit both,
	// which on an action means offering an operation nobody granted.
	endpoints := ops.all()
	for _, a := range actions {
		if a.Endpoint != nil {
			endpoints = append(endpoints, a.Endpoint)
		}
	}

	seenRel := make(map[auth.LinkRel]string, len(endpoints))
	for _, e := range endpoints {
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

// CheckReferences verifies that every field declaring References names a
// view that is actually registered.
//
// It is a separate call rather than part of Register because a cross-view
// reference cannot be validated at registration time: registration order is
// map iteration, so the target may legitimately not exist yet when the
// referencing view registers. Running it once after every view is in turns
// a dangling reference into a startup refusal instead of a link that 404s
// the first time somebody clicks it.
//
// It reports every problem it finds rather than the first, because fixing
// them one restart at a time is how a six-line mistake takes six restarts.
func CheckReferences() error {
	var problems []string
	for _, name := range Names() {
		d, ok := Lookup(name)
		if !ok {
			continue
		}
		for _, f := range d.Fields {
			if !f.Referencing() {
				continue
			}
			if _, exists := Lookup(f.References); !exists {
				problems = append(problems, fmt.Sprintf(
					"view %q field %q references view %q, which is not registered", name, f.Name, f.References))
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("view references are broken:\n  %s", strings.Join(problems, "\n  "))
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

// validateRefresh rejects a refresh nothing could honour.
//
// The interval floor is the only real rule here, and it is a fail-fast for a
// mistake with no other symptom: a resource declaring a 100ms refresh works
// perfectly in a one-tab test and quietly multiplies every open tab by ten
// requests a second against the same handler that renders the page.
func validateRefresh(name string, spec *RefreshSpec) error {
	if spec == nil {
		return nil
	}
	if spec.Interval < minRefreshInterval {
		return fmt.Errorf("view %q declares a refresh interval of %s, below the %s minimum",
			name, spec.Interval, minRefreshInterval)
	}
	return nil
}
