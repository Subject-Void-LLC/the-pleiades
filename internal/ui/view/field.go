package view

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// FieldKind is how one field is rendered and how its submitted value is
// interpreted. It is a closed set rather than free-form text for the same
// reason auth.Scope is: a typo in a literal would otherwise compile as
// "render this field in a way nothing handles" and surface as a blank cell
// three views later.
type FieldKind string

const (
	// KindText is a single-line string, rendered as <input type="text">.
	KindText FieldKind = "text"

	// KindLongText is a multi-line string, rendered as <textarea>.
	KindLongText FieldKind = "longtext"

	// KindSelect is a choice from a bounded set, rendered as <select>.
	// A descriptor declaring it must supply Options; Register refuses one
	// that does not, because a <select> with no <option> is a control a
	// user can focus but never operate.
	KindSelect FieldKind = "select"

	// KindBool is a two-state flag, rendered as <input type="checkbox">.
	KindBool FieldKind = "bool"

	// KindNumber is an integer, rendered as <input type="number">.
	KindNumber FieldKind = "number"

	// KindTags is a repeated string, submitted as a comma-separated value
	// and rendered as a list. It is deliberately not a multi-select: the
	// set is open, and a native multi-select is one of the least usable
	// controls on the platform for a keyboard or touch user.
	KindTags FieldKind = "tags"

	// KindTimestamp is a read-only instant, rendered in the list and the
	// detail view but never in a form. Nothing in this UI lets a caller
	// hand-write a timestamp: every one it displays is server-assigned.
	KindTimestamp FieldKind = "timestamp"

	// KindBadge is a short status word rendered as a filled badge. The
	// fill colour comes from BadgeClass, and the badge always carries the
	// word itself, never colour alone (WCAG SC 1.4.1).
	KindBadge FieldKind = "badge"

	// KindReadOnly is a value shown but never submitted. It appears in
	// lists and detail views and is skipped entirely when building a form,
	// so it cannot be round-tripped back through a write.
	KindReadOnly FieldKind = "readonly"
)

// writableKinds is every FieldKind a form may submit. KindTimestamp and
// KindReadOnly are absent on purpose: a field a user cannot author must
// not appear in a form, and one that never appears in a form must never be
// read back off a submission.
var writableKinds = map[FieldKind]bool{
	KindText:     true,
	KindLongText: true,
	KindSelect:   true,
	KindBool:     true,
	KindNumber:   true,
	KindTags:     true,
}

// Option is one choice in a KindSelect field. Label is what a human reads;
// Value is what the form submits and what Bind receives.
type Option struct {
	Label string
	Value string
}

