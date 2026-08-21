package resources_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// This is the view conformance suite: one set of assertions driving every
// registered view through identical requests.
//
// Its purpose is the claim this phase was built to make good on -- that
// adding a resource is two files and costs nothing in correctness or
// accessibility. That claim is only true if the shared machinery is
// genuinely shared, and the only way to know is to drive every resource
// through the same assertions rather than testing each one's own happy
// path. A view joins by being registered; it does not join by having a test
// written for it, which is precisely the point.
//
// A resource author who ships an inaccessible table has to break one of
// these to do it.

// TestViewConformance_RegisteredAndReachable is the FAILURE_PATTERNS.md #52
// gate: a resource package that compiles, passes its own tests, and is
// invisible to the running binary because nothing imports it.
func TestViewConformance_RegisteredAndReachable(t *testing.T) {
	registerViews(t)

	want := []string{
		"credentials", "dashboard", "devices",
		"governance", "inventories", "jobs", "runbooks", "templates",
	}
	got := view.Names()

	registered := make(map[string]bool, len(got))
	for _, name := range got {
		registered[name] = true
	}

	for _, name := range want {
		if !registered[name] {
			t.Errorf("view %q is not registered.\n"+
				"A view package registers through internal/ui/resources/registrars.go, and a "+
				"package absent from that list is never reached, however complete it is. "+
				"Add it there.", name)
		}
	}
}

// TestViewConformance_EveryViewRendersItsList drives the list page of every
// registered view, implemented or declared, and holds all of them to the
// same accessibility assertions.
func TestViewConformance_EveryViewRendersItsList(t *testing.T) {
	h := newHarness(t, adminIdentity)

	for _, name := range view.Names() {
		t.Run(name, func(t *testing.T) {
			w := h.get(t, "/ui/"+name)
			if w.Code != http.StatusOK {
				t.Fatalf("GET /ui/%s = %d, want 200", name, w.Code)
			}
			assertAccessibleDocument(t, w.Body.String())
		})
	}
}

// TestViewConformance_PagingControlIsRenderedOnceAndOnlyWhenItLeadsSomewhere
// covers the two ways a paging control goes wrong, both of which shipped.
//
// A duplicate is a rendering fault: the link lives inside the swappable
// list region so a refresh carries it, and a copy left outside that region
// puts two on the page and leaves the outer one pointing at the cursor the
// page was first painted with.
//
// A control on a short list is a reader fault: setting the next cursor
// because a page came back non-empty offers a next page from every list
// that has any rows at all, including the last one. Following it reaches an
// empty table, which reads as data loss rather than as the end of a list.
//
// The two halves are checked against different authorities on purpose.
// Whether a next page exists is the reader's claim, so that half asks the
// reader directly rather than counting rendered rows, which would also
// count the header and any section tables on the page. Whether the control
// matches that claim is the template's job, so that half renders and
// counts. Asking one of them about both is how a page can faithfully
// render a lie.
//
// The duplicate is only observable on a list that genuinely has a second
// page, which is what limit=1 manufactures out of any fixture holding two
// records. Without that, the fixtures are small enough that no view offers
// a next page at all and a count of controls proves nothing.
func TestViewConformance_PagingControlIsRenderedOnceAndOnlyWhenItLeadsSomewhere(t *testing.T) {
	h := newHarness(t, adminIdentity)

	const control = ">Next page<"

	for _, name := range view.Names() {
		d, ok := view.Lookup(name)
		if !ok || !d.Implemented() || !d.ListsRecords() {
			continue
		}
		t.Run(name, func(t *testing.T) {
			// A page that did not fill cannot have another after it.
			// Claiming otherwise offers a link that reaches an empty table,
			// which reads as data loss rather than as the end of a list.
			full, err := d.Handlers.List(t.Context(), view.Query{Limit: view.DefaultPageSize})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			short := len(full.Rows) < view.DefaultPageSize
			if short && full.NextCursor != "" {
				t.Errorf("the reader returned %d rows against a limit of %d and still named a next "+
					"cursor %q", len(full.Rows), view.DefaultPageSize, full.NextCursor)
			}
			if n := strings.Count(h.get(t, "/ui/"+name).Body.String(), control); short && n != 0 {
				t.Errorf("%d paging control(s) rendered where the reader reports no next page", n)
			}

			// One record per page, so every record but the last leads
			// somewhere. Exactly one control: a copy rendered outside the
			// swappable list region would not follow a refresh, and would
			// go on pointing at the cursor the page was first painted with.
			one, err := d.Handlers.List(t.Context(), view.Query{Limit: 1})
			if err != nil {
				t.Fatalf("List(limit 1): %v", err)
			}
			if one.NextCursor == "" {
				t.Skipf("fixture holds under two records, so this view has no second page to render")
			}
			if n := strings.Count(h.get(t, "/ui/"+name+"?limit=1").Body.String(), control); n != 1 {
				t.Errorf("%d paging controls rendered where the reader reports a next page, want exactly 1", n)
			}
		})
	}
}

