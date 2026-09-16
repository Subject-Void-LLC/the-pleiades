// Package view's table and zero state: the two shapes every region in this UI
// is made of.
//
// A collection's rows, a record's related rows and a declared region's future
// columns are one component here, and the four different emptinesses a region
// can be in are one type. Both replaced near-identical copies that had already
// drifted apart.
package view

import (
	"net/url"
	"path"
	"strconv"
	"strings"
)

// TableModel is every table this UI draws.
//
// A collection's rows and a record's related rows were two nearly identical
// blocks of template: the same header loop, the same data-label attributes,
// the same badge rendering, the same mobile-card treatment. Nearly identical
// is the problem -- the section copy was missing reference links, so a job's
// device outcome could name a device and not reach it, and nobody noticed
// because the two copies were never read side by side.
//
// One model, one component, two builders. A table gains a capability once.
type TableModel struct {
	// Prefix is the UI mount point, needed to build any link out of a cell.
	Prefix string

	// Columns are the fields this table renders, already filtered.
	Columns []Field

	// Rows are the records, already loaded.
	Rows []Row

	// RecordView is the view name a row links to, empty when these rows are
	// not records of any view. A section's rows are the usual empty case:
	// they are a projection of something else, and it is their referencing
	// cells that lead anywhere, not the row itself.
	RecordView string

	// Empty is what renders in place of the table when there are no rows.
	// Never blank: see Descriptor.Empty for why an empty table is the one
	// rendering that must not be silent.
	Empty ZeroState

	// Caption describes the table for assistive technology when the
	// surrounding heading does not. Empty renders no caption element.
	Caption string

	// CSRFToken is this request's token, carried because a row control is
	// a form submission and every form submission in this UI presents one.
	// Empty on every table that renders no controls.
	CSRFToken string

	// RowControls are the per-row controls, one slice per row, parallel to
	// Rows and either empty or exactly as long.
	//
	// Parallel to Rows rather than keyed by row id, because a section's row
	// ids are only unique by convention: they are whatever the section's
	// projection put there, and a map would silently collapse two rows that
	// happened to agree. An index cannot.
	//
	// Only a section under a record ever fills this in. A collection page
	// has no record for a row control to hang off, and the main list's rows
	// are records with their own pages rather than elements of a document.
	RowControls [][]RowControl

	// Preview renders the column headers even though there are no rows,
	// with the zero state beneath them.
	//
	// It is how a declared-but-unimplemented region shows its future shape
	// rather than only apologising for not having one. "Notifications is
	// not built" tells a reader nothing they can plan around; "Notifications
	// will list TARGET and ON, and is not built" tells them what it is going
	// to be and lets them say whether that is the right answer before
	// anybody writes it.
	Preview bool
}

// ShowsColumns reports whether the header row renders: either there are rows
// under it, or this is a preview of the columns there will be.
func (t TableModel) ShowsColumns() bool {
	return (t.HasRows() || t.Preview) && len(t.Columns) > 0
}

// ColumnSpan is the header row's width, for the cell the zero state sits in
// when a preview has no rows to fill it. The controls column counts: a
// zero state that stopped short of the table's real width leaves a stray
// empty cell beside it.
func (t TableModel) ColumnSpan() string {
	span := len(t.Columns)
	if t.HasRowControls() {
		span++
	}
	return strconv.Itoa(span)
}

// HasRowControls reports whether this table renders a trailing controls
// column at all, so a table with none draws no empty header cell for one.
func (t TableModel) HasRowControls() bool {
	for _, controls := range t.RowControls {
		if len(controls) > 0 {
			return true
		}
	}
	return false
}

// ControlsAt is one row's controls, by the row's position in Rows.
//
// Bounds checked rather than assumed. The two slices are built together and
// should always agree, but a template that ranged past the end would panic
// inside a render, which answers a page with a blank screen.
func (t TableModel) ControlsAt(i int) []RowControl {
	if i < 0 || i >= len(t.RowControls) {
		return nil
	}
	return t.RowControls[i]
}

// HasRows reports whether the table renders at all, as against its zero
// state.
func (t TableModel) HasRows() bool { return len(t.Rows) > 0 }

