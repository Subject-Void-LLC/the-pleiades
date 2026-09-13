// Package view's page chrome: the header every page renders above its own
// content, and the tab strip a record's parts are reached through.
//
// Everything here is a decision a template is not allowed to make. Before it
// existed each template wrote its own header, which is how one record page
// came to be titled with a name and another with a primary key.
package view

import (
	"net/url"
	"path"
	"strconv"
	"strings"
)

// Chrome is the header every page renders above its own content: where the
// reader is, what this page is called, what state it is in, what they may do
// to it, and which of its parts they are looking at.
//
// One type, built by every page model, rendered by one template component.
// Before this existed each template wrote its own <header> with an <h1> and
// an ad-hoc "Back to X" link, which is why a record page was titled with a
// primary key on one view and a name on another: there was no single place
// that decided. A model that cannot fill a field leaves it zero and the
// component renders nothing for it, so a page is never obliged to invent a
// breadcrumb or a badge it has no basis for.
type Chrome struct {
	// Crumbs is the trail, broadest first, ending at this page.
	Crumbs []Crumb

	// Title is the page's heading. On a record it is the record's name,
	// never its identifier: a page titled with a primary key has moved the
	// join into the reader's head.
	Title string

	// Badges sit beside the title. Status first, then anything that
	// qualifies what this record is.
	Badges []Badge

	// Summary is the sentence under the title. On a list it is the
	// descriptor's own Summary; on a record it is what the record is.
	Summary string

	// Actions are the controls this caller may use, already filtered.
	// They render right-aligned on the title row rather than at the foot
	// of the page, which is where a reader looks for them and where every
	// comparable control plane puts them.
	Actions []ChromeAction

	// Tabs are this record's parts. Empty on a page that has none, which
	// is what stops a list rendering an empty tab strip.
	Tabs []Tab
}

// HasTabs reports whether the tab strip renders at all.
//
// One tab is not a tab strip. A record with no sections and no stream has only
// its own fields, and rendering a lone "Details" tab above them is a control
// that offers a choice between one thing: it reads as a broken tab bar rather
// than as a page with nothing else on it.
func (c Chrome) HasTabs() bool { return len(c.Tabs) > 1 }

// HeaderClass distinguishes a header that carries tabs from one that does
// not, because the two need different bottom spacing and a template must not
// be the place that decides which.
func (c Chrome) HeaderClass() string {
	if c.HasTabs() {
		return "view-header"
	}
	return "view-header view-header-plain"
}

// Crumb is one step in the breadcrumb trail.
//
// Href empty means this step is not a link. That covers both ends of the
// trail: a navigation group is a heading with no page of its own, and the
// last crumb is the page you are already on.
type Crumb struct {
	Label string
	Href  string

	// Current marks the trail's last entry, which carries aria-current so
	// a screen reader announces where the trail ends rather than reading
	// an unmarked final list item.
	Current bool
}

// Linked reports whether this crumb renders as an anchor.
func (c Crumb) Linked() bool { return c.Href != "" }

// CurrentAttr is the literal aria-current value, "page" or the empty string
// that omits the attribute. A template must not hold this decision.
func (c Crumb) CurrentAttr() string {
	if c.Current {
		return "page"
	}
	return "false"
}

// Badge is a status chip rendered beside a title or in a cell.
//
// Class is one of the validated badge classes, never free text: it is
// interpolated into a class attribute, and the closed set is what makes that
// safe. BadgeClassFor is the only thing that produces one.
type Badge struct {
	Label string
	Class string
}

// ChromeAction is one control on the title row.
//
// Kind is a small closed vocabulary rather than a CSS class, so a template
// picks the rendering and a model never writes markup. A model that wants a
// button styled differently is a model that wants a new kind here, which is
// a decision worth making once in this file.
type ChromeAction struct {
	Label string
	Href  string
	Kind  ActionKind

	// Dialog names a dialog this control opens instead of navigating,
	// which is how delete confirmation works without script.
	Dialog string
}

// ActionKind is how a chrome action renders.
type ActionKind string

// The action kinds, which are the only ones a template knows how to draw.
const (
	// ActionPrimary is the one thing this page is for: launch, save.
	ActionPrimary ActionKind = "primary"

	// ActionNormal is everything else a caller may do here.
	ActionNormal ActionKind = "normal"

	// ActionDanger destroys something. It is visually separated and is
	// never the primary control on any page.
	ActionDanger ActionKind = "danger"
)

// Class is the rendered class attribute for this action.
func (a ChromeAction) Class() string {
	switch a.Kind {
	case ActionPrimary:
		return "btn btn-primary"
	case ActionDanger:
		return "btn btn-danger"
	default:
		return "btn"
	}
}

