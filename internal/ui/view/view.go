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
	"io"
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
	// A section's row controls are gated on this same permitted set, so a
	// relation reached only from one has to be offered for evaluation. Left
	// out, the generator is never asked about it, it is never permitted,
	// and every one of those controls is silently withheld from everybody
	// -- a Remove button that is simply never drawn, which reads as a
	// design decision rather than as a bug.
	for _, s := range d.Sections {
		for _, a := range s.RowActions {
			if a.Endpoint == nil {
				continue
			}
			out = append(out, auth.Affordance{Rel: a.Endpoint.Rel, Scope: a.Endpoint.Scope})
		}
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

	// Note is an optional line rendered under the heading, resolved per
	// RECORD rather than declared once, which is the whole distinction
	// from Summary above.
	//
	// It exists because a section had no way to say anything true of the
	// rows it just loaded. The first thing that needed one is a bounded
	// read: a table capped at five hundred rows of a larger run shows a
	// partial record of what happened while looking exactly like a
	// complete one, and on an audit trail that is the worst available
	// outcome. Logging the cap tells the operator nothing, because the
	// operator is not reading the log.
	//
	// It is prose, not markup, and it is escaped like any other text. A
	// section that wants to say something structural wants a column.
	Note func(ctx context.Context, parentID string) string

	// Empty is what renders when there are none. It is required for the
	// same reason StatusDeclared exists: an empty table and a thing that
	// has not happened yet look identical, and "no devices have reported
	// yet" and "this job dispatched to nothing" are very different facts
	// about a job somebody is investigating.
	//
	// A declared section uses it as the panel's own sentence: what will be
	// here, and what owns it.
	Empty string

	// Actions name RecordActions offered in this section's header, acting
	// on the record the section hangs off rather than on any row of it:
	// "Add input" on a credential type's Inputs section, "Add question" on
	// a template's Survey. Each name must be one of the parent
	// Descriptor's own Actions, so a section reuses the whole action path
	// -- the shared form, its validation, its scope gate -- rather than
	// inventing a second write surface. Register refuses a name that
	// matches no declared action.
	//
	// A section reaches the collection page as well as a record page, and
	// on the collection page there is no record for these to act on, so
	// they render only where a parent id exists. That is a rendering
	// decision rather than a validation one, because the same declaration
	// is correct on both pages: act on the record when there is one, offer
	// nothing when there is not.
	//
	// Only an implemented section may name them. A declared section reaches
	// no port, so an action button on it would post to a write path its own
	// panel says is not wired -- the contradiction StatusDeclared exists to
	// remove.
	Actions []string

	// RowActions are controls on one row of this section rather than on
	// the record it hangs off: "Remove" on a credential type's input,
	// "Remove" on one of its injectors.
	//
	// Declared here rather than named from the parent's Actions, the way
	// the header half is, because a row action's target is different in
	// kind. A record action acts on the record the URL names; a row action
	// acts on one element of a document that record holds, and which
	// element is the only thing separating one control in the table from
	// the one on the line below it.
	//
	// Only an implemented section may declare them, for the reason Actions
	// gives, and they render only where a parent id exists, for the reason
	// Actions gives.
	RowActions []RowAction
}

// Implemented reports whether this section reaches a real port, the same
// question Descriptor.Implemented answers and defaulted the same way.
func (s Section) Implemented() bool { return s.Status == StatusImplemented }

// RowPosition is where a row sits in the list a control is drawn on.
//
// It exists so RowAction.Applies can withhold a control whose only possible
// outcome on this row is a refusal, which for an ordered list means the two
// ends: "move up" on the first row, "move down" on the last. A Row cannot
// answer that itself. Its Cells are display strings a section author chose
// and its ID is author data, so reading an ordinal back out of either would
// be parsing a label, and a section that happened not to render an order
// column could not be reordered at all.
//
// Zero-based, matching the slice the resolver is walking. Count is how many
// rows that section LOADED rather than how many the store holds, which is
// the honest bound: a control can only move a row past one the page is
// showing.
type RowPosition struct {
	Index int
	Count int
}

// First reports whether this row is the first of its list.
func (p RowPosition) First() bool { return p.Index <= 0 }