// Captioned reports whether a caption element renders.
func (t TableModel) Captioned() bool { return t.Caption != "" }

// Cell renders one value.
func (t TableModel) Cell(row Row, f Field) string { return row.Cells[f.Name] }

// BadgeClass resolves a badge cell's class through the one validated
// implementation.
func (t TableModel) BadgeClass(row Row, f Field) string {
	return BadgeClassFor(f, row.Cells[f.Name])
}

// IsPrimary reports whether a field is the one a narrow viewport promotes to
// each card's heading. A string because it is rendered as an attribute value.
func (t TableModel) IsPrimary(f Field) string {
	if f.MobilePrimary {
		return "true"
	}
	return "false"
}

// RowHref is the link to the record this row is, empty when its rows are not
// records of a view.
func (t TableModel) RowHref(row Row) string {
	if t.RecordView == "" || row.ID == "" {
		return ""
	}
	return path.Join(t.Prefix, t.RecordView, url.PathEscape(row.ID))
}

// Linkable reports whether rows in this table lead anywhere on their own.
func (t TableModel) Linkable() bool { return t.RecordView != "" }

// RefHref is the link to the record a referencing cell points at, empty when
// the field references nothing or the cell has no text to be a link.
//
// Built from the registered view's own name and the stored target id, never
// from anything an author supplied per row. A cell with no text must not
// become a link: an anchor with no content has no accessible name and is
// announced as its URL, which is worse than the plain cell it replaced.
func (t TableModel) RefHref(row Row, f Field) string {
	if !f.Referencing() {
		return ""
	}
	id := row.Ref(f.Name)
	if id == "" {
		return ""
	}
	if strings.TrimSpace(row.Cells[f.Name]) == "" {
		return ""
	}
	return path.Join(t.Prefix, f.References, url.PathEscape(id))
}

// CellHref is the one link decision a cell makes, so the template asks once
// rather than branching over two link kinds in the order it happens to think
// of them.
//
// A referencing cell links to what it names. Otherwise the primary cell of a
// linkable row links to that row, which is what makes a record's name the way
// into it rather than a separate Open column at the far right of a table
// somebody has to scroll sideways to reach.
//
// It pairs with CellText, and the two must agree: a link is only ever offered
// where CellText guarantees there is something to name it. A record whose
// primary cell is empty still has to be reachable -- that is what the
// identifier fallback there is for -- so this asks whether the row is
// linkable, not whether the cell happens to be populated.
func (t TableModel) CellHref(row Row, f Field) string {
	if href := t.RefHref(row, f); href != "" {
		return href
	}
	if f.MobilePrimary && t.Linkable() && t.CellText(row, f) != "" {
		return t.RowHref(row)
	}
	return ""
}

// CellText is what a cell displays, which is its value except in the one case
// where a value is missing and something has to stand in for it.
//
// A record's primary cell is the way into that record now that the trailing
// Open column is gone. An empty one would be a row nobody can open and, worse,
// an anchor with no content: announced as its own URL, which is how a link
// ends up with no accessible name. The identifier is the honest stand-in --
// it is what the record is addressed by, it is never empty, and it is more
// use to somebody than a blank cell was.
func (t TableModel) CellText(row Row, f Field) string {
	value := row.Cells[f.Name]
	if strings.TrimSpace(value) != "" {
		return value
	}
	if f.MobilePrimary && t.Linkable() {
		return row.ID
	}
	return value
}

// ZeroState is what a region with nothing in it says.
//
// Four different facts render as an empty region -- nothing exists yet, a
// filter excluded everything, this was never built, and authorization could
// not be evaluated -- and three of them used to render identically. Naming
// which one this is, and offering the control that resolves it, is the whole
// type.
type ZeroState struct {
	// Heading is the fact, in a few words.
	Heading string

	// Body explains it. One or two sentences; a paragraph here is a sign
	// the page needed a different design rather than a longer message.
	Body string

	// Actions are what resolves it, usually exactly one.
	Actions []ChromeAction

	// Tone selects the rendering. Empty is the ordinary quiet one.
	Tone ZeroTone
}

