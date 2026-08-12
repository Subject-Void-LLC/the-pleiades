package view

import (
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
)

// The models below are what a template receives. Each one is fully
// resolved before rendering starts: every URL built, every option list
// fetched, every affordance decided. A template that had to call back into
// a port to finish rendering would be a template making decisions, and the
// whole reason this package holds no logic is that a decision inside a
// template is a branch no coverage tool ever reports on.

// ListModel is one page of a resource.
type ListModel struct {
	Page       PageModel
	Descriptor Descriptor
	Rows       []Row
	NextCursor string
	Aff        Affordances

	// Sections are the related-record tables rendered beneath the list,
	// already loaded. On a collection page they hang off the collection
	// rather than any row, which is what the dashboard's operator notices
	// are.
	Sections []LoadedSection

	// RefreshURL is where a live region re-requests itself, carrying the
	// narrowing the reader applied. Empty means this list does not refresh,
	// which is how the handler declines to disturb a reader who has paged
	// forward.
	RefreshURL string

	// Chart holds the aggregates when this resource declares a chart,
	// resolved before rendering so the table equivalent is real HTML
	// rather than something a script has to arrive to produce. A reader
	// with no JavaScript, or a script that failed to load, still gets the
	// numbers.
	Chart ChartData
}

// HasChart reports whether the chart section renders.
func (m ListModel) HasChart() bool { return m.Descriptor.Chart != nil }

// ChartTitle and ChartCaption are the chart's heading and its long
// description, both required at registration.
func (m ListModel) ChartTitle() string {
	if m.Descriptor.Chart == nil {
		return ""
	}
	return m.Descriptor.Chart.Title
}

func (m ListModel) ChartCaption() string {
	if m.Descriptor.Chart == nil {
		return ""
	}
	return m.Descriptor.Chart.Caption
}

// ChartDataHref is where the script fetches this chart's aggregates.
//
// Derived from the mount prefix and the resource name rather than declared
// by the resource, so a view never hardcodes a URL that belongs to the
// composition root.
func (m ListModel) ChartDataHref() string {
	return path.Join(m.Page.Prefix, m.Descriptor.Name, "chart.json")
}

// ChartBucketClass keeps a bucket's colour inside the validated set, for
// the same reason a badge's does: a class attribute is the one place a
// caller-controlled string could reach the stylesheet.
func (m ListModel) ChartBucketClass(b ChartBucket) string {
	if !ValidBadgeClasses[b.Class] {
		return "badge-neutral"
	}
	return b.Class
}

// ChartCount renders a bucket's count, and ChartTotal the sum, so the
// template performs no arithmetic.
func (m ListModel) ChartCount(b ChartBucket) string { return strconv.Itoa(b.Count) }
func (m ListModel) ChartTotal() string              { return strconv.Itoa(m.Chart.Total()) }

// Columns are the fields this list renders, in declaration order.
func (m ListModel) Columns() []Field { return m.Descriptor.ListFields() }

// Cell renders one row's value for a field.
//
// Formatting lives here rather than in the template for the same reason
// the models are pre-resolved: a template switching on a value's shape is
// a template making a decision.
func (m ListModel) Cell(row Row, f Field) string { return row.Cells[f.Name] }

// BadgeClass resolves a badge field's CSS class, refusing anything outside
// the validated set. A caller-controlled string reaching a class attribute
// is exactly what the content security policy exists to make impossible,
// so an unrecognized value degrades to the neutral badge rather than being
// interpolated.
func (m ListModel) BadgeClass(row Row, f Field) string {
	if f.BadgeClass == nil {
		return "badge-neutral"
	}
	class := f.BadgeClass(row.Cells[f.Name])
	if !ValidBadgeClasses[class] {
		return "badge-neutral"
	}
	return class
}

// IsPrimary reports whether a field is the one a narrow viewport promotes
// to each card's heading.
func (m ListModel) IsPrimary(f Field) string {
	if f.MobilePrimary {
		return "true"
	}
	return "false"
}

