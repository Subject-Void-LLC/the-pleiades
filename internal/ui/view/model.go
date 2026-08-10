package view

import (
	"net/url"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
)

// Cells is one record's presentation values, keyed by Field.Name. It is
// deliberately map[string]string and not map[string]any: a template that
// can receive any type is a template that has to decide how to render one,
// and every decision a template makes is a branch no coverage tool will
// ever report on. Formatting happens in Go, in a resource's Projector,
// where it is ordinary tested code.
type Cells map[string]string

// Row is one record, erased from its domain type into exactly what a
// template needs: an identity for the detail link, and the values keyed by
// the field names the descriptor already declared.
type Row struct {
	// ID is the value of the descriptor's IDField for this record. It
	// becomes the {id} path segment, so it is URL-escaped at render time
	// and never interpolated raw.
	ID string

	// Cells holds this record's presentation values.
	Cells Cells
}

// RowPage is one page of erased rows plus the cursor that fetches the
// next. Keyset paging, not offset: an offset query over a table that is
// being written to skips and repeats records, which on an inventory list
// means a device silently missing from the page a human is reading.
type RowPage struct {
	Rows       []Row
	NextCursor string
}

// Query is a list request: how many rows, from where, sorted how.
type Query struct {
	// Cursor is the opaque position returned by a previous RowPage, or
	// empty for the first page.
	Cursor string

	// Limit is the maximum number of rows to return. Zero means the
	// resource's own default.
	Limit int

	// Sort names a Field to order by. It is validated against the
	// descriptor's Sortable fields before it reaches a resource, so a
	// resource never receives an arbitrary caller-supplied column name to
	// interpolate into a query.
	Sort string

	// Descending reverses the sort.
	Descending bool

	// Search is a free-text filter. A resource that does not support one
	// ignores it.
	Search string
}

// FieldErrors maps a Field.Name to the problems found with its submitted
// value. It is ordered on read rather than on write so a form's error
// summary lists fields in a stable order across requests -- a summary that
// reshuffles between two submissions is one a screen reader user has to
// re-read from the top every time.
type FieldErrors map[string][]string

// Add records one problem against a field.
func (e FieldErrors) Add(field, message string) {
	e[field] = append(e[field], message)
}

// Any reports whether anything failed.
func (e FieldErrors) Any() bool { return len(e) > 0 }

// Names returns every field with an error, sorted.
func (e FieldErrors) Names() []string {
	names := make([]string, 0, len(e))
	for name := range e {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Summary flattens the errors into the ordered list a form renders at the
// top and moves focus to on a failed submission (WCAG SC 3.3.1). Each
// entry carries the field name so the summary can link to the control
// rather than merely describing it.
func (e FieldErrors) Summary(fields []Field) []SummaryError {
	label := make(map[string]string, len(fields))
	order := make(map[string]int, len(fields))
	for i, f := range fields {
		label[f.Name] = f.Label
		order[f.Name] = i
	}

	out := make([]SummaryError, 0, len(e))
	for name, messages := range e {
		for _, m := range messages {
			out = append(out, SummaryError{Field: name, Label: label[name], Message: m})
		}
	}
	// Declaration order, so the summary reads in the same order as the
	// form itself.
	sort.SliceStable(out, func(i, j int) bool {
		return order[out[i].Field] < order[out[j].Field]
	})
	return out
}

// SummaryError is one entry in a form's error summary.
type SummaryError struct {
	Field   string
	Label   string
	Message string
}

// reservedFormKeys are submission keys the form machinery owns. They are
// not resource fields and must not be reported as undeclared input.
var reservedFormKeys = map[string]bool{
	"_csrf":   true,
	"_method": true,
}

// Values is a submitted form, narrowed to the fields a descriptor actually
// declared.
//
// It exists so that mass assignment is structurally impossible rather than
// remembered. A resource author cannot read a parameter their descriptor
// did not declare, because Values has no method that would return one --
// there is no escape hatch back to the raw url.Values, deliberately. The
// usual defence against over-posting is an allowlist someone has to
// maintain; here the allowlist is the field declaration that already had
// to exist for the form to render at all.
type Values struct {
	declared map[string]Field
	raw      url.Values
}

// NewValues narrows raw to the writable fields in the given declaration.
// It returns the narrowed Values and every submitted key that no field
// declared, so a handler can reject the request outright rather than
// silently ignoring input the caller believed was accepted -- which is
// also how a typo in a template's control name gets caught instead of
// becoming a field that never saves.
func NewValues(fields []Field, raw url.Values) (Values, []string) {
	declared := make(map[string]Field, len(fields))
	for _, f := range fields {
		if f.Writable() {
			declared[f.Name] = f
		}
	}

	var undeclared []string
	for key := range raw {
		if !declared[key].Writable() && !reservedFormKeys[key] {
			undeclared = append(undeclared, key)
		}
	}
	sort.Strings(undeclared)

	return Values{declared: declared, raw: raw}, undeclared
}

// Get returns the submitted value for a declared field, or the empty
// string for one this descriptor never declared.
func (v Values) Get(name string) string {
	if _, ok := v.declared[name]; !ok {
		return ""
	}
	return v.raw.Get(name)
}

// Bool reports whether a declared KindBool field was checked. An unchecked
// checkbox submits nothing at all, so absence is false rather than an
// error.
func (v Values) Bool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(v.Get(name))) {
	case "", "0", "false", "off", "no":
		return false
	default:
		return true
	}
}

// Tags splits a declared KindTags field's comma-separated value, trimming
// blanks. It returns nil rather than a one-element slice containing the
// empty string, which is what a naive Split gives for empty input and what
// then persists as a device with one nameless tag.
func (v Values) Tags(name string) []string {
	raw := v.Get(name)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// Affordances is the set of link relations an identity may exercise on the
// thing currently being rendered.
//
// It is computed once per request from the same auth.HATEOASGenerator the
// JSON API's _links array is computed from, so a rendered button and an
// API response cannot disagree about what a caller is allowed to do.
type Affordances struct {
	permitted map[auth.LinkRel]bool

	// Unknown records that authorization could not be evaluated at all.
	// It is distinct from "nothing permitted" on purpose: HTML has no
	// way to express the absent-_links state the JSON API uses for this,
	// so a template renders no action buttons *and* a visible alert
	// rather than quietly presenting a read-only page as if that were
	// the answer.
	Unknown bool
}

// NewAffordances builds an Affordances set from the relations a generator
// permitted.
func NewAffordances(rels []auth.LinkRel) Affordances {
	permitted := make(map[auth.LinkRel]bool, len(rels))
	for _, r := range rels {
		permitted[r] = true
	}
	return Affordances{permitted: permitted}
}

// UnknownAffordances is what a handler renders when the authorization
// backend failed. Nothing is permitted and the caller is told why.
func UnknownAffordances() Affordances {
	return Affordances{permitted: map[auth.LinkRel]bool{}, Unknown: true}
}

// Can reports whether the identity may exercise rel here.
func (a Affordances) Can(rel auth.LinkRel) bool { return a.permitted[rel] }
