package view

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
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

	// Refs holds the target record id for each field declaring
	// References, keyed by field name. The name a reader sees lives in
	// Cells; the id a link points at lives here.
	//
	// Two maps rather than one encoded value, because the alternative is a
	// template parsing an id back out of a label, and a label is text a
	// human wrote. "Network (7)" and a customer who names an organization
	// "Network (7)" are indistinguishable to any parser worth writing.
	//
	// A field may declare References and still have no entry here: a
	// reference to a record that has been deleted has a name to render and
	// nowhere to point.
	Refs map[string]string
}

// Ref returns the target id for a referencing field, empty when the field
// does not reference anything or its target no longer exists.
func (r Row) Ref(name string) string {
	if r.Refs == nil {
		return ""
	}
	return r.Refs[name]
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

// DefaultPageSize is what a reader uses when a Query names no limit. It
// lives here rather than in the web handler so that a reader called from a
// test, a seeder or a future non-HTTP caller pages the same way the served
// list does.
const DefaultPageSize = 50

// PageSize is the limit a reader should actually ask its store for, with
// Limit's documented "zero means the resource's own default" applied once.
//
// Every reader needs this because every reader over-fetches by one to
// observe whether a next page exists, and computing that from a zero limit
// asks for a single row and then slices it to nothing. Doing it here rather
// than in each reader also keeps the default from drifting between two of
// them.
func (q Query) PageSize() int {
	if q.Limit <= 0 {
		return DefaultPageSize
	}
	return q.Limit
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
	// Where an appearance control puts the reader back. It rides on the
	// preference forms rather than on a resource form, but the edit-form
	// conformance suite resubmits every control rendered on a page, so any
	// key the chrome emits has to be declared here or it reads as
	// over-posting. The underscore is the convention that says "the form
	// machinery owns this, it is not a field".
	"_return": true,
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

	// editing is which form this submission came from, carried so Validate
	// applies the same narrowing the renderer did. A create and an edit do
	// not offer the same controls once a field is Immutable, and a
	// validator working from the wider set would demand a value for a
	// control the page never showed.
	editing bool
}

// NewValues narrows raw to the fields the given declaration makes writable
// in this mode. It returns the narrowed Values and every submitted key that
// no field declared, so a handler can reject the request outright rather
// than silently ignoring input the caller believed was accepted -- which is
// also how a typo in a template's control name gets caught instead of
// becoming a field that never saves.
//
// editing says whether the submission is an update. It decides the set, so
// an immutable field posted to an update comes back as undeclared: the edit
// form never rendered it, so a submission carrying it did not come from the
// form, and refusing is the same answer the launch form gives to a smuggled
// control.
func NewValues(fields []Field, raw url.Values, editing bool) (Values, []string) {
	declared := make(map[string]Field, len(fields))
	for _, f := range fields {
		if f.WritableOn(editing) {
			declared[f.Name] = f
		}
	}

	var undeclared []string
	for key := range raw {
		if _, ok := declared[key]; !ok && !reservedFormKeys[key] {
			undeclared = append(undeclared, key)
		}
	}
	sort.Strings(undeclared)

	return Values{declared: declared, raw: raw, editing: editing}, undeclared
}

// NarrowPrefill checks a prefill map against the controls a form will
// actually render, and refuses one that could lose or leak a value.
//
// The mirror of NewValues, and it exists for the same reason. A submission
// carrying a field nobody declared is refused rather than ignored, because
// silently dropping input somebody believed was accepted is how they end up
// certain they changed something they did not. A PREFILL naming a control
// nobody declared is that same mistake from the other side, and it is
// worse: the control renders empty, the operator does not retype a value
// they cannot see, and the save writes the blank over what was stored.
// Nothing on the page shows it, and this is the only place it can be seen.
//
// A non-empty value for a password control is refused too. field.templ
// writes a password's value into a value attribute, and the comment
// justifying that rests on a survey password being answered once per launch
// and never read back. A prefilled form is exactly the case that reasoning
// excludes, so a stored secret would be rendered into the page source. An
// EMPTY value is allowed, so the "leave blank to keep the current one" form
// stays possible: what is refused is prefilling a secret, not offering the
// control.
//
// editing selects the mode the form renders in, and is taken rather than
// assumed so a caller cannot check a prefill against a control set the form
// does not draw. An edit form withholds an Immutable control, so on an edit
// a prefill naming one is a value nothing renders, which is the case this
// function exists to catch.
func NarrowPrefill(fields []Field, editing bool, values map[string]string) error {
	declared := make(map[string]Field, len(fields))
	for _, f := range fields {
		if !f.WritableOn(editing) {
			continue
		}
		declared[f.Name] = f
	}

	unknown := make([]string, 0, len(values))
	for name, value := range values {
		f, ok := declared[name]
		if !ok {
			unknown = append(unknown, name)
			continue
		}
		if f.Kind == KindPassword && value != "" {
			return fmt.Errorf("prefill carries a value for the password control %q, which would render the secret into the page", name)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("prefill names %v, which this form does not render, so the value would be lost on the next save", unknown)
	}
	return nil
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

// Editing reports whether this submission came from an edit form rather
// than a create form.
//
// A Bind function needs it for exactly one reason: an Immutable field is
// absent from an edit submission by design, so a Bind that parses one
// unconditionally rejects every edit. That is not hypothetical, it is the
// defect this method was added to fix, which broke editing on three views
// at once and passed CI because no test posted an edit form.
//
// The writer's Update is what supplies the real value in that case, from
// storage, so a Bind that skips an absent immutable field is not leaving
// it unset: it is declining to overwrite what only storage knows.
func (v Values) Editing() bool { return v.editing }

// Fields returns the fields this submission was narrowed against, in
// declaration order where the caller preserved one and otherwise in map
// order.
//
// It exists for a binder whose field set is not knowable from its own
// package. Credentials is the case: its controls come from the credential
// type's input schema, so its Bind cannot walk a static list to find out
// what was submitted, and walking the raw submission instead would read
// keys that were never declared -- exactly the narrowing this type exists
// to perform. Ranging over the declared set keeps the guarantee intact:
// what comes back is what a Field permitted, never what a request carried.
func (v Values) Fields() []Field {
	out := make([]Field, 0, len(v.declared))
	for _, f := range v.declared {
		out = append(out, f)
	}
	return out
}

// Int returns a declared KindNumber field's value, and zero for a field
// left empty.
//
// It cannot report a parse failure, and does not need to: Validate has
// already refused a KindNumber field whose value is not a whole number, and
// it runs before any Bind function sees the submission. So the only two
// states reaching here are a number and an absence, and an absent optional
// number is its zero -- which is the same answer Bool gives an unchecked
// checkbox, for the same reason.
//
// It exists because four views were each reaching for strconv themselves,
// and a fifth reading a number that Validate had already parsed once is a
// fifth chance to disagree with it about what a number is.
func (v Values) Int(name string) int {
	n, err := strconv.Atoi(strings.TrimSpace(v.Get(name)))
	if err != nil {
		return 0
	}
	return n
}

// Selected returns every value submitted for a declared KindLookup field.
//
// Get cannot serve this: url.Values.Get returns only the first value, so a
// multi-select of five users read through it would silently persist one.
// That is the shape of failure this whole type exists to prevent, so the
// plural read is its own method rather than an option on the singular one.
//
// Blank values are dropped. A multi-select can legitimately submit nothing
// at all, and an empty string in the middle of a list of ids is not a
// selection anybody made.
func (v Values) Selected(name string) []string {
	if _, ok := v.declared[name]; !ok {
		return nil
	}
	raw := v.raw[name]
	out := make([]string, 0, len(raw))
	for _, value := range raw {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
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