// Last reports whether this row is the last of its list.
//
// A single-row list is both First and Last, which is the correct reading:
// neither direction moves it anywhere, so both controls are withheld.
func (p RowPosition) Last() bool { return p.Index >= p.Count-1 }

// RowAction is a control on one row of a section.
//
// The header half of the section write path shipped first: a section names
// one of its parent record's actions and renders a button that acts on the
// record, which is how "Add input" reaches a credential type. That left
// every section a one way door. An input could be added to a credential
// type and never removed, an injector added and never removed, and the only
// route back was the JSON API or the database.
//
// It began as two ids and no Values, on the reasoning that a row control
// never prompts: removing a row needs no form and reordering one needs no
// form, while editing a row in place needs a form prefilled from that row,
// which RecordAction has no seam for (its own doc comment records that its
// form prefills nothing). That reasoning was right about the hazard and
// wrong about the conclusion. The seam was built here instead, because a
// row is the one thing on the page that already exists and can therefore be
// read back: Fields and Form arrive together or not at all, and Register
// refuses one without the other precisely so the empty-boxes-that-blank-the
// -row failure cannot be reintroduced by declaring half of it.
type RowAction struct {
	// Name is the URL segment: /{resource}/{id}/{name}/{row}. It shares
	// one namespace with the parent's RecordActions, because both occupy
	// the same segment of the same route, and Register enforces that.
	Name string

	// Label is the button text.
	Label string

	// Endpoint carries the scope and relation this control is gated on,
	// exactly as a RecordAction does. Two controls may name one endpoint:
	// adding to a document and removing from it are usually the same API
	// operation, and the affordance question has one answer for both.
	Endpoint *apispec.Endpoint

	// Heading is the form's own title when this control prompts.
	Heading string

	// Confirm is what a confirmation dialog asks before the control posts.
	// Empty posts straight through.
	//
	// Setting it also renders the control as a destructive one, because
	// the only reason to interrupt somebody on their way to a button is
	// that what is behind it is hard to undo.
	//
	// Refused beside Fields. A confirming control posts from inside a
	// dialog whose only content is the CSRF token, so on a prompting
	// control that submission would reach Submit with every control blank
	// and look like a deliberate save: the silent blanking this whole seam
	// exists to prevent, arriving through the one door nobody is watching.
	// A form is already the interruption.
	Confirm string

	// Fields prompt before the action runs. Empty means no prompt, which
	// is what Remove wants: removing a row needs no form, and neither does
	// moving one.
	//
	// There is deliberately no FieldsFor. A record action has one because
	// a launch form is not the same form twice; a row control's fields are
	// the same for every row of its section and only the VALUES differ, so
	// one field set per control is the whole truth and a per row
	// resolution would be a second answer that could disagree.
	//
	// The form is an EDIT form, because a row is a thing that already
	// exists. Immutable therefore means what it means everywhere else: the
	// control naming the row is offered by the add form beside this one
	// and withheld here, so one field slice serves both.
	Fields []Field

	// Form produces the values that prefill this row's controls, and is
	// required exactly when Fields is present.
	//
	// Prompting and prefilling are one decision on a row, not two, and
	// Register enforces it. A prompt with no prefill renders the row's
	// current values as empty boxes and silently blanks whichever ones the
	// operator does not retype, which is precisely the failure this seam
	// was built to remove; allowing the combination would leave the trap
	// open at the one place it is most likely to be sprung.
	//
	// It re-reads the row rather than being handed the Row the table drew,
	// and that is not redundancy. A Row's Cells are display strings and a
	// form value is a submission token, the same disagreement
	// Projector.Row and Projector.Form already have one level up. A
	// credential type's input renders "yes" in its REQUIRED column where a
	// checkbox reads only the literal "true", renders "string" in TYPE
	// where the select posts its own value, and its MULTILINE, HELP and
	// DEFAULT never appear in a column at all. A prefill built from cells
	// would be wrong in three controls and blank in three more.
	Form func(ctx context.Context, parentID, rowID string) (map[string]string, error)

	// Applies withholds this control from a row it could not work on, the
	// same job Descriptor.Applies does for a record. Nil offers it on
	// every row.
	//
	// It takes the row's position as well as the row, because the first
	// control that needed this could not be written without it: "move up"
	// on the first row of an ordered list is a button whose only possible
	// outcome is a refusal. A Row carries no ordinal -- its Cells are
	// display strings and its ID is author data -- so the position comes
	// from the resolver, which is counting the rows anyway.
	//
	// Gating here is about not drawing a dead control and never about
	// safety: the row may stop qualifying between the page rendering and
	// the button being pressed, so Submit is still the authority and still
	// has to refuse.
	Applies func(row Row, at RowPosition) bool

	// Submit performs the action and returns where to send the caller
	// afterwards. An empty redirect returns them to the parent record.
	//
	// It receives both identities and adjudicates neither. rowID is the row
	// the URL named; v carries what the form was told, narrowed to the
	// controls that form actually offered. A resource whose identity column
	// is also an editable control decides for itself whether the two
	// disagreeing is a rename or a refusal, because only it knows.
	//
	// v is the zero Values for a control that does not prompt: it declares
	// nothing, so it can read nothing. A FieldErrors result redisplays the
	// form with the message on the control that caused it; a Refused
	// reaches the notice page, which is where a control with no form has
	// always sent one.
	Submit func(ctx context.Context, parentID, rowID string, v Values) (redirect string, errs FieldErrors, err error)
}