// DetailHref is the link to one record.
func (m ListModel) DetailHref(row Row) string {
	return path.Join(m.Page.Prefix, m.Descriptor.Name, url.PathEscape(row.ID))
}

// RefHref is the link to the record a referencing cell points at, empty
// when the field references nothing or the target no longer exists.
//
// Built from the registered view's own name and the stored target id, never
// from anything an author supplied per row, which is the same argument
// ChartSpec.Data already makes about author-supplied hrefs.
func (m ListModel) RefHref(row Row, f Field) string {
	if !f.Referencing() {
		return ""
	}
	id := row.Ref(f.Name)
	if id == "" {
		return ""
	}
	// A cell with no text must not become a link. An anchor whose content
	// is empty has no accessible name and is announced as its URL, which
	// is worse than the plain cell it replaced. This is the last line of
	// defence: the projector is supposed to supply a name, and the
	// conformance suite fails it for not doing so, but neither of those
	// should be what stands between a missing name and a nameless link.
	if strings.TrimSpace(row.Cells[f.Name]) == "" {
		return ""
	}
	return path.Join(m.Page.Prefix, f.References, url.PathEscape(id))
}

// CreateHref is the link to the create form.
func (m ListModel) CreateHref() string {
	return path.Join(m.Page.Prefix, m.Descriptor.Name, "new")
}

// NextHref is the link to the following page, empty when this is the last.
func (m ListModel) NextHref() string {
	if m.NextCursor == "" {
		return ""
	}
	q := url.Values{"after": {m.NextCursor}}
	return path.Join(m.Page.Prefix, m.Descriptor.Name) + "?" + q.Encode()
}

// CanCreate reports whether the create control renders for this caller.
//
// The relation comes from the endpoint the descriptor names rather than
// being assumed to be auth.RelCreate. Not every act of creation is called
// "create": dispatching a runbook creates a job and its endpoint declares
// auth.RelExecute, and hardcoding the relation here would have silently
// hidden that button from everyone, with the permission check passing and
// the control simply never appearing.
func (m ListModel) CanCreate() bool { return permits(m.Descriptor.Ops.Create, m.Aff) }

// permits is the one place an operation's endpoint is turned into a yes or
// no, so no model reimplements the "is it offered, and is it permitted"
// pair or invents a second opinion about which relation to ask about.
func permits(endpoint *apispec.Endpoint, aff Affordances) bool {
	return endpoint != nil && aff.Can(endpoint.Rel)
}

// ResultCount is what the live region announces after an HTMX swap, so a
// screen reader user learns the list changed and by how much.
func (m ListModel) ResultCount() string {
	return strconv.Itoa(len(m.Rows)) + " results"
}

// DetailModel is one record.
type DetailModel struct {
	Page       PageModel
	Descriptor Descriptor
	Row        Row
	Aff        Affordances

	// Sections are the related-record tables, already loaded. Resolved
	// before rendering like everything else here, so no template performs
	// I/O and a section that failed to load is a decision the handler
	// already made rather than one the template discovers halfway down
	// the page.
	Sections []LoadedSection
}

// LoadedSection is one Section with its rows in hand.
type LoadedSection struct {
	Spec Section
	Rows []Row
}

// ID is the section's DOM identifier, derived from its title so the
// heading can be referenced by aria-labelledby. Titles are unique per
// descriptor (Register enforces it), which is what makes this collision
// free.
func (s LoadedSection) ID() string {
	slug := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return '-'
		}
	}, s.Spec.Title)
	return "section-" + slug
}

// HeadingID is the id the section's <h2> carries.
func (s LoadedSection) HeadingID() string { return s.ID() + "-heading" }

// Columns are the section's fields marked for listing.
func (s LoadedSection) Columns() []Field { return filterFields(s.Spec.Fields, Field.listed) }

// Cell renders one value.
func (s LoadedSection) Cell(row Row, f Field) string { return row.Cells[f.Name] }