// ZeroTone is how a zero state renders.
type ZeroTone string

// The tones. Each maps to one class, so a template chooses nothing.
const (
	// ZoneQuiet is an ordinary absence: nothing here yet, nothing matched.
	ZoneQuiet ZeroTone = ""

	// ZoneDeclared is a view or section registered with no implementation
	// behind it. It reads as deliberate rather than broken.
	ZoneDeclared ZeroTone = "declared"

	// ZoneProblem is something that went wrong, most often authorization
	// that could not be evaluated. It is never used for mere absence.
	ZoneProblem ZeroTone = "problem"
)

// Class is the rendered class attribute for this zero state.
func (z ZeroState) Class() string {
	switch z.Tone {
	case ZoneDeclared:
		return "zero zero-declared"
	case ZoneProblem:
		return "zero zero-problem"
	default:
		return "zero"
	}
}

// HasActions reports whether the action row renders.
func (z ZeroState) HasActions() bool { return len(z.Actions) > 0 }

// Table is the collection's rows as the one table component wants them.
func (m ListModel) Table() TableModel {
	return TableModel{
		Prefix:     m.Page.Prefix,
		Columns:    m.Columns(),
		Rows:       m.Rows,
		RecordView: m.Descriptor.Name,
		Empty:      m.ZeroState(),
		Caption:    m.Descriptor.Title,
	}
}

// ZeroState is what this list says when it has no rows.
//
// It distinguishes the two absences a collection can be in, because they call
// for opposite responses: a page past the end wants to go back to the start,
// and a genuinely empty collection wants its first record created. Offering
// either control to the other reader is offering something that does not help.
func (m ListModel) ZeroState() ZeroState {
	if m.Paged() {
		return ZeroState{
			Heading: "Nothing on this page",
			Body: "This is past the end of " + strings.ToLower(m.Descriptor.Title) +
				". The link that brought you here may have been made when there were more.",
			Actions: []ChromeAction{{
				Label: "Back to the first page",
				Href:  path.Join(m.Page.Prefix, m.Descriptor.Name),
				Kind:  ActionNormal,
			}},
		}
	}

	z := ZeroState{Heading: "Nothing here yet", Body: m.Descriptor.EmptyText()}
	if m.CanCreate() {
		z.Actions = append(z.Actions, ChromeAction{
			Label: m.CreateLabel(),
			Href:  m.CreateHref(),
			Kind:  ActionPrimary,
		})
	}
	return z
}

// Paged reports whether this is a continuation rather than the first page.
func (m ListModel) Paged() bool { return m.Cursor != "" }

// RowControl is one resolved row action: what to draw, where it posts, and
// what it asks first.
//
// A finished control rather than a declaration, for the reason ChromeAction
// is: the resolver is the one place holding the parent id, the prefix and
// the caller's permitted set, so the template renders what it is handed and
// decides nothing.
type RowControl struct {
	// Label is the button text.
	Label string

	// Href is the POST target. A row control is always a form submission,
	// never a link: it changes state, and a GET that changes state is one
	// a link prefetcher or a corporate scanner eventually runs.
	Href string

	// Confirm is the dialog's question, empty when the control posts
	// straight through.
	Confirm string

	// DialogID is the element id the confirmation dialog carries, unique
	// within the page. Empty when there is no dialog.
	DialogID string

	// Prompts says this control opens a form rather than acting at once,
	// which makes it the one row control that is a LINK.
	//
	// Every other one posts, because it changes state and a GET that
	// changes state is one a prefetcher eventually runs. This one changes
	// nothing until its form is submitted, and the form posts back to the
	// same address, so the GET is safe and is the only way to reach a page
	// that has to be drawn before anything happens.
	Prompts bool
}

// Confirms reports whether this control opens a dialog before posting.
func (c RowControl) Confirms() bool { return c.DialogID != "" }

