package view_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// The model methods are where every decision a template is not allowed to
// make actually lives. That is the whole bargain that lets internal/ui/render
// be excluded from the coverage ratchet, so these have to be covered here or
// the exclusion is just a hole.

func modelDescriptor() view.Descriptor {
	return view.Descriptor{
		Name:     "widgets",
		Title:    "Widgets",
		NavLabel: "WIDGETS",
		IDField:  "name",
		Fields: []view.Field{
			{Name: "name", Label: "NAME", Kind: view.KindText, InList: true, InForm: true,
				Required: true, MaxLen: 64, Help: "The widget's name."},
			{Name: "state", Label: "STATE", Kind: view.KindBadge, InList: true,
				BadgeClass: func(v string) string {
					if v == "ok" {
						return "badge-ok"
					}
					return "totally-invented-class"
				}},
			{Name: "notes", Label: "NOTES", Kind: view.KindLongText, InForm: true},
		},
		Ops: view.Ops{
			List:   &apispec.ListDevices,
			Get:    &apispec.GetDevice,
			Create: &apispec.CreateDevice,
			Update: &apispec.UpdateDevice,
			Delete: &apispec.DeleteDevice,
		},
	}
}

func listModel(rels ...auth.LinkRel) view.ListModel {
	return view.ListModel{
		Page:       view.PageModel{Prefix: "/ui", Title: "Widgets"},
		Descriptor: modelDescriptor(),
		Rows: []view.Row{
			{ID: "alpha", Cells: view.Cells{"name": "alpha", "state": "ok"}},
			{ID: "needs escaping/../..", Cells: view.Cells{"name": "x", "state": "bad"}},
		},
		Aff: view.NewAffordances(rels),
	}
}

// TestListModel_URLsEscapeTheIdentifier is the model-layer half of the
// defect gosec found in the write path: path.Join *cleans* its result, so an
// identifier containing ".." would not be a segment, it would delete the
// prefix in front of it and send the reader somewhere else entirely.
func TestListModel_URLsEscapeTheIdentifier(t *testing.T) {
	m := listModel()

	for _, row := range m.Rows {
		href := m.DetailHref(row)
		if !strings.HasPrefix(href, "/ui/widgets/") {
			t.Errorf("DetailHref(%q) = %q, which escaped its own prefix", row.ID, href)
		}

		// The invariant is that the identifier stays one path segment, not
		// that the characters ".." never appear. An id of "../.." escaped
		// to "..%2F.." is inert: it is a literal segment name, and there is
		// no separator left for a path resolver to act on. Asserting on the
		// absence of dots instead would fail on a perfectly safe id.
		segment := strings.TrimPrefix(href, "/ui/widgets/")
		if strings.Contains(segment, "/") {
			t.Errorf("DetailHref(%q) = %q, whose identifier spans more than one segment", row.ID, href)
		}
	}
}

// TestListModel_BadgeClassRefusesAnythingOutsideTheSet: a class attribute is
// the one place a caller-controlled string could reach the stylesheet, which
// is exactly what the content security policy exists to make impossible.
func TestListModel_BadgeClassRefusesAnythingOutsideTheSet(t *testing.T) {
	m := listModel()
	state := m.Descriptor.Fields[1]

	if got := m.BadgeClass(m.Rows[0], state); got != "badge-ok" {
		t.Errorf("BadgeClass for a valid value = %q, want badge-ok", got)
	}
	if got := m.BadgeClass(m.Rows[1], state); got != "badge-neutral" {
		t.Errorf("BadgeClass for an invented class = %q, want badge-neutral", got)
	}

	// A field with no mapper at all must still produce a valid class.
	if got := m.BadgeClass(m.Rows[0], m.Descriptor.Fields[0]); got != "badge-neutral" {
		t.Errorf("BadgeClass with no mapper = %q, want badge-neutral", got)
	}
}

// TestListModel_CanCreateReadsTheEndpointsOwnRelation is the bug this
// method was rewritten to fix: not every act of creation is called "create".
// Dispatching a runbook creates a job and declares auth.RelExecute, and a
// hardcoded RelCreate here hid that button from everyone while the
// permission check passed.
func TestListModel_CanCreateReadsTheEndpointsOwnRelation(t *testing.T) {
	permitted := listModel(apispec.CreateDevice.Rel)
	if !permitted.CanCreate() {
		t.Error("CanCreate() = false for an identity the generator permitted")
	}

	other := listModel(auth.RelSelf)
	if other.CanCreate() {
		t.Error("CanCreate() = true for an identity permitted only an unrelated relation")
	}

	none := listModel()
	if none.CanCreate() {
		t.Error("CanCreate() = true with nothing permitted")
	}

	// And a view that does not offer creation never renders the control,
	// however broadly the caller is scoped.
	noCreate := listModel(apispec.CreateDevice.Rel)
	noCreate.Descriptor.Ops.Create = nil
	if noCreate.CanCreate() {
		t.Error("CanCreate() = true for a view that declares no create endpoint")
	}
}