// BadgeClass keeps a section's badges inside the validated set, exactly as
// the main list does. A section is not a lesser table.
func (s LoadedSection) BadgeClass(row Row, f Field) string {
	if f.BadgeClass == nil {
		return "badge-neutral"
	}
	class := f.BadgeClass(row.Cells[f.Name])
	if !ValidBadgeClasses[class] {
		return "badge-neutral"
	}
	return class
}

// IsPrimary reports whether a field is the one a narrow viewport promotes
// to each card's heading.
func (s LoadedSection) IsPrimary(f Field) string {
	if f.MobilePrimary {
		return "true"
	}
	return "false"
}

// HasRows reports whether this section has anything to show.
func (s LoadedSection) HasRows() bool { return len(s.Rows) > 0 }

// Fields are every field with a value to show, in declaration order.
func (m DetailModel) Fields() []Field { return m.Descriptor.Fields }

// Value renders one field for this record.
func (m DetailModel) Value(f Field) string { return m.Row.Cells[f.Name] }

// EditHref is the link to this record's edit form.
func (m DetailModel) EditHref() string {
	return path.Join(m.Page.Prefix, m.Descriptor.Name, url.PathEscape(m.Row.ID), "edit")
}

// SelfHref is this record's own URL, which the delete control posts to.
func (m DetailModel) SelfHref() string {
	return path.Join(m.Page.Prefix, m.Descriptor.Name, url.PathEscape(m.Row.ID))
}

// ListHref is the link back to the collection.
func (m DetailModel) ListHref() string {
	return path.Join(m.Page.Prefix, m.Descriptor.Name)
}

// CanEdit and CanDelete gate their controls on the same permitted set the
// JSON API's _links array is computed from, intersected with whatever the
// record's own state allows.
func (m DetailModel) CanEdit() bool {
	return permits(m.Descriptor.Ops.Update, m.Aff) && m.applies(auth.RelUpdate)
}

func (m DetailModel) CanDelete() bool {
	return permits(m.Descriptor.Ops.Delete, m.Aff) && m.applies(auth.RelDelete)
}

func (m DetailModel) applies(rel auth.LinkRel) bool {
	if m.Descriptor.Applies == nil {
		return true
	}
	return m.Descriptor.Applies(m.Row, rel)
}

// Actions are the record actions this caller may exercise, already
// filtered. Same permitted set the JSON API's _links is computed from, and
// the same per-row Applies filter, so an action is offered on exactly the
// records it would actually work on.
func (m DetailModel) Actions() []RecordActionLink {
	out := make([]RecordActionLink, 0, len(m.Descriptor.Actions))
	for _, a := range m.Descriptor.Actions {
		if !permits(a.Endpoint, m.Aff) || !m.applies(a.Endpoint.Rel) {
			continue
		}
		out = append(out, RecordActionLink{
			Label: a.Label,
			Href:  path.Join(m.Page.Prefix, m.Descriptor.Name, url.PathEscape(m.Row.ID), a.Name),
		})
	}
	return out
}

// RecordActionLink is one rendered action control.
type RecordActionLink struct {
	Label string
	Href  string
}

// CanStream reports whether this record offers a live log stream, which is
// a property of the resource rather than of the caller: the stream endpoint
// enforces its own scope, so gating the link on authorization here would
// mean two places deciding one question.
func (m DetailModel) CanStream() bool { return m.Descriptor.Stream != nil }

// LogsHref is the link to this record's live log page.
func (m DetailModel) LogsHref() string {
	return path.Join(m.Page.Prefix, m.Descriptor.Name, url.PathEscape(m.Row.ID), "logs")
}

// StreamModel is one record's live log view.
//
// It is the one page designed for a phone first rather than adapted to one:
// watching a running job is the thing somebody is most likely to open away
// from a desk, usually because something is already going wrong.
type StreamModel struct {
	Page       PageModel
	Descriptor Descriptor
	// ID is the record whose stream this is.
	ID string
}