// SectionView is one section together with the context it needs to render.
//
// LoadedSection is what a handler produces and knows nothing about URLs; this
// is what a template consumes. Keeping them apart is what lets a section be
// loaded without a page model in hand, which is what the fragment handlers do.
type SectionView struct {
	LoadedSection

	// Prefix is the UI mount point, for the links inside this section's
	// cells.
	Prefix string

	// Actions are this section's header controls, already resolved to a
	// label and a link acting on the record the section hangs off. Empty
	// on a collection page, where there is no such record: the resolver
	// that fills this in is the one place that knows the parent id, so the
	// template renders whatever it is handed and decides nothing.
	Actions []ChromeAction

	// RowControls are this section's per-row controls, resolved by the
	// same resolver and parallel to Rows.
	RowControls [][]RowControl

	// CSRFToken is this request's token, for the forms those controls are.
	CSRFToken string
}

// HasActions reports whether this section renders any header control, so
// the template omits the header row entirely rather than drawing an empty
// one.
func (s SectionView) HasActions() bool { return len(s.Actions) > 0 }

// Table is this section's rows as the one table component wants them.
func (s SectionView) Table() TableModel {
	return TableModel{
		Prefix:  s.Prefix,
		Columns: s.Columns(),
		Rows:    s.Rows,
		// A declared section shows the columns it is going to have. The
		// fields are already in the declaration, so the shape costs
		// nothing to render and is the difference between a skeleton
		// somebody can review and a panel they can only accept.
		Preview: !s.Spec.Implemented(),
		// Deliberately empty. A section's rows are a projection of
		// something else -- a job's per-device outcome is not a record of
		// the Jobs view -- so the row as a whole leads nowhere and it is
		// the referencing cells inside it that do.
		RecordView:  "",
		Empty:       s.ZeroState(),
		Caption:     s.Spec.Title,
		RowControls: s.RowControls,
		CSRFToken:   s.CSRFToken,
	}
}

// ZeroState is what this section says when it has nothing to show.
func (s SectionView) ZeroState() ZeroState {
	if !s.Spec.Implemented() {
		return ZeroState{
			Heading: "Declared, not implemented",
			Body:    s.Spec.Empty,
			Tone:    ZoneDeclared,
		}
	}
	return ZeroState{Heading: "Nothing to show", Body: s.Spec.Empty}
}

// SectionViews wraps this record's visible sections for rendering.
//
// This is the one place a section's header actions are resolved, because it
// is the one place that has both the parent record's id and the descriptor
// whose Actions the section named. The template is handed finished links.
func (m DetailModel) SectionViews() []SectionView {
	visible := m.VisibleSections()
	out := make([]SectionView, 0, len(visible))
	for _, s := range visible {
		out = append(out, SectionView{
			LoadedSection: s,
			Prefix:        m.Page.Prefix,
			Actions:       m.Descriptor.sectionActions(s.Spec, m.Page.Prefix, m.Row, m.Aff),
			RowControls:   m.Descriptor.rowControls(s, m.Page.Prefix, m.Row, m.Aff),
			CSRFToken:     m.Page.CSRFToken,
		})
	}
	return out
}

// sectionActions resolves a section's named header actions into rendered
// controls acting on the parent record. It returns none when that record
// has no id -- the collection page has no record for a section action to
// act on -- and none for a name matching no declared action, which Register
// has already refused, so this stays a lookup rather than a second
// validation.
//
// Each control passes the same two filters a record's own actions do: the
// permitted set the JSON _links array is computed from, and the record's
// own Applies state. A section is a write surface like any other, so
// offering "Add input" to a caller who cannot write, or on a managed type
// the store would refuse, is the same "control that can only fail" the
// affordance layer exists to withhold.
func (d Descriptor) sectionActions(spec Section, prefix string, parent Row, aff Affordances) []ChromeAction {
	if parent.ID == "" || len(spec.Actions) == 0 {
		return nil
	}
	out := make([]ChromeAction, 0, len(spec.Actions))
	for _, name := range spec.Actions {
		for _, a := range d.Actions {
			if a.Name != name {
				continue
			}
			if !permits(a.Endpoint, aff) {
				break
			}
			if d.Applies != nil && a.Endpoint != nil && !d.Applies(parent, a.Endpoint.Rel) {
				break
			}
			out = append(out, ChromeAction{
				Label: a.Label,
				Href:  path.Join(prefix, d.Name, url.PathEscape(parent.ID), a.Name),
				Kind:  ActionNormal,
			})
			break
		}
	}
	return out
}