// OpensDialog reports whether this action opens a dialog rather than
// following a link.
func (a ChromeAction) OpensDialog() bool { return a.Dialog != "" }

// Tab is one part of a record.
//
// Tabs are computed from what the descriptor already declares -- its
// Sections and its Stream -- and never hand-written, so a section added to a
// declaration appears as a tab with a count and nothing has to be remembered.
type Tab struct {
	Label string
	Href  string

	// Slug is the value the tab query parameter carries. It is derived
	// from the label rather than declared, so two sections cannot disagree
	// about their own address.
	Slug string

	// Count is the number of rows behind this tab, rendered beside its
	// label. Empty when a count would be meaningless, which is the case
	// for the record's own fields and for a live stream.
	Count string

	Current bool
}

// CurrentAttr is the literal aria-current value for this tab.
func (t Tab) CurrentAttr() string {
	if t.Current {
		return "page"
	}
	return "false"
}

// Class is the rendered class attribute, so the template holds no state.
func (t Tab) Class() string {
	if t.Current {
		return "tab tab-current"
	}
	return "tab"
}

// HasCount reports whether a count renders beside the label.
func (t Tab) HasCount() bool { return t.Count != "" }

// detailsTab is the slug of a record's own fields, which is the tab a record
// opens on and the one every other tab returns to.
const detailsTab = "details"

// outputTab is the slug of the live stream, which is a tab on the record
// rather than a page of its own. It is a constant because the handler that
// serves the stream and the model that links to it must agree.
const outputTab = "output"