// TestViewConformance_ReferencesRenderNamesNotKeys is the guard on the
// defect this project shipped: a Teams list that read ORGANIZATION: 1.
//
// A cell holding a foreign key has not saved the reader a join, it has
// moved the join into their head and asked them to remember that
// organization 1 is Network. The check is deliberately crude, because the
// failure is crude: a referencing cell whose entire content parses as an
// integer is a primary key on a page, whatever it was meant to be.
//
// A blank fails too. "No organization" and "an organization whose name we
// did not load" are different facts and an empty cell says neither, which
// is the same ambiguity the declared-view panel exists to prevent.
func TestViewConformance_ReferencesRenderNamesNotKeys(t *testing.T) {
	// The harness is what registers the views; the assertions below read
	// the registry and the ports directly rather than rendering a page,
	// because the defect is in what the projector produces.
	newHarness(t, adminIdentity)

	for _, name := range view.Names() {
		d, ok := view.Lookup(name)
		if !ok || !d.Implemented() || !d.ListsRecords() {
			continue
		}
		referencing := make([]view.Field, 0, len(d.Fields))
		for _, f := range d.Fields {
			if f.Referencing() && f.InList {
				referencing = append(referencing, f)
			}
		}
		if len(referencing) == 0 {
			continue
		}

		t.Run(name, func(t *testing.T) {
			page, err := d.Handlers.List(t.Context(), view.Query{Limit: view.DefaultPageSize})
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(page.Rows) == 0 {
				t.Skip("fixture holds no rows, so no cell can be wrong")
			}

			for _, f := range referencing {
				for i, row := range page.Rows {
					cell := strings.TrimSpace(row.Cells[f.Name])
					if cell == "" {
						t.Errorf("row %d field %q references %q and renders empty", i, f.Name, f.References)
						continue
					}
					if _, err := strconv.Atoi(cell); err == nil {
						t.Errorf("row %d field %q renders %q, which is a primary key: reference %q by name",
							i, f.Name, cell, f.References)
					}
				}
			}
		})
	}
}

// TestViewConformance_ReferencedViewsExist is the startup refusal, asserted
// as a test so a dangling reference fails a build rather than a page.
//
// CheckReferences runs in RegisterAll, which the harness already calls, so
// reaching this line at all proves it passed. It is written out explicitly
// because a check that only runs as a side effect of another call is one
// somebody will later move without noticing what it was doing.
func TestViewConformance_ReferencedViewsExist(t *testing.T) {
	newHarness(t, adminIdentity)

	if err := view.CheckReferences(); err != nil {
		t.Fatalf("CheckReferences: %v", err)
	}
}

// TestViewConformance_SidebarOrderIsDeclaredNotAccidental asserts no two
// views claim the same NavOrder.
//
// A duplicate does not fail anything: Nav breaks the tie on Name, so the
// sidebar still renders, in an order neither view asked for and neither
// author can see is wrong. That is the whole hazard. The numbers live in
// thirteen separate files, so the only place the collision is visible is
// here, over the real registry, after every view has registered.
//
// A test rather than a startup refusal, unlike CheckReferences. A dangling
// reference renders a link to nothing; a duplicate order renders a working
// menu in an arbitrary sequence, and refusing to boot over that would take
// the whole control plane down for a cosmetic fault.
func TestViewConformance_SidebarOrderIsDeclaredNotAccidental(t *testing.T) {
	registerViews(t)

	byOrder := map[int]string{}
	for _, name := range view.Names() {
		d, ok := view.Lookup(name)
		if !ok {
			continue
		}
		if other, clash := byOrder[d.NavOrder]; clash {
			t.Errorf("views %q and %q both declare NavOrder %d, so the sidebar "+
				"orders them by name rather than by either author's intent",
				other, name, d.NavOrder)
			continue
		}
		byOrder[d.NavOrder] = name
	}
}