// rowControls resolves one section's row actions for every row it loaded.
//
// Each control passes four filters, and the first three are the header
// half's own, evaluated once for the whole action rather than per row:
// there is a parent record to act on, the caller holds the relation the
// endpoint declares, and the parent record itself still qualifies. A
// managed credential type refuses every write, so withholding "Remove"
// from its rows is the same judgement as withholding "Add input" from its
// header, and reaching that judgement twice in two places is how the two
// drift apart.
//
// The fourth is the row's own, and it is the only one evaluated per row.
func (d Descriptor) rowControls(s LoadedSection, prefix string, parent Row, aff Affordances) [][]RowControl {
	if parent.ID == "" || len(s.Spec.RowActions) == 0 || len(s.Rows) == 0 {
		return nil
	}

	offered := make([]RowAction, 0, len(s.Spec.RowActions))
	for _, a := range s.Spec.RowActions {
		if !permits(a.Endpoint, aff) {
			continue
		}
		if d.Applies != nil && a.Endpoint != nil && !d.Applies(parent, a.Endpoint.Rel) {
			continue
		}
		offered = append(offered, a)
	}
	if len(offered) == 0 {
		return nil
	}

	out := make([][]RowControl, len(s.Rows))
	for i, row := range s.Rows {
		if row.ID == "" {
			// There is nothing to address. A control here would post to
			// the parent's own action route with a trailing empty
			// segment, which is a 404 at best.
			continue
		}
		// The position is over every row the section loaded, including any
		// skipped above for having no id. Counting only the addressable
		// ones would make the ordinal disagree with the order the table
		// renders, and a "move up" withheld from the wrong row is worse
		// than one withheld from none.
		at := RowPosition{Index: i, Count: len(s.Rows)}
		for _, a := range offered {
			if a.Applies != nil && !a.Applies(row, at) {
				continue
			}
			control := RowControl{
				Label:   a.Label,
				Href:    path.Join(prefix, d.Name, url.PathEscape(parent.ID), a.Name, url.PathEscape(row.ID)),
				Confirm: a.Confirm,
				Prompts: a.Prompts(),
			}
			if a.Confirms() {
				// The row's index rather than its id. An id is author
				// data and two of them can slug to the same string,
				// which would give two dialogs one element id and open
				// the wrong row's confirmation.
				control.DialogID = s.ID() + "-" + a.Name + "-" + strconv.Itoa(i)
			}
			out[i] = append(out[i], control)
		}
	}
	return out
}

// SectionViews wraps a collection page's sections for rendering.
//
// The dashboard is the only view that has them: it is a chart and its
// operator notices, with no table of its own.
func (m ListModel) SectionViews() []SectionView {
	out := make([]SectionView, 0, len(m.Sections))
	for _, s := range m.Sections {
		out = append(out, SectionView{LoadedSection: s, Prefix: m.Page.Prefix})
	}
	return out
}

// AffordanceUnknown is what a page says when authorization could not be
// evaluated.
//
// HTML has no way to express the JSON API's "the _links field is absent,
// which is not the same as empty", so the honest rendering is no action
// controls plus a visible explanation. Silently presenting a read-only page
// would repeat FAILURE_PATTERNS #73 in a new medium. It is a zero state like
// every other absence rather than a panel of its own, which is what it used
// to be: one shape, four tones, no fourth block of markup.
func AffordanceUnknown() ZeroState {
	return ZeroState{
		Heading: "Available actions could not be determined",
		Body: "The record is shown, but this control plane did not report what you may " +
			"do with it. No action controls are drawn, because drawing none and drawing " +
			"none because we could not ask look the same.",
		Tone: ZoneProblem,
	}
}

