package view_test

import (
	"context"
	"net/url"
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// This file covers Field.Immutable: a field set when a record is created
// and never afterwards.
//
// It exists because three views were already relying on it informally. A
// team's organization, an inventory's organization, and a template's kind,
// definition and inventory are each read from storage on every update, and
// each edit form rendered the control anyway. What a reader could do was
// pick a different value, submit, be told it saved, and have nothing move.
//
// The two halves have to agree or the flag is worse than useless: the
// renderer must omit the control, and the narrower must refuse a submission
// that carries it. A renderer that omitted it while the narrower still
// accepted it would leave the field writable to anything that posts
// directly, which is the shape of a privilege escalation rather than a
// cosmetic bug.

// immutableFields is a create form with one field of each disposition.
var immutableFields = []view.Field{
	{Name: "name", Label: "NAME", Kind: view.KindText, InForm: true, Required: true},
	{Name: "tenant", Label: "TENANT", Kind: view.KindText, InForm: true, Required: true, Immutable: true},
	{Name: "created", Label: "CREATED", Kind: view.KindTimestamp, InList: true},
}

// names lists a field set, for comparing what a form offers.
func names(fields []view.Field) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, f.Name)
	}
	return out
}

func TestFormFieldsFor_TheEditFormDropsImmutableFields(t *testing.T) {
	d := view.Descriptor{Fields: immutableFields}

	if got, want := names(d.FormFieldsFor(false)), []string{"name", "tenant"}; !slices.Equal(got, want) {
		t.Errorf("create form = %v, want %v: the one moment the decision is taken is the one that must offer it", got, want)
	}
	if got, want := names(d.FormFieldsFor(true)), []string{"name"}; !slices.Equal(got, want) {
		t.Errorf("edit form = %v, want %v", got, want)
	}

	// FormFields is the create form, so a caller that does not care about
	// the mode gets the wider set rather than a surprise.
	if got, want := names(d.FormFields()), names(d.FormFieldsFor(false)); !slices.Equal(got, want) {
		t.Errorf("FormFields() = %v, want the create set %v", got, want)
	}
}

func TestNewValues_AnImmutableFieldPostedToAnEditIsUndeclared(t *testing.T) {
	form := url.Values{"name": {"platform"}, "tenant": {"7"}}

	// On a create, both are the form's own controls.
	if _, undeclared := view.NewValues(immutableFields, form, false); len(undeclared) != 0 {
		t.Errorf("create reported %v as undeclared, want none", undeclared)
	}

	// On an edit, the form never rendered tenant, so a submission carrying
	// it did not come from the form. Refused rather than ignored: silently
	// dropping input a caller believed was accepted is how somebody ends up
	// certain they changed something they did not.
	values, undeclared := view.NewValues(immutableFields, form, true)
	if !slices.Equal(undeclared, []string{"tenant"}) {
		t.Fatalf("edit reported %v as undeclared, want [tenant]", undeclared)
	}

	// And it is unreadable as well as reported, so a Bind function that
	// ignored the report still cannot act on it.
	if got := values.Get("tenant"); got != "" {
		t.Errorf("Get(tenant) on an edit = %q, want empty", got)
	}
}

func TestValidate_DoesNotRequireAFieldTheEditFormNeverOffered(t *testing.T) {
	ctx := context.Background()

	// tenant is Required, and absent, and that is correct on an edit: the
	// control was not rendered, so demanding a value for it would make
	// every edit unsubmittable.
	values, _ := view.NewValues(immutableFields, url.Values{"name": {"platform"}}, true)
	if errs := view.Validate(ctx, immutableFields, values); errs.Any() {
		t.Errorf("editing with no tenant = %v, want no errors", errs)
	}

	// On a create it is required, because that is the moment it is decided.
	values, _ = view.NewValues(immutableFields, url.Values{"name": {"platform"}}, false)
	errs := view.Validate(ctx, immutableFields, values)
	if len(errs["tenant"]) == 0 {
		t.Errorf("creating with no tenant = %v, want a required error on tenant", errs)
	}
}

func TestFormModel_ReadsItsModeOffTheRecordItPosts(t *testing.T) {
	d := view.Descriptor{Fields: immutableFields}

	// One bit decides both which controls are drawn and where the form
	// posts, so the two cannot disagree about which operation this is.
	create := view.FormModel{Descriptor: d}
	if got, want := names(create.Fields()), []string{"name", "tenant"}; !slices.Equal(got, want) {
		t.Errorf("create model fields = %v, want %v", got, want)
	}

	edit := view.FormModel{Descriptor: d, ID: "7"}
	if got, want := names(edit.Fields()), []string{"name"}; !slices.Equal(got, want) {
		t.Errorf("edit model fields = %v, want %v", got, want)
	}
}

func TestValues_Int(t *testing.T) {
	fields := []view.Field{{Name: "order", Label: "ORDER", Kind: view.KindNumber, InForm: true}}

	for _, tc := range []struct {
		name string
		raw  string
		want int
	}{
		{"a number", "3", 3},
		{"padded", "  3  ", 3},
		{"negative", "-1", -1},
		// Absent reads as zero, the same answer Bool gives an unchecked
		// checkbox: an optional number nobody supplied is its zero.
		{"absent", "", 0},
		// Unreachable through a form, because Validate refuses a
		// KindNumber field that is not a whole number before any Bind sees
		// it. Zero rather than a panic if it ever arrives another way.
		{"not a number", "soon", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, _ := view.NewValues(fields, url.Values{"order": {tc.raw}}, false)
			if got := v.Int("order"); got != tc.want {
				t.Errorf("Int(%q) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}

	// An undeclared field reads as zero rather than reaching the raw
	// submission, which is the property the whole type exists for.
	v, _ := view.NewValues(fields, url.Values{"smuggled": {"9"}}, false)
	if got := v.Int("smuggled"); got != 0 {
		t.Errorf("Int on an undeclared field = %d, want 0", got)
	}
}