// Prompts reports whether this control renders a form before it runs.
func (a RowAction) Prompts() bool { return len(a.Fields) > 0 }

// ResolveValues returns this row's prefill, and an empty map for a control
// that does not prompt.
func (a RowAction) ResolveValues(ctx context.Context, parentID, rowID string) (map[string]string, error) {
	if a.Form == nil {
		return map[string]string{}, nil
	}
	values, err := a.Form(ctx, parentID, rowID)
	if err != nil {
		return nil, err
	}
	if values == nil {
		return map[string]string{}, nil
	}
	return values, nil
}

// Confirms reports whether this control interrupts before it posts.
func (a RowAction) Confirms() bool { return strings.TrimSpace(a.Confirm) != "" }

// Refused is a refusal a row action's Submit returns when the reason is one
// the operator can act on, as against a failure that is nobody's doing.
//
// The two must not be answered the same way. "An injector depends on this
// input" is a rule the person who pressed the button can satisfy by
// removing the injector first; a store that could not be reached is not.
// Without this distinction both reach serverError, which logs the real
// reason where the operator cannot see it and answers them with the words
// "internal error", so a fixable refusal reads as a fault in the product.
//
// Wrap the store's error rather than restating it: Unwrap keeps errors.Is
// working for whatever is above, and the message shown is the store's own
// words, because the store is the authority on why it refused.
type Refused struct {
	// Message is what the operator is told. The store's own sentence.
	Message string

	// Err is the refusal being carried, kept reachable for errors.Is.
	Err error
}

// Error makes Refused an error carrying the message it shows.
func (r Refused) Error() string { return r.Message }

// Unwrap keeps errors.Is and errors.As working through the wrapper.
func (r Refused) Unwrap() error { return r.Err }

// Refuse wraps an error as a refusal shown to the operator in its own
// words. A nil error refuses nothing and returns nil, so a caller can hand
// it a store result without first asking whether there was one.
func Refuse(err error) error {
	if err == nil {
		return nil
	}
	return Refused{Message: err.Error(), Err: err}
}

// RowAction finds a row action by name across every section that declares
// one.
//
// One lookup across all sections rather than per section, because the route
// carries no section: /{resource}/{id}/{action}/{row} names the control and
// the row and nothing between them. Register keeps that honest by refusing
// two sections to declare the same name.
func (d Descriptor) RowAction(name string) (RowAction, bool) {
	for _, s := range d.Sections {
		for _, a := range s.RowActions {
			if a.Name == name {
				return a, true
			}
		}
	}
	return RowAction{}, false
}

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

	// Form produces the values that prefill this action's prompt.
	//
	// Nil prefills nothing, which is right for an ADD and right for a
	// launch: appending an input to a credential type starts from nothing,
	// and every answer to a survey is given afresh. What it is not right
	// for is a form that REPLACES something the record already holds, and
	// that shape shipped without it. bindCredentialsAction resolved a
	// template's bound credentials into a variable, had nowhere to put it
	// and dropped it, so the multi-select rendered with nothing selected
	// and submitting the form as drawn unbound every credential the
	// template authenticated as.
	//
	// Same name, same return type and same job as Handlers.Form, the edit
	// form's own prefill, because a second answer to "where do a form's
	// existing values come from" is a second one to keep in step with
	// every Field kind that ever renders a value. The map is keyed by
	// Field.Name and encoded the way a submission encodes it: "true" for a
	// checked box, the option's own value for a select, a comma joined
	// list for a multi select. A key naming no rendered control is refused
	// rather than ignored, and so is a value for a password control; see
	// NarrowPrefill for both, and for why silently dropping either is the
	// failure this seam exists to prevent.
	Form func(ctx context.Context, id string) (map[string]string, error)

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