// Field declares one attribute of a resource, once, for every consumer
// that needs to know about it.
//
// This single declaration drives six things, which is the entire reason
// adding a resource is cheap: the list view's <th> and <td>, the form's
// <label> and control, the detail view's <dl>, the server-side validation
// that runs before an author's own Bind ever sees the value, and the
// mobile card layout (a narrow viewport renders Label as a <dt> instead of
// a column header). Nothing about a field is declared twice, so nothing
// about a field can disagree with itself.
type Field struct {
	// Name is the form control name, the query parameter, and the key in
	// a Row's Cells map. It must be unique within a descriptor.
	Name string

	// Label is the <th> text and the <label> text. It is required, and
	// Register refuses a blank one: a table header or form control with
	// no text has no accessible name (WCAG SC 1.3.1, SC 4.1.2), and a
	// screen reader user meets it as an unlabelled column of values.
	Label string

	// Kind is how this field renders and how its submission is parsed.
	Kind FieldKind

	// Help is optional guidance rendered next to the control and wired to
	// it through aria-describedby, so it is announced with the field
	// rather than orphaned beside it.
	Help string

	// Required marks a field the form will not accept empty. It renders
	// the word "(required)" into the visible label -- not merely an
	// asterisk or a colour -- and sets aria-required, because "required"
	// communicated only by a red star is communicated only to people who
	// can see red stars.
	Required bool

	// Autocomplete is the HTML autofill token for this control (WCAG SC
	// 1.3.5). Register validates it against the closed specification
	// vocabulary, because a plausible-looking invented token ("device-name")
	// is silently inert to every assistive technology that would have
	// used a real one.
	Autocomplete string

	// MaxLen bounds the submitted value's length. It renders as the
	// maxlength attribute and is enforced again server-side, since a
	// maxlength attribute is advice to a cooperating browser and nothing
	// more.
	MaxLen int

	// Options supplies the choices for a KindSelect field. It takes a
	// context because a real option set is usually a query (every
	// inventory group, every device type), not a constant.
	Options func(context.Context) ([]Option, error)

	// InList includes this field as a column in the list view.
	InList bool

	// InForm includes this field as a control in the create and edit
	// forms. Ignored for KindTimestamp and KindReadOnly, which Register
	// refuses to accept as writable at all.
	InForm bool

	// Sortable offers this column as a sort key in the list view.
	Sortable bool

	// MobilePrimary marks this field as the one that identifies a record
	// at a glance. Below the layout breakpoint the list collapses from a
	// table to a stack of cards, and the primary field becomes each
	// card's heading while the rest become its <dl>. Exactly one field
	// per descriptor should carry it; Register enforces at most one.
	MobilePrimary bool

	// BadgeClass maps a KindBadge value to a CSS class name. The returned
	// name must come from a closed set (see ValidBadgeClasses), because
	// the alternative is a template interpolating a caller-controlled
	// string into a class attribute, and the content security policy this
	// UI ships under exists precisely so that is impossible.
	BadgeClass func(value string) string
}

// Writable reports whether this field may appear in a form and be read
// back off a submission. It is the one place the InForm flag and the
// kind's own writability are combined, so no caller reimplements the rule.
func (f Field) Writable() bool {
	return f.InForm && writableKinds[f.Kind]
}

// ValidBadgeClasses is the closed set of CSS class names a BadgeClass
// function may return. A name outside it is a programming error caught by
// the conformance suite rather than a silently unstyled badge.
var ValidBadgeClasses = map[string]bool{
	"badge-ok":      true,
	"badge-failed":  true,
	"badge-changed": true,
	"badge-skipped": true,
	"badge-neutral": true,
}

// autocompleteTokens is the closed autofill vocabulary from the HTML
// specification's autofill detail tokens, which is the set WCAG SC 1.3.5
// ("Identify Input Purpose") is defined against. A token outside this set
// does nothing for the users the criterion exists to serve, so Register
// treats one as a declaration error rather than passing it through to the
// markup.
var autocompleteTokens = map[string]bool{
	"off": true, "on": true,
	"name": true, "honorific-prefix": true, "given-name": true, "additional-name": true,
	"family-name": true, "honorific-suffix": true, "nickname": true,
	"username": true, "new-password": true, "current-password": true, "one-time-code": true,
	"organization-title": true, "organization": true,
	"street-address": true, "address-line1": true, "address-line2": true, "address-line3": true,
	"address-level4": true, "address-level3": true, "address-level2": true, "address-level1": true,
	"country": true, "country-name": true, "postal-code": true,
	"cc-name": true, "cc-given-name": true, "cc-additional-name": true, "cc-family-name": true,
	"cc-number": true, "cc-exp": true, "cc-exp-month": true, "cc-exp-year": true,
	"cc-csc": true, "cc-type": true,
	"transaction-currency": true, "transaction-amount": true,
	"language": true, "bday": true, "bday-day": true, "bday-month": true, "bday-year": true,
	"sex": true, "url": true, "photo": true,
	"tel": true, "tel-country-code": true, "tel-national": true, "tel-area-code": true,
	"tel-local": true, "tel-local-prefix": true, "tel-local-suffix": true, "tel-extension": true,
	"email": true, "impp": true,
}