// ZeroState is what a declared view renders instead of a page.
//
// It says out loud that it is a skeleton rather than presenting an empty page
// that looks broken, which is the one absence the application already got
// right and the reason this tone exists at all.
func (m DeclaredModel) ZeroState() ZeroState {
	return ZeroState{
		Heading: "Declared, not implemented",
		Body:    m.Reason,
		Tone:    ZoneDeclared,
	}
}

// BadgeClass resolves one of a record's own values through the single
// validated implementation.
func (m DetailModel) BadgeClass(f Field) string {
	return BadgeClassFor(f, m.Row.Cells[f.Name])
}

// RefHref is the link to the record one of this record's fields names.
//
// The same rule the tables apply. Without it the detail list was the one
// place a reference rendered as plain text, so a walk down the hierarchy
// stopped at whichever record you opened first: a job named its template and
// could not reach it, a template named its inventory and could not reach it.
func (m DetailModel) RefHref(f Field) string {
	if !f.Referencing() {
		return ""
	}
	id := m.Row.Ref(f.Name)
	if id == "" {
		return ""
	}
	if strings.TrimSpace(m.Row.Cells[f.Name]) == "" {
		return ""
	}
	return path.Join(m.Page.Prefix, f.References, url.PathEscape(id))
}

// StreamURL is where the record's Output tab reads its live log from.
//
// The same path the standalone stream page uses, because they are the same
// stream: a record watched from its own page and one watched from a bookmark
// must not be able to behave differently.
func (m DetailModel) StreamURL() string {
	if m.Descriptor.Stream == nil {
		return ""
	}
	return m.Descriptor.Stream.StreamPath(m.Row.ID)
}

// Table is a declared view's future shape: the columns its collection will
// list, with no rows and the not-implemented panel inside them.
//
// A declared view used to render one sentence saying it was not built. That
// is honest and it is not useful: it tells a reader nothing they can plan
// against, review, or object to. The fields are already in the declaration --
// Register validates them exactly as it does an implemented view's -- so
// showing them costs nothing and turns "this is missing" into "this is what
// it is going to be, and it is missing".
func (m DeclaredModel) Table() TableModel {
	return TableModel{
		Prefix:  m.Page.Prefix,
		Columns: m.Descriptor.ListFields(),
		Preview: true,
		Empty:   m.ZeroState(),
		Caption: m.Descriptor.Title,
	}
}

// FutureTabs are the parts a record of this view will have once it is built.
//
// Rendered as a list rather than as a tab strip, because a tab strip whose
// tabs go nowhere is a control that lies about being one. They are the
// declared Sections plus the stream, which is the same input DetailModel.Tabs
// computes a real tab strip from, so the two cannot describe different
// futures.
func (m DeclaredModel) FutureTabs() []Tab {
	out := []Tab{{Label: "Details", Slug: detailsTab}}
	for _, s := range m.Descriptor.Sections {
		out = append(out, Tab{Label: s.Title, Slug: TabSlug(s.Title)})
	}
	if m.Descriptor.Stream != nil {
		out = append(out, Tab{Label: m.Descriptor.Stream.Title, Slug: outputTab})
	}
	// One entry is just "Details", which is not a future worth listing.
	if len(out) == 1 {
		return nil
	}
	return out
}

// HasFutureTabs reports whether the record-shape list renders.
func (m DeclaredModel) HasFutureTabs() bool { return len(m.FutureTabs()) > 0 }

// Planned declares a part of a record that does not exist yet but whose shape
// does.
//
// A section with no port is still worth declaring in full. Register validates
// its fields exactly as it does an implemented one's, the record page renders
// it as a tab carrying the columns it is going to have, and the result is a
// skeleton somebody can argue with rather than a gap they can only discover.
// The alternative -- leaving it out until the port exists -- means the shape
// is decided by whoever writes the port, at the point where changing it is
// most expensive.
//
// empty is what the panel says. It should name what is missing rather than
// apologise: "the Notification Engine owns these and this deployment has no
// port to it" is a fact somebody can act on, and "not implemented" is not.
func Planned(title, summary, empty string, fields []Field) Section {
	return Section{
		Status:  StatusDeclared,
		Title:   title,
		Summary: summary,
		Empty:   empty,
		Fields:  fields,
	}
}