// TabSlug turns a section title into its address.
//
// Lowercase, non-alphanumerics collapsed to single hyphens, trimmed. The
// same transformation LoadedSection.ID already applied to build a DOM id,
// factored out here because a slug is now two things -- an element id and a
// URL -- and two implementations of it would eventually disagree.
func TabSlug(title string) string {
	var b strings.Builder
	lastHyphen := true // leading hyphens are trimmed by never emitting one
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		default:
			if !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

// groupCrumb is the navigation group a view sits under, rendered as the
// first step of every trail.
//
// It is not a link because a group has no page: it is a heading in the
// sidebar. Including it anyway is what makes a trail locate the reader in
// the navigation rather than only in the URL, which is the whole point of
// having one.
func groupCrumb(d Descriptor) Crumb {
	if d.NavGroup == NavGroupNone {
		return Crumb{}
	}
	return Crumb{Label: titleCase(string(d.NavGroup))}
}

// titleCase renders a shouted constant as a word.
//
// The navigation groups are declared in upper case because that is how the
// sidebar renders them, but a breadcrumb is a sentence and SHOUTING IN ONE
// is a different voice inside the same page.
func titleCase(s string) string {
	if s == "" {
		return ""
	}
	lower := strings.ToLower(s)
	return strings.ToUpper(lower[:1]) + lower[1:]
}

// listCrumbs is the trail shared by every page under one view: the group,
// then the view itself.
//
// Every builder below starts from this, which is why a view renamed in one
// place is renamed in all of them.
func listCrumbs(prefix string, d Descriptor, currentIsList bool) []Crumb {
	out := make([]Crumb, 0, 3)
	if g := groupCrumb(d); g.Label != "" {
		out = append(out, g)
	}
	listCrumb := Crumb{Label: d.Title}
	if currentIsList {
		listCrumb.Current = true
	} else {
		listCrumb.Href = path.Join(prefix, d.Name)
	}
	return append(out, listCrumb)
}

// recordCrumb is the trail through a single record, used by the record page
// and by every page hanging off it.
func recordCrumbs(prefix string, d Descriptor, id, name string, currentIsRecord bool) []Crumb {
	out := listCrumbs(prefix, d, false)
	label := name
	if strings.TrimSpace(label) == "" {
		label = id
	}
	crumb := Crumb{Label: label}
	if currentIsRecord {
		crumb.Current = true
	} else {
		crumb.Href = path.Join(prefix, d.Name, url.PathEscape(id))
	}
	return append(out, crumb)
}

// Chrome assembles the list page's header.
func (m ListModel) Chrome() Chrome {
	c := Chrome{
		Crumbs:  listCrumbs(m.Page.Prefix, m.Descriptor, true),
		Title:   m.Descriptor.Title,
		Summary: m.Descriptor.Summary,
	}
	if m.Descriptor.ListsRecords() {
		c.Badges = append(c.Badges, Badge{Label: m.CountLabel(), Class: "badge-neutral"})
	}
	if m.CanCreate() {
		c.Actions = append(c.Actions, ChromeAction{
			Label: m.CreateLabel(),
			Href:  m.CreateHref(),
			Kind:  ActionPrimary,
		})
	}
	return c
}

// CountLabel is the row count beside a list's title.
//
// It counts what is on this page, and says so. A bare number next to a title
// reads as a total, and this view has no total to report: the store is read
// through a cursor, so "how many are there" is a question nothing here can
// answer without scanning every row.
func (m ListModel) CountLabel() string {
	n := strconv.Itoa(len(m.Rows))
	if len(m.Rows) == 1 {
		return n + " shown"
	}
	return n + " shown"
}

// CreateLabel is what the create control says.
//
// The descriptor's Title is plural, and a button offering to make one of
// something should say the singular. There is no reliable way to depluralise
// English, so this drops a trailing "s" and accepts that a view called
// "Access" would read oddly -- which is why Access declares no create at all.
func (m ListModel) CreateLabel() string {
	return "New " + strings.ToLower(strings.TrimSuffix(m.Descriptor.Title, "s"))
}

// Chrome assembles the record page's header, including its tabs.
func (m DetailModel) Chrome() Chrome {
	c := Chrome{
		Crumbs:  recordCrumbs(m.Page.Prefix, m.Descriptor, m.Row.ID, m.RecordName(), true),
		Title:   m.RecordName(),
		Summary: m.RecordSummary(),
		Tabs:    m.Tabs(),
	}
	if b, ok := m.StatusBadge(); ok {
		c.Badges = append(c.Badges, b)
	}
	for _, a := range m.Actions() {
		c.Actions = append(c.Actions, ChromeAction{Label: a.Label, Href: a.Href, Kind: ActionPrimary})
	}
	if m.CanEdit() {
		c.Actions = append(c.Actions, ChromeAction{Label: "Edit", Href: m.EditHref(), Kind: ActionNormal})
	}
	if m.CanDelete() {
		c.Actions = append(c.Actions, ChromeAction{Label: "Delete", Kind: ActionDanger, Dialog: "confirm-delete"})
	}
	return c
}

// RecordName is what this record is called, which is what titles its page.
//
// Falls back to the identifier when the descriptor names no title field or
// the record's own is empty, because a page with no heading is worse than one
// headed by a key.
func (m DetailModel) RecordName() string {
	if f := m.Descriptor.TitleField(); f != "" {
		if v := strings.TrimSpace(m.Row.Cells[f]); v != "" {
			return v
		}
	}
	return m.Row.ID
}

// RecordSummary is the line under a record's title.
//
// It names the record's identifier when the title is showing something else,
// so the key stays visible to somebody who needs it -- reading a log, filing
// a ticket -- without being the first thing anybody sees.
func (m DetailModel) RecordSummary() string {
	if m.RecordName() == m.Row.ID {
		return m.Descriptor.Summary
	}
	return m.Descriptor.Title + " " + m.Row.ID
}

// StatusBadge is the record's state, rendered beside its title.
//
// It reads the descriptor's own status field, so a view that declares no
// badge field gets no badge rather than this package guessing which of its
// values is a state.
func (m DetailModel) StatusBadge() (Badge, bool) {
	f, ok := m.Descriptor.StatusField()
	if !ok {
		return Badge{}, false
	}
	value := strings.TrimSpace(m.Row.Cells[f.Name])
	if value == "" {
		return Badge{}, false
	}
	return Badge{Label: value, Class: BadgeClassFor(f, value)}, true
}

// Tabs is the record's parts: its own fields, each declared section, and the
// live stream when it has one.
//
// Computed rather than declared. The descriptor already holds every input,
// and a hand-written tab bar is a second list of sections to keep in step
// with the first.
func (m DetailModel) Tabs() []Tab {
	base := path.Join(m.Page.Prefix, m.Descriptor.Name, url.PathEscape(m.Row.ID))
	current := m.CurrentTab()

	tabs := []Tab{{
		Label:   "Details",
		Slug:    detailsTab,
		Href:    base,
		Current: current == detailsTab,
	}}

	for _, s := range m.Sections {
		slug := TabSlug(s.Spec.Title)
		t := Tab{
			Label:   s.Spec.Title,
			Slug:    slug,
			Href:    base + "?tab=" + url.QueryEscape(slug),
			Current: current == slug,
		}
		// A declared-but-unimplemented section has no rows to count, and
		// rendering a zero beside it would claim something about live
		// data it has no port to say.
		if s.Spec.Implemented() {
			t.Count = strconv.Itoa(len(s.Rows))
		}
		tabs = append(tabs, t)
	}

	if m.CanStream() {
		tabs = append(tabs, Tab{
			Label:   m.Descriptor.Stream.Title,
			Slug:    outputTab,
			Href:    base + "?tab=" + outputTab,
			Current: current == outputTab,
		})
	}
	return tabs
}

// CurrentTab is the selected tab's slug, defaulting to the record's own
// fields.
//
// An unrecognised value falls back to the default rather than rendering an
// empty page: the tab is in a query parameter, so it arrives from whatever
// somebody pasted into the address bar.
func (m DetailModel) CurrentTab() string {
	want := strings.TrimSpace(m.Tab)
	if want == "" {
		return m.defaultTab()
	}
	if want == outputTab && m.CanStream() {
		return outputTab
	}
	for _, s := range m.Sections {
		if TabSlug(s.Spec.Title) == want {
			return want
		}
	}
	return m.defaultTab()
}

// defaultTab resolves the descriptor's declared landing tab, falling back to
// the record's own fields.
//
// The fallback matters as much as the feature: a declared title that matches
// no section and no stream must open the record rather than nothing, because
// the usual way that happens is a section being renamed and the declaration
// not following it.
func (m DetailModel) defaultTab() string {
	want := strings.TrimSpace(m.Descriptor.DefaultTab)
	if want == "" {
		return detailsTab
	}
	if m.CanStream() && want == m.Descriptor.Stream.Title {
		return outputTab
	}
	for _, s := range m.Sections {
		if s.Spec.Title == want {
			return TabSlug(want)
		}
	}
	return detailsTab
}

// ShowDetails reports whether the record's own field list renders.
func (m DetailModel) ShowDetails() bool { return m.CurrentTab() == detailsTab }

// ShowStream reports whether the live output tab is the selected one.
func (m DetailModel) ShowStream() bool { return m.CurrentTab() == outputTab }

// VisibleSections is the sections the selected tab renders, which is one of
// them or none.
//
// A slice rather than a single value so the template loops over it exactly as
// it always did, and so a future tab holding more than one section needs no
// template change.
func (m DetailModel) VisibleSections() []LoadedSection {
	current := m.CurrentTab()
	if current == detailsTab || current == outputTab {
		return nil
	}
	for _, s := range m.Sections {
		if TabSlug(s.Spec.Title) == current {
			return []LoadedSection{s}
		}
	}
	return nil
}

// Chrome assembles the form page's header.
func (m FormModel) Chrome() Chrome {
	var crumbs []Crumb
	if m.Editing() {
		crumbs = recordCrumbs(m.Page.Prefix, m.Descriptor, m.ID, m.RecordName(), false)
		crumbs = append(crumbs, Crumb{Label: "Edit", Current: true})
	} else {
		crumbs = listCrumbs(m.Page.Prefix, m.Descriptor, false)
		crumbs = append(crumbs, Crumb{Label: "New", Current: true})
	}
	return Chrome{Crumbs: crumbs, Title: m.Heading(), Summary: m.Descriptor.Summary}
}

// RecordName is what the record being edited is called, for its breadcrumb.
func (m FormModel) RecordName() string {
	if f := m.Descriptor.TitleField(); f != "" {
		if v := strings.TrimSpace(m.Values[f]); v != "" {
			return v
		}
	}
	return m.ID
}

// Chrome assembles an action prompt's header.
func (m ActionModel) Chrome() Chrome {
	crumbs := recordCrumbs(m.Page.Prefix, m.Descriptor, m.ID, m.ID, false)
	crumbs = append(crumbs, Crumb{Label: m.Action.Heading, Current: true})
	return Chrome{Crumbs: crumbs, Title: m.Heading()}
}

// Chrome assembles the live stream page's header.
//
// The stream is a tab on its record, so this page exists for a link somebody
// bookmarked before it was. Its trail walks back to the record rather than
// leaving a reader on a page with no way to tell which job they are watching,
// which is what the standalone page did.
func (m StreamModel) Chrome() Chrome {
	crumbs := recordCrumbs(m.Page.Prefix, m.Descriptor, m.ID, m.ID, false)
	crumbs = append(crumbs, Crumb{Label: m.Descriptor.Stream.Title, Current: true})
	return Chrome{Crumbs: crumbs, Title: m.Heading()}
}

// Chrome assembles a declared view's header.
func (m DeclaredModel) Chrome() Chrome {
	return Chrome{
		Crumbs:  listCrumbs(m.Page.Prefix, m.Descriptor, true),
		Title:   m.Descriptor.Title,
		Summary: m.Descriptor.Summary,
		Badges:  []Badge{{Label: "Declared", Class: "badge-skipped"}},
	}
}