// validateFields checks a descriptor's field declarations. It returns the
// first problem it finds, named precisely enough to fix without opening
// this file.
func validateFields(fields []Field, idField string) error {
	if len(fields) == 0 {
		return fmt.Errorf("declares no fields")
	}

	seen := make(map[string]bool, len(fields))
	primaries := 0
	idDeclared := false

	for _, f := range fields {
		switch {
		case strings.TrimSpace(f.Name) == "":
			return fmt.Errorf("has a field with no name")
		case seen[f.Name]:
			return fmt.Errorf("declares field %q twice", f.Name)
		case strings.TrimSpace(f.Label) == "":
			// A blank label is the single most common way to ship an
			// inaccessible table, so it fails at process start.
			return fmt.Errorf("field %q has no label", f.Name)
		}
		seen[f.Name] = true

		if f.Name == idField {
			idDeclared = true
		}
		if f.MobilePrimary {
			primaries++
		}

		if f.Kind == KindSelect && f.Options == nil {
			return fmt.Errorf("field %q is a select with no options", f.Name)
		}
		if f.Autocomplete != "" && !autocompleteTokens[f.Autocomplete] {
			return fmt.Errorf("field %q declares unknown autocomplete token %q", f.Name, f.Autocomplete)
		}
		if f.InForm && !writableKinds[f.Kind] {
			return fmt.Errorf("field %q is kind %q, which cannot appear in a form", f.Name, f.Kind)
		}
		if f.MaxLen < 0 {
			return fmt.Errorf("field %q has a negative MaxLen", f.Name)
		}
	}

	if strings.TrimSpace(idField) == "" {
		return fmt.Errorf("declares no IDField")
	}
	if !idDeclared {
		// Without this, every row's detail link points at a value the
		// descriptor never produces, and every one of them 404s.
		return fmt.Errorf("IDField %q is not a declared field", idField)
	}
	if primaries > 1 {
		return fmt.Errorf("declares %d MobilePrimary fields, want at most 1", primaries)
	}

	return nil
}

// Validate runs the checks a Field declares -- required, maximum length,
// and select-option membership -- against a submission, before the
// resource author's own Bind function sees it.
//
// It exists so that every resource gets the same baseline validation from
// the same declaration, rather than each author remembering to re-check
// what they already wrote down. An author's Bind adds domain rules on top;
// it never has to repeat these.
func Validate(ctx context.Context, fields []Field, v Values) FieldErrors {
	errs := FieldErrors{}

	for _, f := range fields {
		if !f.Writable() {
			continue
		}
		raw := strings.TrimSpace(v.Get(f.Name))

		if f.Required && raw == "" {
			errs.Add(f.Name, f.Label+" is required.")
			continue
		}
		if raw == "" {
			continue
		}
		if f.MaxLen > 0 && len(raw) > f.MaxLen {
			errs.Add(f.Name, fmt.Sprintf("%s must be %d characters or fewer.", f.Label, f.MaxLen))
			continue
		}
		if f.Kind == KindNumber {
			if _, err := strconv.Atoi(raw); err != nil {
				errs.Add(f.Name, f.Label+" must be a whole number.")
				continue
			}
		}
		if f.Kind == KindSelect && f.Options != nil {
			opts, err := f.Options(ctx)
			if err != nil {
				// A failed option lookup is not a validation failure --
				// it is an outage. Reporting it as "invalid choice"
				// would blame the user for a broken query.
				errs.Add(f.Name, "Choices for "+f.Label+" could not be loaded.")
				continue
			}
			if !hasOption(opts, raw) {
				errs.Add(f.Name, raw+" is not a valid choice for "+f.Label+".")
			}
		}
	}

	return errs
}

func hasOption(opts []Option, value string) bool {
	for _, o := range opts {
		if o.Value == value {
			return true
		}
	}
	return false
}