// TestViewConformance_DeclaredViewsSaySo asserts a declared view renders the
// honest panel rather than an empty table.
//
// An empty table and a not-implemented view look identical to a user, and
// the difference matters enormously: one means "nothing has happened yet"
// and the other means "this does not work". Shipping the first when you
// mean the second is a failure this project has made twice.
func TestViewConformance_DeclaredViewsSaySo(t *testing.T) {
	h := newHarness(t, adminIdentity)

	for _, name := range view.Names() {
		d, ok := view.Lookup(name)
		if !ok || d.Implemented() {
			continue
		}
		t.Run(name, func(t *testing.T) {
			body := h.get(t, "/ui/"+name).Body.String()
			if !strings.Contains(body, "Declared, not implemented") {
				t.Errorf("declared view %q does not say it is declared", name)
			}
			if strings.Contains(body, "<table") {
				t.Errorf("declared view %q renders a table, which is indistinguishable "+
					"from an implemented view with no records", name)
			}
		})
	}
}

// TestViewConformance_DeclaredViewsCarryNoHandlers is the structural half of
// the assertion above: a declared view must not secretly work.
func TestViewConformance_DeclaredViewsCarryNoHandlers(t *testing.T) {
	registerViews(t)

	for _, name := range view.Names() {
		d, ok := view.Lookup(name)
		if !ok || d.Implemented() {
			continue
		}
		if d.Handlers != nil {
			t.Errorf("declared view %q carries handlers, so it is not declared", name)
		}
		if d.Ops.Create != nil || d.Ops.Update != nil || d.Ops.Delete != nil {
			t.Errorf("declared view %q offers a write operation", name)
		}
	}
}

// TestViewConformance_DetailRendersForEveryReadableView opens one record of
// every view that has records, through the real detail route.
func TestViewConformance_DetailRendersForEveryReadableView(t *testing.T) {
	h := newHarness(t, adminIdentity)

	for _, name := range view.Names() {
		d, ok := view.Lookup(name)
		if !ok || !d.ListsRecords() {
			continue
		}
		t.Run(name, func(t *testing.T) {
			id := firstRecordID(t, h, name)
			if id == "" {
				t.Skipf("view %q rendered no records to open", name)
			}

			w := h.get(t, "/ui/"+name+"/"+id)
			if w.Code != http.StatusOK {
				t.Fatalf("GET /ui/%s/%s = %d, want 200", name, id, w.Code)
			}
			assertAccessibleDocument(t, w.Body.String())
		})
	}
}

// TestViewConformance_FormsRenderAccessibly drives the create form of every
// view that offers one.
//
// Forms are where accessibility is most often lost, so this is the assertion
// that earns the "a resource author cannot ship an inaccessible form" claim:
// every control labelled, every hint and error wired through
// aria-describedby, every required field marked in words rather than in
// punctuation alone.
func TestViewConformance_FormsRenderAccessibly(t *testing.T) {
	h := newHarness(t, adminIdentity)

	for _, name := range view.Names() {
		d, ok := view.Lookup(name)
		if !ok || d.Ops.Create == nil {
			continue
		}
		t.Run(name, func(t *testing.T) {
			w := h.get(t, "/ui/"+name+"/new")
			if w.Code != http.StatusOK {
				t.Fatalf("GET /ui/%s/new = %d, want 200", name, w.Code)
			}

			body := w.Body.String()
			assertAccessibleDocument(t, body)

			// A view that offers Create must have something to collect.
			//
			// This guard exists because the loop below is over FormFields,
			// and a view whose fields all forgot Field.InForm has none --
			// so every assertion in it passes vacuously and the suite
			// reports a form that renders no controls at all as conformant.
			// That is not hypothetical: it shipped exactly once, in the
			// Schedules view, whose eight fields declared InList and not
			// InForm (FAILURE_PATTERNS.md #167). The page returned 200 and
			// rendered a heading and a Save button over nothing.
			if len(d.FormFields()) == 0 {
				t.Fatalf("view %q offers a Create endpoint but declares no form fields; "+
					"every field is missing Field.InForm, so the form renders no controls", name)
			}

			// Every writable field must have rendered a control.
			for _, f := range d.FormFields() {
				if !strings.Contains(body, `name="`+f.Name+`"`) {
					t.Errorf("form for %q declares field %q but renders no control for it", name, f.Name)
				}
				if f.Required && !strings.Contains(body, "(required)") {
					t.Errorf("form for %q has a required field but no field is marked required in words", name)
				}
			}
		})
	}
}