// ResolveValues returns this prompt's prefill for one record, and an empty
// map for an action that declares none.
//
// An empty map rather than a nil one, so a caller never has to ask which
// kind of nothing it was handed.
func (a RecordAction) ResolveValues(ctx context.Context, id string) (map[string]string, error) {
	if a.Form == nil {
		return map[string]string{}, nil
	}
	values, err := a.Form(ctx, id)
	if err != nil {
		return nil, err
	}
	if values == nil {
		return map[string]string{}, nil
	}
	return values, nil
}

// reservedRecordSegments are the path segments the fixed route table
// already owns beneath a record. An action may not take one of these: chi
// resolves a static segment before a parameter, so the action would
// register cleanly and then never be reachable -- the silent failure this
// check exists to convert into a refusal at startup.
//
// "download" is here because internal/ui/web/handler.go says it is. That
// file mounts the static segments above the parameterised action route and
// states that Register refuses a clash, which was true of the first three
// and not of the fourth for as long as downloads have existed: a section
// row action named "download" registered cleanly and was then shadowed by
// the download route forever, with nothing anywhere reporting it.
var reservedRecordSegments = map[string]bool{
	"download": true,
	"edit":     true,
	"logs":     true,
	"new":      true,
}

// DownloadSpec declares one thing a record can be downloaded AS.
//
// A list rather than a single spec, and resolved per record rather than
// declared once, because the two downloads a job has are complementary and
// neither exists for every job. The run journal is durable forever and is
// written only by the native runbook executor, so a playbook job has none;
// the log output lives in the broker's retention window and carries
// per-task detail only for a playbook job, so for a runbook job it is two
// lines and after the window it is nothing at all.
//
// Offering both on every record would hand an operator an empty file about
// half the time. Offering only what this record actually has is the same
// rule RowAction.Applies follows one level down: a control whose only
// possible outcome is a refusal must not be drawn.
//
// Available is the chooser's gate and Write is the authority. A record can
// stop qualifying between the page rendering and the link being followed --
// a log window can expire -- so Write still has to cope, exactly as a row
// action's Submit does.
type DownloadSpec struct {
	// Name is the URL segment: /{resource}/{id}/download/{name}. It shares
	// no namespace with actions, because it sits behind its own static
	// segment, but it must still be a legal path token.
	Name string

	// Label is what the control says. It names the ARTEFACT rather than
	// the act, because the control already says Download: "Run journal
	// (JSON)" tells a reader what they will get, where "Download JSON"
	// tells them what they already knew.
	Label string

	// Summary is an optional line explaining what this download is and,
	// where it matters, what it is not. The log download's says how long
	// the broker keeps it, because an operator who finds it missing next
	// month should have been told.
	Summary string

	// ContentType is the response's media type, declared and never
	// sniffed, matching the stance internal/ui/static takes for the same
	// reason: a browser deciding for itself that a file is executable
	// script is precisely what the nosniff header exists to stop.
	ContentType string

	// Filename is the name the browser saves under, with {id} replaced by
	// the record's identifier. A pattern rather than a function, for the
	// reason StreamSpec.PathPattern gives: substituting into a validated
	// pattern means the escaping happens once, here.
	Filename string

	// Available reports whether this record has anything to download in
	// this form. Nil offers it on every record, which is right only for a
	// format that cannot be empty.
	Available func(ctx context.Context, id string) bool

	// Write streams the body. It owns the encoding and nothing else: the
	// status, the headers and the disposition are set before it is called,
	// so an error it returns after the first byte can only be logged.
	//
	// That is the same constraint the SSE stream lives under and it is why
	// Available exists: the decision that a download is possible has to be
	// made before the response is committed.
	Write func(ctx context.Context, w io.Writer, id string) error
}