// TestListModel_NextHrefIsEmptyOnTheLastPage, because a "next page" link on
// the last page is a link to an empty table.
func TestListModel_NextHrefIsEmptyOnTheLastPage(t *testing.T) {
	m := listModel()
	if got := m.NextHref(); got != "" {
		t.Errorf("NextHref() with no cursor = %q, want empty", got)
	}

	m.NextCursor = "alpha"
	got := m.NextHref()
	if !strings.HasPrefix(got, "/ui/widgets?") || !strings.Contains(got, "after=alpha") {
		t.Errorf("NextHref() = %q, want the collection with an after cursor", got)
	}
}

// TestDetailModel_AppliesWithdrawsAnAffordancePerRecord covers the rule an
// archived device relies on: retirement is idempotent, so following a delete
// link again would succeed and change nothing, with no error to explain why.
func TestDetailModel_AppliesWithdrawsAnAffordancePerRecord(t *testing.T) {
	d := modelDescriptor()
	d.Applies = func(row view.Row, rel auth.LinkRel) bool {
		return !(rel == auth.RelDelete && row.Cells["state"] == "archived")
	}

	aff := view.NewAffordances([]auth.LinkRel{apispec.UpdateDevice.Rel, apispec.DeleteDevice.Rel})

	live := view.DetailModel{
		Page: view.PageModel{Prefix: "/ui"}, Descriptor: d, Aff: aff,
		Row: view.Row{ID: "alpha", Cells: view.Cells{"state": "active"}},
	}
	if !live.CanDelete() {
		t.Error("CanDelete() = false for a live record with the relation permitted")
	}

	archived := live
	archived.Row = view.Row{ID: "alpha", Cells: view.Cells{"state": "archived"}}
	if archived.CanDelete() {
		t.Error("CanDelete() = true for an archived record")
	}
	if !archived.CanEdit() {
		t.Error("Applies withdrew the update relation too, which it was not asked to")
	}
}

// TestDetailModel_StreamLinkOnlyWhenDeclared.
func TestDetailModel_StreamLinkOnlyWhenDeclared(t *testing.T) {
	m := view.DetailModel{
		Page: view.PageModel{Prefix: "/ui"}, Descriptor: modelDescriptor(),
		Row: view.Row{ID: "alpha"},
	}
	if m.CanStream() {
		t.Error("CanStream() = true for a view declaring no stream")
	}

	m.Descriptor.Stream = &view.StreamSpec{Title: "Output", PathPattern: "/api/v1/jobs/{id}/logs"}
	if !m.CanStream() {
		t.Error("CanStream() = false for a view declaring a stream")
	}
	if got := m.LogsHref(); got != "/ui/widgets/alpha/logs" {
		t.Errorf("LogsHref() = %q", got)
	}
}

// TestStreamModel_URLEscapesTheIdentifier: the pattern exists precisely so
// the escape happens once, here, rather than in every resource author's
// hands.
func TestStreamModel_URLEscapesTheIdentifier(t *testing.T) {
	m := view.StreamModel{
		Page:       view.PageModel{Prefix: "/ui"},
		Descriptor: view.Descriptor{Name: "jobs", Stream: &view.StreamSpec{Title: "Output", PathPattern: "/api/v1/jobs/{id}/logs"}},
		ID:         "../../etc/passwd",
	}

	got := m.StreamURL()
	if strings.Contains(got, "..") && !strings.Contains(got, "%2F") {
		t.Errorf("StreamURL() = %q, which carries an unescaped traversal", got)
	}
	if !strings.HasPrefix(got, "/api/v1/jobs/") {
		t.Errorf("StreamURL() = %q, which escaped its own prefix", got)
	}

	// And a model whose descriptor declares no stream must produce nothing
	// rather than a URL with a literal {id} in it.
	none := view.StreamModel{Descriptor: view.Descriptor{Name: "jobs"}}
	if got := none.StreamURL(); got != "" {
		t.Errorf("StreamURL() with no stream = %q, want empty", got)
	}
}