// Heading names the record being watched.
func (m StreamModel) Heading() string {
	if m.Descriptor.Stream == nil {
		return m.ID
	}
	return m.Descriptor.Stream.Title + ": " + m.ID
}

// StreamURL is the server-sent-events endpoint this page connects to.
//
// It is same-origin by construction -- StreamSpec refuses a pattern that is
// not a rooted path -- which is what lets the content security policy keep
// connect-src at 'self', and what makes the session cookie reach it. An
// EventSource cannot set an Authorization header, so cookie authentication
// is not a convenience here; it is the only reason this page can exist.
func (m StreamModel) StreamURL() string {
	if m.Descriptor.Stream == nil {
		return ""
	}
	return m.Descriptor.Stream.StreamPath(m.ID)
}

// BackHref returns to the record this stream belongs to.
func (m StreamModel) BackHref() string {
	return path.Join(m.Page.Prefix, m.Descriptor.Name, url.PathEscape(m.ID))
}

// FormModel is a create or edit form.
type FormModel struct {
	Page       PageModel
	Descriptor Descriptor

	// ID is empty for a create form. It is what decides where the form
	// posts and what it is called, so there is no separate "mode" field
	// that could disagree with it.
	ID string

	// Values prefill the controls: the submitted values on a failed
	// submission, the stored values on an edit, empty on a create. A
	// failed submission redisplays what the user typed rather than
	// clearing it, because making someone retype a form to see what was
	// wrong with it is its own accessibility problem.
	Values map[string]string

	// Errors are the per-field problems, if this is a redisplay.
	Errors FieldErrors

	// Options holds resolved select choices, fetched before rendering so
	// no template performs I/O.
	Options map[string][]Option
}

// Fields are the writable fields, in declaration order.
func (m FormModel) Fields() []Field { return m.Descriptor.FormFields() }

// Editing reports whether this form updates an existing record.
func (m FormModel) Editing() bool { return m.ID != "" }

// Heading names the operation in words.
func (m FormModel) Heading() string {
	if m.Editing() {
		return "Edit " + m.Descriptor.Title
	}
	return "New " + m.Descriptor.Title
}

// Action is where the form posts. Both operations post rather than one
// posting and one putting, because a browser form supports exactly two
// methods and the UI must work with scripting disabled.
func (m FormModel) Action() string {
	if m.Editing() {
		return path.Join(m.Page.Prefix, m.Descriptor.Name, url.PathEscape(m.ID))
	}
	return path.Join(m.Page.Prefix, m.Descriptor.Name)
}

// CancelHref returns to wherever the form was entered from.
func (m FormModel) CancelHref() string {
	if m.Editing() {
		return path.Join(m.Page.Prefix, m.Descriptor.Name, url.PathEscape(m.ID))
	}
	return path.Join(m.Page.Prefix, m.Descriptor.Name)
}

// Value is the current value for a control.
func (m FormModel) Value(f Field) string { return m.Values[f.Name] }

// Choices are a select or lookup field's resolved options.
func (m FormModel) Choices(f Field) []Option { return m.Options[f.Name] }

// IsSelected reports whether one option of a KindLookup field is currently
// chosen.
//
// The current values arrive through the same map[string]string every other
// field uses, comma-separated, rather than by widening the Projector's Form
// signature to map[string][]string. That would be the tidier type and it
// would touch every registered view to gain one field's worth of
// expressiveness, so the encoding stays local to the two ends that care:
// the projector joins, this splits.
func (m FormModel) IsSelected(f Field, value string) bool {
	for _, chosen := range strings.Split(m.Values[f.Name], ",") {
		if strings.TrimSpace(chosen) == value {
			return true
		}
	}
	return false
}

// HasErrors reports whether the error summary renders.
func (m FormModel) HasErrors() bool { return m.Errors.Any() }

// Summary is the ordered error list the summary renders and focuses.
func (m FormModel) Summary() []SummaryError { return m.Errors.Summary(m.Descriptor.Fields) }