// TestViewConformance_InvalidSubmissionIsRejectedWithFieldErrors asserts a
// failed submission comes back as 422 with a focusable error summary, and --
// the part that matters -- that nothing was written.
func TestViewConformance_InvalidSubmissionIsRejectedWithFieldErrors(t *testing.T) {
	h := newHarness(t, adminIdentity)

	for _, name := range view.Names() {
		d, ok := view.Lookup(name)
		if !ok || d.Ops.Create == nil || !hasRequiredField(d) {
			continue
		}
		t.Run(name, func(t *testing.T) {
			// Every required field left empty.
			w := h.post(t, "/ui/"+name, map[string]string{})
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("POST /ui/%s with an empty form = %d, want 422", name, w.Code)
			}

			body := w.Body.String()
			if !strings.Contains(body, `id="error-summary"`) {
				t.Error("a rejected submission renders no error summary")
			}
			if !strings.Contains(body, `role="alert"`) {
				t.Error("the error summary is not announced")
			}
			if !strings.Contains(body, `aria-invalid="true"`) {
				t.Error("no control is marked aria-invalid")
			}
			assertAccessibleDocument(t, body)
		})
	}
}

// TestViewConformance_UndeclaredFieldsAreRefused asserts over-posting is
// rejected outright rather than silently ignored.
//
// Silently dropping input a caller believed was accepted is how somebody
// ends up certain they changed something they did not.
func TestViewConformance_UndeclaredFieldsAreRefused(t *testing.T) {
	h := newHarness(t, adminIdentity)

	for _, name := range view.Names() {
		d, ok := view.Lookup(name)
		if !ok || d.Ops.Create == nil {
			continue
		}
		t.Run(name, func(t *testing.T) {
			w := h.post(t, "/ui/"+name, map[string]string{"totally_undeclared": "1"})
			if w.Code != http.StatusBadRequest {
				t.Errorf("POST /ui/%s with an undeclared field = %d, want 400", name, w.Code)
			}
		})
	}
}

// TestViewConformance_WritesRequireCSRF asserts every write refuses without
// a token, and asserts the side effect did not happen rather than only the
// status code.
func TestViewConformance_WritesRequireCSRF(t *testing.T) {
	h := newHarness(t, adminIdentity)

	for _, name := range view.Names() {
		d, ok := view.Lookup(name)
		if !ok || d.Ops.Create == nil {
			continue
		}
		t.Run(name, func(t *testing.T) {
			before := recordCount(t, h, name)

			w := h.postWithoutCSRF(t, "/ui/"+name, "name=csrf-probe&type=linux_server")
			if w.Code != http.StatusForbidden {
				t.Fatalf("POST /ui/%s with no CSRF token = %d, want 403", name, w.Code)
			}

			if after := recordCount(t, h, name); after != before {
				t.Errorf("a CSRF-rejected request still changed state: %d records before, %d after", before, after)
			}
		})
	}
}