// TestFormModel_DescribedByOmitsDanglingReferences. An aria-describedby
// pointing at nothing is worse than absent: the reference is announced and
// resolves to nothing.
func TestFormModel_DescribedByOmitsDanglingReferences(t *testing.T) {
	d := modelDescriptor()
	m := view.FormModel{Page: view.PageModel{Prefix: "/ui"}, Descriptor: d, Errors: view.FieldErrors{}}

	name, notes := d.Fields[0], d.Fields[2]

	if got := m.DescribedBy(name); got != m.HintID(name) {
		t.Errorf("DescribedBy for a field with only a hint = %q, want %q", got, m.HintID(name))
	}
	if got := m.DescribedBy(notes); got != "" {
		t.Errorf("DescribedBy for a field with neither hint nor error = %q, want empty", got)
	}

	m.Errors = view.FieldErrors{"name": {"is required"}}
	got := m.DescribedBy(name)
	if !strings.Contains(got, m.HintID(name)) || !strings.Contains(got, m.ErrorID(name)) {
		t.Errorf("DescribedBy with both a hint and an error = %q, want both ids", got)
	}
	if m.Invalid(name) != "true" {
		t.Error("Invalid() did not report the field with an error")
	}
	if m.Invalid(notes) != "false" {
		t.Error("Invalid() reported a field with no error")
	}
}

// TestFormModel_SummaryFollowsDeclarationOrder, because a summary that
// reshuffles between two submissions is one a screen reader user has to
// re-read from the top every time.
func TestFormModel_SummaryFollowsDeclarationOrder(t *testing.T) {
	d := modelDescriptor()
	m := view.FormModel{
		Descriptor: d,
		Errors:     view.FieldErrors{"notes": {"too long"}, "name": {"is required"}},
	}

	summary := m.Summary()
	if len(summary) != 2 {
		t.Fatalf("Summary() returned %d entries, want 2", len(summary))
	}
	if summary[0].Field != "name" || summary[1].Field != "notes" {
		t.Errorf("Summary() = %v, want declaration order (name, notes)", summary)
	}
	if summary[0].Label != "NAME" {
		t.Errorf("Summary() entry carries label %q, want the field's own label", summary[0].Label)
	}
}

// TestFormModel_ActionAndHeadingFollowTheIdentifier, so there is no separate
// "mode" field that could disagree with whether an id is present.
func TestFormModel_ActionAndHeadingFollowTheIdentifier(t *testing.T) {
	create := view.FormModel{Page: view.PageModel{Prefix: "/ui"}, Descriptor: modelDescriptor()}
	if create.Editing() {
		t.Error("Editing() = true with no id")
	}
	if got := create.Action(); got != "/ui/widgets" {
		t.Errorf("create Action() = %q", got)
	}
	if !strings.HasPrefix(create.Heading(), "New ") {
		t.Errorf("create Heading() = %q", create.Heading())
	}

	edit := create
	edit.ID = "alpha"
	if !edit.Editing() {
		t.Error("Editing() = false with an id")
	}
	if got := edit.Action(); got != "/ui/widgets/alpha" {
		t.Errorf("edit Action() = %q", got)
	}
	if !strings.HasPrefix(edit.Heading(), "Edit ") {
		t.Errorf("edit Heading() = %q", edit.Heading())
	}
	if got := edit.CancelHref(); got != "/ui/widgets/alpha" {
		t.Errorf("edit CancelHref() = %q, want the record it came from", got)
	}
}

// TestPageModel_CSRFHeadersAreJSONEncoded. The value is an HMAC in base64url
// and hand-building JSON around caller-adjacent data is how an injection
// gets written.
func TestPageModel_CSRFHeadersAreJSONEncoded(t *testing.T) {
	empty := view.PageModel{}
	if got := empty.CSRFHeaders(); got != "{}" {
		t.Errorf("CSRFHeaders() with no token = %q, want {}", got)
	}

	// A token containing characters with meaning in JSON must come back
	// escaped rather than breaking out of the attribute.
	p := view.PageModel{CSRFToken: `a"b\c`}
	got := p.CSRFHeaders()
	if strings.Contains(got, `"a"b`) {
		t.Errorf("CSRFHeaders() = %q, which did not escape the token", got)
	}
	if !strings.Contains(got, "X-CSRF-Token") {
		t.Errorf("CSRFHeaders() = %q, want the header name", got)
	}
}

// TestPageModel_SignedInGatesTheLogoutControl, since the login page shares
// this chrome and must not offer to sign out of nothing.
func TestPageModel_SignedInGatesTheLogoutControl(t *testing.T) {
	if (view.PageModel{}).SignedIn() {
		t.Error("SignedIn() = true with no subject")
	}
	if !(view.PageModel{Subject: "someone"}).SignedIn() {
		t.Error("SignedIn() = false with a subject")
	}
	if got := (view.PageModel{Prefix: "/ui"}).LogoutAction(); got != "/ui/logout" {
		t.Errorf("LogoutAction() = %q", got)
	}
}