// validateDownloads checks a descriptor's download declarations.
func validateDownloads(name string, downloads []DownloadSpec) error {
	seen := make(map[string]bool, len(downloads))
	for _, dl := range downloads {
		switch {
		case !namePattern.MatchString(dl.Name):
			return fmt.Errorf("view %q download name %q must match %s", name, dl.Name, namePattern)
		case seen[dl.Name]:
			return fmt.Errorf("view %q declares download %q twice", name, dl.Name)
		case strings.TrimSpace(dl.Label) == "":
			// A link with no text has no accessible name.
			return fmt.Errorf("view %q download %q has no label", name, dl.Name)
		case strings.TrimSpace(dl.ContentType) == "":
			// Declared and never sniffed, so an absent one is not a
			// default to fill in: it is a decision nobody made.
			return fmt.Errorf("view %q download %q declares no content type", name, dl.Name)
		case !strings.Contains(dl.Filename, "{id}"):
			// Every record would otherwise save under one name, and a
			// reader with three jobs' journals in a folder could not tell
			// them apart.
			return fmt.Errorf("view %q download %q has no {id} in its filename %q", name, dl.Name, dl.Filename)
		case dl.Write == nil:
			return fmt.Errorf("view %q download %q has no Write function", name, dl.Name)
		}
		seen[dl.Name] = true
	}
	return nil
}

// Offers reports whether this download is available for one record.
func (d DownloadSpec) Offers(ctx context.Context, id string) bool {
	return d.Available == nil || d.Available(ctx, id)
}

// FilenameFor is the name a record saves under.
//
// The id is sanitised to the characters a filename may safely carry rather
// than escaped, because the result goes into a Content-Disposition header
// where a quote or a newline is a header-injection question rather than a
// display one.
func (d DownloadSpec) FilenameFor(id string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-', r == '_', r == '.':
			return r
		default:
			return '-'
		}
	}, id)
	if safe == "" {
		safe = "record"
	}
	return strings.ReplaceAll(d.Filename, "{id}", safe)
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

	// NameField is the field whose value titles a record's page.
	//
	// Separate from IDField because the two answer different questions. An
	// identifier addresses a record; a name says what it is, and a page
	// headed by a primary key has moved the join into the reader's head.
	// Optional: a view whose identifier already is its name leaves this
	// empty and TitleField falls back, which is why Devices and Templates
	// need no declaration here and Jobs does.
	NameField string

	// DefaultTab is the part of a record that opens first, named by its
	// section title or by the stream's title. Empty opens the record's own
	// fields, which is the right answer for almost every view.
	//
	// Jobs is why it exists. A job's details are what it was asked to do
	// and its output is what happened, and somebody opening a job has
	// nearly always come for the second: AWX lands on Output for exactly
	// this reason, and a reader who has to click through to it every time
	// is a reader the page is getting in the way of.
	//
	// Named by title rather than by slug so a declaration reads as English
	// and cannot drift from the section it points at: a title that matches
	// nothing falls back to the record's fields, which ResolveDefaultTab
	// is where that is decided.
	DefaultTab string

	// StatusBadgeField names the field whose value is this record's state,
	// rendered as a badge beside its title. Empty means no badge.
	//
	// Declared rather than inferred, and the inference it replaced is why.
	// Taking the first listed badge field looked reasonable and was wrong
	// almost everywhere: a job's first badge is its KIND, so a failed job was
	// headed "4821 runbook" rather than "4821 failed", and a runbook's only
	// badge is INTERRUPTIBLE, so every runbook record was headed with the
	// word "YES". A badge beside a title is a claim that this value is what
	// the record currently IS, and most badge fields are properties rather
	// than states. Nothing can tell the two apart by looking, so the
	// descriptor says which.
	StatusBadgeField string

	// Empty is what a list with no rows says.
	//
	// Section has carried one of these since it existed, and uses it well:
	// "this job has not recorded any per-device outcomes yet" and "this
	// dispatched to nothing" are very different facts that a blank table
	// renders identically. The collection view had no equivalent, so every
	// one of them said "No records." -- which conflates a first-run
	// install, a filtered-to-nothing search and a genuinely idle fleet.
	Empty string

	// Chart optionally adds one chart to this view.
	Chart *ChartSpec

	// Stream optionally declares that each of this resource's records has
	// a live event stream, which is what makes /{resource}/{id}/logs
	// resolve for this resource and 404 for every other one.
	Stream *StreamSpec

	// Downloads are the forms a record can be saved AS, resolved per
	// record. Declaring any is what makes /{resource}/{id}/download/{name}
	// resolve for this resource and 404 for every other one.
	Downloads []DownloadSpec

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

	// FieldsFor optionally resolves additional fields for one record's edit
	// form and update submission, appended after the static Fields. Nil
	// means Fields is the whole form for every record, which is every view
	// but one whose real field set depends on data only the record itself
	// carries.
	//
	// It exists for Templates: a template's execution fields (forks, limit,
	// and the rest) are declared per launch.Kind, and a kind arrives in a
	// file this package has never seen, so they cannot be a fixed part of
	// Fields the way RecordAction.FieldsFor already cannot be a fixed part
	// of an action's Fields, for the same reason. This is that seam
	// generalised to the record's own form rather than a second one invented
	// for it.
	//
	// It applies to create as well as edit, which it did not until
	// Credentials needed it. The original seam took an id and refused a
	// create outright, on the reasoning that there is nothing to resolve a
	// per-record field set from before the record exists. That reasoning
	// held only because Templates, its one caller, drives its dynamic
	// fields off a kind the stored record already names.
	//
	// A credential does not work that way. Its fields are declared by the
	// credential type the person is choosing right now, in the form, so
	// what drives the resolution is a value in the submission rather than
	// a value in the database, and it exists on a create exactly as much
	// as on an edit. Resolve carries both: an id that is empty on a create,
	// and the submission narrowed to the static fields. A caller that only
	// wants the id keeps working by reading r.ID and ignoring r.Values.
	FieldsFor func(ctx context.Context, r Resolve) ([]Field, error)

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