// FieldErrorsFor returns one field's problems.
func (m FormModel) FieldErrorsFor(f Field) []string { return m.Errors[f.Name] }

// Invalid is the literal aria-invalid value for a control.
func (m FormModel) Invalid(f Field) string {
	if len(m.Errors[f.Name]) > 0 {
		return "true"
	}
	return "false"
}

// ControlID is a field's DOM id, used by its label's for attribute.
func (m FormModel) ControlID(f Field) string { return "f-" + f.Name }

// HintID is a field's hint element id.
func (m FormModel) HintID(f Field) string { return "f-" + f.Name + "-hint" }

// ErrorID is a field's error element id.
func (m FormModel) ErrorID(f Field) string { return "f-" + f.Name + "-error" }

// DescribedBy wires a control to its hint and its error message, so both
// are announced with the field rather than orphaned beside it. An empty
// result means the attribute is omitted entirely, which is correct:
// aria-describedby pointing at nothing is worse than absent, because a
// screen reader announces the dangling reference.
func (m FormModel) DescribedBy(f Field) string {
	var ids []string
	if f.Help != "" {
		ids = append(ids, m.HintID(f))
	}
	if len(m.Errors[f.Name]) > 0 {
		ids = append(ids, m.ErrorID(f))
	}
	switch len(ids) {
	case 0:
		return ""
	case 1:
		return ids[0]
	default:
		return ids[0] + " " + ids[1]
	}
}

// MaxLen renders a field's maxlength attribute, empty when unbounded.
func (m FormModel) MaxLen(f Field) string {
	if f.MaxLen <= 0 {
		return ""
	}
	return strconv.Itoa(f.MaxLen)
}

// ActionModel is a record action's prompt form.
//
// It reuses FormModel's field rendering wholesale rather than growing a
// second form template: the controls, the labels, the hints, the error
// summary and the accessibility wiring are all the same, and a second copy
// of them would be a second copy to keep accessible.
type ActionModel struct {
	Page       PageModel
	Descriptor Descriptor
	Action     RecordAction

	// ID is the record this action runs against.
	ID string

	// Fields is the prompt as resolved for this record, which is not
	// always the action's own declaration: a launch form renders only the
	// fields the template being launched actually opened.
	Fields []Field

	Values map[string]string
	Errors FieldErrors

	// Options holds resolved select choices, fetched before rendering so
	// no template performs I/O.
	Options map[string][]Option
}

// Form projects this action onto the shared form model, so one template
// renders both.
func (m ActionModel) Form() FormModel {
	return FormModel{
		Page: m.Page,
		// A descriptor carrying the action's fields rather than the
		// resource's, because the form is about the action: running a
		// runbook prompts for a target group, not for the runbook's own
		// columns.
		Descriptor: Descriptor{
			Name:  m.Descriptor.Name,
			Title: m.Action.Heading,
			// The resolved set, not the declaration: an action whose
			// controls differ per record renders the record's own.
			Fields: m.Fields,
		},
		Values:  m.Values,
		Errors:  m.Errors,
		Options: m.Options,
	}
}

// Heading names the action and the record it will run against.
func (m ActionModel) Heading() string { return m.Action.Heading + ": " + m.ID }

// SubmitLabel is the button text, the action's own label rather than
// "Save": what this does is run something, not store something.
func (m ActionModel) SubmitLabel() string { return m.Action.Label }

// Action is where the form posts, and CancelHref returns to the record.
func (m ActionModel) ActionHref() string {
	return path.Join(m.Page.Prefix, m.Descriptor.Name, url.PathEscape(m.ID), m.Action.Name)
}

func (m ActionModel) CancelHref() string {
	return path.Join(m.Page.Prefix, m.Descriptor.Name, url.PathEscape(m.ID))
}