// TestViewConformance_AffordancesMatchTheAPI is Phase 13's Release Gate,
// re-earned in HTML.
//
// The rels a page renders controls for must equal the rels the JSON API's
// own generator permits for the same identity over the same candidates. If
// these ever diverge, the UI has grown a second opinion about authorization,
// which is the exact thing this design exists to not have.
func TestViewConformance_AffordancesMatchTheAPI(t *testing.T) {
	for _, tc := range []struct {
		name     string
		identity *auth.Identity
	}{
		{"admin", adminIdentity},
		{"viewer", viewerIdentity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, tc.identity)

			for _, name := range view.Names() {
				d, ok := view.Lookup(name)
				if !ok || !d.Implemented() || d.Ops.Create == nil {
					continue
				}

				permitted := permittedRels(t, h, d)
				body := h.get(t, "/ui/"+name).Body.String()
				rendered := strings.Contains(body, `href="/ui/`+name+`/new"`)

				if want := permitted[d.Ops.Create.Rel]; rendered != want {
					t.Errorf("%s on view %q: create control rendered=%v, generator permitted=%v",
						tc.name, name, rendered, want)
				}
			}
		})
	}
}

// TestViewConformance_ViewerSeesNoDeleteControl is the concrete half of the
// assertion above. A permission test that only ever checks the permitted
// case proves nothing.
func TestViewConformance_ViewerSeesNoDeleteControl(t *testing.T) {
	viewer := newHarness(t, viewerIdentity)
	admin := newHarness(t, adminIdentity)

	const name = "inventories"
	id := firstRecordID(t, admin, name)
	if id == "" {
		t.Fatal("the fixture repository rendered no device to open")
	}

	adminBody := admin.get(t, "/ui/"+name+"/"+id).Body.String()
	if !strings.Contains(adminBody, "btn-danger") {
		t.Error("admin sees no delete control, so this test could not detect its absence for a viewer")
	}

	viewerResponse := viewer.get(t, "/ui/"+name+"/"+id)
	if viewerResponse.Code != http.StatusOK {
		t.Fatalf("viewer GET /ui/%s/%s = %d, want 200", name, id, viewerResponse.Code)
	}
	if strings.Contains(viewerResponse.Body.String(), "btn-danger") {
		t.Error("a viewer is offered a delete control")
	}
}

// TestViewConformance_ChartsCarryATextEquivalent asserts every chart ships
// its data as a table in the HTML, not only as a canvas a script fills in.
func TestViewConformance_ChartsCarryATextEquivalent(t *testing.T) {
	h := newHarness(t, adminIdentity)

	for _, name := range view.Names() {
		d, ok := view.Lookup(name)
		if !ok || d.Chart == nil {
			continue
		}
		t.Run(name, func(t *testing.T) {
			body := h.get(t, "/ui/"+name).Body.String()

			if !strings.Contains(body, "chart-table") {
				t.Error("a chart renders no table equivalent, so its data is unavailable " +
					"to anyone who cannot see the graphic")
			}
			if !strings.Contains(body, d.Chart.Caption) {
				t.Error("a chart renders no caption")
			}
			// The canvas is hidden from assistive technology precisely
			// because the table carries the same figures; without the
			// table that would be data hidden from a reader entirely.
			if !strings.Contains(body, `aria-hidden="true"`) {
				t.Error("the chart canvas is not hidden from assistive technology")
			}

			// And the endpoint the script fetches must actually serve.
			w := h.get(t, "/ui/"+name+"/chart.json")
			if w.Code != http.StatusOK {
				t.Fatalf("GET /ui/%s/chart.json = %d, want 200", name, w.Code)
			}
			if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("chart endpoint Content-Type = %q, want JSON", ct)
			}
		})
	}
}