// FormFields returns the fields a create form offers, in declaration
// order.
func (d Descriptor) FormFields() []Field { return d.FormFieldsFor(false) }

// FormFieldsFor returns the fields a form of the given mode offers, in
// declaration order. An edit form drops the immutable ones.
func (d Descriptor) FormFieldsFor(editing bool) []Field {
	return filterFields(d.Fields, func(f Field) bool { return f.WritableOn(editing) })
}

// ResolveFormFields returns the fields one record's edit form offers: the
// static set FormFieldsFor already answers, plus whatever FieldsFor
// resolves for that record. A create (id empty) or a view declaring no
// FieldsFor gets exactly FormFieldsFor's answer, unchanged.
//
// The merged result is what the render path, the submission narrower and
// Validate all have to agree on, so it is computed once, here, rather than
// separately by each of them.
func (d Descriptor) ResolveFormFields(ctx context.Context, r Resolve) ([]Field, error) {
	fields := d.FormFieldsFor(r.Editing())
	if d.FieldsFor == nil {
		return fields, nil
	}
	extra, err := d.FieldsFor(ctx, r)
	if err != nil {
		return nil, err
	}
	return append(fields, extra...), nil
}

// Resolve is what a dynamic form field set is resolved against: which
// record, and what the driving controls currently hold.
//
// Values is the submission narrowed to the STATIC fields only, which is the
// answer to the ordering problem this type exists for. The set of declared
// fields depends on a submitted value, and narrowing a submission requires
// knowing the declared set, so one of the two has to go first. The static
// set does: it is fixed, it is what a driving control belongs to, and
// narrowing to it is enough to read the one value the resolution turns on.
//
// The narrowing that matters for safety is the SECOND one, which the caller
// performs against the merged set this resolution returns. Nothing read
// here reaches a domain object; it only decides which controls exist. That
// is why this pass may read the query string while the second may not.
type Resolve struct {
	// ID is the record being edited, empty on a create.
	ID string

	// Values is the submission narrowed to the static form fields.
	Values Values
}

// Editing reports whether this resolution is for an existing record.
func (r Resolve) Editing() bool { return r.ID != "" }

// TitleField is the field whose value titles a record page, empty when this
// view has neither a declared name nor an identity field.
//
// The fallback to IDField is what lets most views declare nothing: a device
// and a template are already addressed by their names, so the identifier is
// the name. Only a view addressed by a generated key -- a job -- needs to say
// which of its fields a reader would recognise.
func (d Descriptor) TitleField() string {
	if d.NameField != "" {
		return d.NameField
	}
	return d.IDField
}