// DeclaredModel is the honest panel a StatusDeclared view renders.
//
// It exists so a declared view needs no per-view work at all: the shared
// template renders this from Status alone, which is the same guardrail
// pkg/collection applies when it refuses to let a declared method pretend
// to be implemented.
type DeclaredModel struct {
	Page       PageModel
	Descriptor Descriptor
	// Reason says what specifically is missing, so the panel is useful
	// rather than merely apologetic.
	Reason string
}

// refreshAttrs is the shared rendering of a live region's polling contract,
// so a list and a record page cannot disagree about how a refresh is
// requested or about when one stops.
type refreshAttrs struct {
	spec *RefreshSpec
	href string
	row  Row
	// hasRow distinguishes a record page, where Active is consulted, from a
	// collection page, where there is no row to consult it about.
	hasRow bool
}

// polls reports whether the region should carry a trigger at all.
//
// An empty href means "do not refresh this one", and it is load-bearing
// rather than defensive: a list the reader has paged forward through must
// not be replaced by page one every few seconds. The handler expresses that
// by declining to build a URL, and this is the only place that decision is
// read, so there is no default to fall back to and quietly re-enable it.
func (a refreshAttrs) polls() bool {
	if a.spec == nil || a.href == "" {
		return false
	}
	if a.hasRow && a.spec.Active != nil {
		return a.spec.Active(a.row)
	}
	return true
}

// trigger is the literal HTMX trigger expression, empty when this region
// does not poll.
//
// Rendered in whole seconds because that is the unit the expression takes
// and because Register refuses anything under a second, so the truncation
// can never round an interval down to zero.
func (a refreshAttrs) trigger() string {
	if !a.polls() {
		return ""
	}
	return "every " + strconv.Itoa(int(a.spec.Interval.Seconds())) + "s"
}

// Polls reports whether this list keeps itself current.
func (m ListModel) Polls() bool { return m.refresh().polls() }

// RefreshTrigger is the HTMX trigger expression for this list, empty when
// it does not refresh.
func (m ListModel) RefreshTrigger() string { return m.refresh().trigger() }

// RefreshHref is the URL the region re-requests.
//
// It is the collection's own URL rather than a separate endpoint, because
// the refresh is the same read the page already performed. RefreshURL is
// set by the handler from the query values it actually parsed, so a
// refreshed list keeps whatever narrowing the reader applied instead of
// silently widening back to everything on the next tick.
func (m ListModel) RefreshHref() string {
	return m.refresh().href
}

func (m ListModel) refresh() refreshAttrs {
	return refreshAttrs{spec: m.Descriptor.Refresh, href: m.RefreshURL}
}

// Polls reports whether this record keeps itself current. A record that has
// reached a terminal state stops, which is what makes a finished job cost
// nothing to leave open.
func (m DetailModel) Polls() bool { return m.refresh().polls() }

// RefreshTrigger is the HTMX trigger expression for this record.
func (m DetailModel) RefreshTrigger() string { return m.refresh().trigger() }

// RefreshHref is the record's own URL.
func (m DetailModel) RefreshHref() string { return m.refresh().href }

func (m DetailModel) refresh() refreshAttrs {
	return refreshAttrs{
		spec:   m.Descriptor.Refresh,
		href:   m.SelfHref(),
		row:    m.Row,
		hasRow: true,
	}
}

// RefreshAnnouncement is what a screen reader is told after a live update,
// and it is deliberately the record's own identity plus its most telling
// field rather than a fixed string.
//
// A polled region that announced the same words every tick would be read out
// endlessly; app.js therefore announces only when this value changes, which
// makes the sentence itself the change detector. Composing it from the badge
// fields is what makes "job-7 failed" arrive the moment it becomes true.
func (m DetailModel) RefreshAnnouncement() string {
	parts := make([]string, 0, 3)
	parts = append(parts, m.Row.ID)
	for _, f := range m.Descriptor.Fields {
		if f.Kind == KindBadge {
			if v := m.Row.Cells[f.Name]; v != "" {
				parts = append(parts, v)
			}
		}
	}
	return strings.Join(parts, " ")
}