// TestViewConformance_StreamPagesConnectSameOrigin asserts a declared stream
// resolves to a rooted path on this origin, since an EventSource pointed off
// origin is both a leak and a page that silently never connects.
func TestViewConformance_StreamPagesConnectSameOrigin(t *testing.T) {
	h := newHarness(t, adminIdentity)

	for _, name := range view.Names() {
		d, ok := view.Lookup(name)
		if !ok || d.Stream == nil {
			continue
		}
		t.Run(name, func(t *testing.T) {
			id := firstRecordID(t, h, name)
			if id == "" {
				t.Skipf("view %q rendered no records to stream", name)
			}

			w := h.get(t, "/ui/"+name+"/"+id+"/logs")
			if w.Code != http.StatusOK {
				t.Fatalf("GET /ui/%s/%s/logs = %d, want 200", name, id, w.Code)
			}

			body := w.Body.String()
			assertAccessibleDocument(t, body)

			url := attributeValue(body, "data-stream-url")
			switch {
			case url == "":
				t.Fatal("the stream page carries no data-stream-url")
			case !strings.HasPrefix(url, "/"):
				t.Errorf("stream URL %q is not a rooted path", url)
			case strings.HasPrefix(url, "//"):
				t.Errorf("stream URL %q is protocol-relative, so it can leave this origin", url)
			case !strings.Contains(url, id):
				t.Errorf("stream URL %q does not name the record being watched", url)
			}
		})
	}
}

// TestViewConformance_UnregisteredResourceIs404 asserts the {resource} path
// parameter reaches a registry lookup and nothing else.
func TestViewConformance_UnregisteredResourceIs404(t *testing.T) {
	h := newHarness(t, adminIdentity)

	for _, path := range []string{
		"/ui/no-such-view",
		"/ui/no-such-view/some-id",
		"/ui/no-such-view/chart.json",
	} {
		if w := h.get(t, path); w.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, w.Code)
		}
	}
}

// TestViewConformance_ChartlessViewsHaveNoChartEndpoint asserts the chart
// route 404s for a view that declares no chart, rather than serving an empty
// document that reads as "no data".
func TestViewConformance_ChartlessViewsHaveNoChartEndpoint(t *testing.T) {
	h := newHarness(t, adminIdentity)

	for _, name := range view.Names() {
		d, ok := view.Lookup(name)
		if !ok || d.Chart != nil {
			continue
		}
		if w := h.get(t, "/ui/"+name+"/chart.json"); w.Code == http.StatusOK {
			t.Errorf("view %q has no chart but GET /ui/%s/chart.json = 200", name, name)
		}
	}
}

// ---- helpers ----

func hasRequiredField(d view.Descriptor) bool {
	for _, f := range d.FormFields() {
		if f.Required {
			return true
		}
	}
	return false
}

// firstRecordID reads one record's id out of a rendered list, rather than
// reaching into a fixture. That keeps the suite honest about what the page
// actually renders: an id the list does not link to is an id no user could
// have clicked.
func firstRecordID(t *testing.T, h *harness, name string) string {
	t.Helper()

	body := h.get(t, "/ui/"+name).Body.String()
	prefix := `href="/ui/` + name + `/`

	// Every match is considered, not only the first. The create button
	// links to .../new and renders above the table, so taking the first
	// match would find that and conclude the list was empty.
	for rest := body; ; {
		idx := strings.Index(rest, prefix)
		if idx < 0 {
			return ""
		}
		rest = rest[idx+len(prefix):]

		end := strings.Index(rest, `"`)
		if end < 0 {
			return ""
		}
		id := rest[:end]
		if id != "new" && id != "chart.json" && !strings.Contains(id, "/") {
			return id
		}
	}
}

// recordCount counts rendered rows, which is what makes the CSRF assertion
// about a side effect rather than about a status code.
func recordCount(t *testing.T, h *harness, name string) int {
	t.Helper()
	return strings.Count(h.get(t, "/ui/"+name).Body.String(), "<tr>")
}

// permittedRels asks the same generator the page used, so the comparison is
// between two readings of one authority rather than between the page and a
// restatement of the rules.
func permittedRels(t *testing.T, h *harness, d view.Descriptor) map[auth.LinkRel]bool {
	t.Helper()

	permitted := map[auth.LinkRel]bool{}
	rels, err := h.generator.Permitted(t.Context(), h.identity, d.Ops.Candidates())
	if err != nil {
		t.Fatalf("Permitted() = %v, want nil", err)
	}
	for _, rel := range rels {
		permitted[rel] = true
	}
	return permitted
}

// attributeValue pulls one attribute's value out of rendered HTML.
func attributeValue(body, attr string) string {
	idx := strings.Index(body, attr+`="`)
	if idx < 0 {
		return ""
	}
	rest := body[idx+len(attr)+2:]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}