// TestPageModel_ThemeOptionsMarkExactlyOnePressed, across all three states
// including the default.
func TestPageModel_ThemeOptionsMarkExactlyOnePressed(t *testing.T) {
	for _, theme := range []string{"", "light", "dark", "not-a-theme"} {
		p := view.PageModel{Theme: theme}

		pressed := 0
		for _, opt := range p.ThemeOptions() {
			if opt.Pressed == "true" {
				pressed++
			}
		}
		if pressed != 1 {
			t.Errorf("theme %q marked %d options pressed, want exactly 1", theme, pressed)
		}
	}
}

// TestPageModel_SkinOptionsMarkExactlyOnePressed, and an unrecognised skin
// falls back to the default rather than leaving nothing selected.
func TestPageModel_SkinOptionsMarkExactlyOnePressed(t *testing.T) {
	for _, skin := range []string{"", "brutalist", "las-ventanas", "nonsense"} {
		p := view.PageModel{Skin: skin}

		pressed := 0
		for _, opt := range p.SkinOptions() {
			if opt.Pressed == "true" {
				pressed++
			}
		}
		if pressed != 1 {
			t.Errorf("skin %q marked %d options pressed, want exactly 1", skin, pressed)
		}
	}
}

// TestPageModel_A11yToggleSubmitsTheOppositeState.
func TestPageModel_A11yToggleSubmitsTheOppositeState(t *testing.T) {
	off := view.PageModel{}
	if off.A11yNext() != "on" || off.AccessibleMode() != "false" {
		t.Errorf("with a11y off: next=%q pressed=%q", off.A11yNext(), off.AccessibleMode())
	}

	on := view.PageModel{A11y: true}
	if on.A11yNext() != "off" || on.AccessibleMode() != "true" {
		t.Errorf("with a11y on: next=%q pressed=%q", on.A11yNext(), on.AccessibleMode())
	}
}

// TestBuildNav_FiltersAndOrders. Deciding what an identity may do is the
// authorization chain's job, so this only proves the filter is applied and
// the order is stable.
func TestBuildNav_FiltersAndOrders(t *testing.T) {
	descriptors := []view.Descriptor{
		{Name: "zulu", NavLabel: "ZULU"},
		{Name: "alpha", NavLabel: "ALPHA"},
	}

	all := view.BuildNav("/ui", "alpha", descriptors, nil)
	if len(all) != 2 {
		t.Fatalf("BuildNav with no filter returned %d items, want 2", len(all))
	}
	if all[0].Href != "/ui/zulu" {
		t.Errorf("BuildNav reordered its input: %v", all)
	}
	if !all[1].Current {
		t.Error("BuildNav did not mark the current view")
	}

	filtered := view.BuildNav("/ui", "", descriptors, func(d view.Descriptor) bool {
		return d.Name == "alpha"
	})
	if len(filtered) != 1 || filtered[0].Label != "ALPHA" {
		t.Errorf("BuildNav did not apply its filter: %v", filtered)
	}
}

// TestChartData_TotalSumsEveryBucket, so the figures a reader is given add
// up to something they can check.
func TestChartData_TotalSumsEveryBucket(t *testing.T) {
	data := view.ChartData{Buckets: []view.ChartBucket{
		{Label: "Completed", Count: 12, Class: "badge-ok"},
		{Label: "Failed", Count: 3, Class: "badge-failed"},
		{Label: "Pending", Count: 0, Class: "badge-skipped"},
	}}

	if got := data.Total(); got != 15 {
		t.Errorf("Total() = %d, want 15", got)
	}
	if got := (view.ChartData{}).Total(); got != 0 {
		t.Errorf("Total() of no buckets = %d, want 0", got)
	}
}

// TestListModel_ChartAccessorsDegradeWithoutAChart, since every one of them
// is reached by a template that has already asked HasChart.
func TestListModel_ChartAccessorsDegradeWithoutAChart(t *testing.T) {
	m := listModel()
	if m.HasChart() {
		t.Fatal("HasChart() = true for a descriptor with no chart")
	}
	if m.ChartTitle() != "" || m.ChartCaption() != "" {
		t.Error("the chart accessors returned text for a view with no chart")
	}
	if got := m.ChartDataHref(); got != "/ui/widgets/chart.json" {
		t.Errorf("ChartDataHref() = %q", got)
	}
	if got := m.ChartBucketClass(view.ChartBucket{Class: "invented"}); got != "badge-neutral" {
		t.Errorf("ChartBucketClass for an invented class = %q, want badge-neutral", got)
	}
}