// StatusField is the field a record's state badge reads, and whether there is
// one at all.
//
// Strictly what StatusBadgeField names, and nothing when it names nothing. A
// view that wants a badge beside its title says so; a view that does not gets
// no badge rather than the first one this package could find. It must be a
// badge field and it must be listed, because a badge class comes from a
// BadgeClass function and an unlisted field is one the view chose not to show.
func (d Descriptor) StatusField() (Field, bool) {
	if d.StatusBadgeField == "" {
		return Field{}, false
	}
	for _, f := range d.Fields {
		if f.Name == d.StatusBadgeField && f.Kind == KindBadge && f.listed() {
			return f, true
		}
	}
	return Field{}, false
}

// EmptyText is what a list with no rows says, never blank.
//
// The fallback names the view, because "No records." on a page whose heading
// already says Jobs is a sentence that spends a line to say nothing. A view
// that wants better declares Empty.
func (d Descriptor) EmptyText() string {
	if d.Empty != "" {
		return d.Empty
	}
	return "No " + strings.ToLower(d.Title) + " to show."
}

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

// SnapshotForTest captures the process-wide web UI view registry and returns a
// function that puts it back, for a test that registers into it.
//
// Without this a test's registration outlives the test, so a second
// iteration under `go test -count>1` fails on a duplicate registration
// rather than starting clean. Call it once at the top of such a test:
//
//	t.Cleanup(view.SnapshotForTest())
//
// It is exported rather than living in an export_test.go because a
// _test.go file cannot be imported across package boundaries, and tests in
// other packages register here too. internal/archtest forbids production
// code from calling it.
func SnapshotForTest() func() {
	return views.SnapshotForTest()
}

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
	if err := validateOps(d.Name, d.Ops, d.Actions, d.Sections); err != nil {
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
	if err := validateSections(d.Name, d.Sections, d.Actions); err != nil {
		return err
	}
	if err := validateActions(d.Name, d.Actions); err != nil {
		return err
	}
	if err := validateRowActions(d.Name, d.Sections, d.Actions); err != nil {
		return err
	}
	if err := validateDownloads(d.Name, d.Downloads); err != nil {
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
	case !d.Implemented() && len(d.Downloads) > 0:
		// And a download most of all. The record page renders the honest
		// "not implemented" panel, while /download/{format} would serve
		// the real bytes at 200 -- a view telling every reader it is
		// unbuilt while handing out files.
		return fmt.Errorf("view %q is declared but declares downloads", d.Name)

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
// unexplained table, or one whose header actions name nothing.
func validateSections(name string, sections []Section, actions []RecordAction) error {
	declared := make(map[string]bool, len(actions))
	for _, a := range actions {
		declared[a.Name] = true
	}
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

		if len(s.Actions) > 0 && !s.Implemented() {
			// A declared section reaches no port, so a header action on it
			// would post to a write path the same panel says is not wired.
			return fmt.Errorf("view %q detail section %q is declared but declares header actions", name, s.Title)
		}
		if len(s.RowActions) > 0 && !s.Implemented() {
			// The same contradiction one row down. A declared section has
			// no rows either, so this is a control that could never even
			// be drawn.
			return fmt.Errorf("view %q detail section %q is declared but declares row actions", name, s.Title)
		}
		for _, a := range s.Actions {
			if !declared[a] {
				// A section header action names one of the parent view's
				// own actions, so it can reuse that action's form, scope
				// and handler. A name matching none would render a button
				// to a route nobody mounted -- the silent 404 the endpoint
				// checks exist to convert into a startup refusal.
				return fmt.Errorf("view %q detail section %q names header action %q, which is not a declared action", name, s.Title, a)
			}
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
		case a.Form != nil && !a.Prompts():
			// A prefill for a form that never renders. Harmless today and
			// a trap tomorrow: it reads as though the action carries the
			// record's current values, so whoever later gives the action
			// fields will believe the prefill is already wired and will
			// not check that it reaches anything.
			return fmt.Errorf("view %q action %q declares a prefill but no fields, so nothing renders it", name, a.Name)
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
// validateRowActions refuses a row control that could not be reached,
// could not be gated, or would shadow another control's route.
//
// Names are checked against the parent's RecordActions and against every
// other section's row actions, because all three occupy the same segment of
// the same route. Two declarations sharing a name would register cleanly
// and one of them would silently never run.
func validateRowActions(name string, sections []Section, actions []RecordAction) error {
	seen := make(map[string]string, len(actions))
	for _, a := range actions {
		seen[a.Name] = "an action"
	}

	for _, s := range sections {
		for _, a := range s.RowActions {
			switch {
			case !namePattern.MatchString(a.Name):
				return fmt.Errorf("view %q section %q row action name %q must match %s",
					name, s.Title, a.Name, namePattern)
			case reservedRecordSegments[a.Name]:
				// chi resolves a static segment before a parameter, so
				// this control would register cleanly and never be
				// reachable.
				return fmt.Errorf("view %q section %q row action %q collides with a reserved path segment",
					name, s.Title, a.Name)
			case seen[a.Name] != "":
				return fmt.Errorf("view %q section %q row action %q collides with %s of the same name",
					name, s.Title, a.Name, seen[a.Name])
			case strings.TrimSpace(a.Label) == "":
				// A button with no text has no accessible name.
				return fmt.Errorf("view %q section %q row action %q has no label", name, s.Title, a.Name)
			case a.Endpoint == nil:
				// Without an endpoint there is no scope to enforce and no
				// relation to gate the control on, so the button would
				// render for everybody and the route would be unguarded.
				return fmt.Errorf("view %q section %q row action %q names no endpoint, so nothing gates it",
					name, s.Title, a.Name)
			case a.Submit == nil:
				return fmt.Errorf("view %q section %q row action %q has no Submit function", name, s.Title, a.Name)
			case a.Prompts() && a.Form == nil:
				// The whole point of the seam. A prompt with no prefill
				// renders the row's current values as empty boxes and
				// blanks whichever ones the operator does not retype, and
				// nothing on the page shows it happening.
				return fmt.Errorf("view %q section %q row action %q prompts but declares no prefill, so its form would blank the row", name, s.Title, a.Name)
			case a.Form != nil && !a.Prompts():
				return fmt.Errorf("view %q section %q row action %q declares a prefill but no fields, so nothing renders it", name, s.Title, a.Name)
			case a.Prompts() && strings.TrimSpace(a.Heading) == "":
				return fmt.Errorf("view %q section %q row action %q prompts but has no heading", name, s.Title, a.Name)
			case a.Prompts() && a.Confirms():
				// A confirming control posts from a dialog carrying only
				// the CSRF token, so on a prompting control that
				// submission reaches Submit with every field blank and
				// looks like a deliberate save. The form is already the
				// interruption.
				return fmt.Errorf("view %q section %q row action %q both prompts and confirms, so the dialog would submit a blank form", name, s.Title, a.Name)
			}

			if err := validateFields(a.Fields); err != nil {
				return fmt.Errorf("view %q section %q row action %q %s", name, s.Title, a.Name, err)
			}
			seen[a.Name] = fmt.Sprintf("a row action on section %q", s.Title)
		}
	}
	return nil
}

func validateOps(name string, ops Ops, actions []RecordAction, sections []Section) error {
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
	// A section's row controls are gated by the same Affordances map and
	// rendered on the same page, so an endpoint reached only from one is
	// checked here too. Left out, a row action naming a stale copy of an
	// endpoint would render the old scope's button against the new
	// scope's route, which is the failure the stale check above exists
	// for.
	for _, s := range sections {
		for _, a := range s.RowActions {
			if a.Endpoint != nil {
				endpoints = append(endpoints, a.Endpoint)
			}
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
		if prev, dup := seenRel[e.Rel]; dup && prev != e.Name {
			// Two DIFFERENT operations sharing a relation makes a
			// permitted result ambiguous: a template asking Can(rel)
			// could not tell which of the two it was told about.
			//
			// One endpoint named twice is a different thing and is
			// allowed. Two controls can be two affordances onto a single
			// API operation -- adding an input to a credential type and
			// removing one are both its set-inputs endpoint -- and there
			// the single answer Can(rel) gives is not ambiguous but
			// correct, because the caller either may set that document or
			// may not. Refusing it forced a second endpoint to exist for
			// no reason but this check, which is a relation invented to
			// satisfy a validator rather than to describe the API.
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
