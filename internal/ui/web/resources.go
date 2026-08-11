package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strconv"

	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/render"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
	"github.com/go-chi/chi/v5"
)

// defaultPageSize bounds a rendered list. It is the server's, not the
// caller's: a page size read from a query parameter with no cap is a
// request to hold an entire fleet in memory.
const defaultPageSize = 50

// resolve loads the descriptor for a request and enforces its scope,
// answering the caller itself when either fails.
//
// Scope comes from the endpoint the descriptor names, so the UI never
// declares an authorization requirement of its own -- it reads the one the
// router already enforces on the JSON route for the same operation.
func (h *Handler) resolve(w http.ResponseWriter, r *http.Request, op func(view.Ops) *apispec.Endpoint) (view.Descriptor, bool) {
	d, ok := h.resourceOf(r)
	if !ok {
		h.notFound(w, r)
		return view.Descriptor{}, false
	}

	// A declared view is answered before any scope is looked for, because
	// it has no endpoint to read one from -- that is what being declared
	// means. Falling through would 404 it, which is the one answer that
	// makes a registered-but-unimplemented view indistinguishable from a
	// view that does not exist, and the whole reason it is registered is
	// to say out loud which of those it is. Nothing is disclosed: the
	// panel says only that the view is not implemented, which the
	// navigation already showed.
	if !d.Implemented() {
		h.renderDeclared(w, r, d)
		return view.Descriptor{}, false
	}

	endpoint := op(d.Ops)
	if endpoint == nil {
		// The descriptor does not offer this operation at all, which is a
		// different thing from the caller not being allowed to perform
		// it, and 404 is the honest answer.
		h.notFound(w, r)
		return view.Descriptor{}, false
	}

	if !h.permits(r.Context(), identityFrom(r.Context()), endpoint.Scope) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return view.Descriptor{}, false
	}
	return d, true
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	d, ok := h.resolve(w, r, func(o view.Ops) *apispec.Endpoint { return o.List })
	if !ok {
		return
	}
	// A chart-only view is a finished shape, not an unfinished one, so
	// "declared" is decided by status and by having nothing at all to
	// render -- never by the absence of a table.
	if !d.Implemented() || (!d.ListsRecords() && d.Chart == nil) {
		h.renderDeclared(w, r, d)
		return
	}

	limit := defaultPageSize
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}

	var page view.RowPage
	if d.ListsRecords() {
		var err error
		page, err = d.Handlers.List(r.Context(), view.Query{
			Cursor: r.URL.Query().Get("after"),
			Limit:  limit,
			Search: r.URL.Query().Get("q"),
		})
		if err != nil {
			h.serverError(w, r, "list "+d.Name, err)
			return
		}
	}

	chrome := h.page(r, d.Title, d.Name)
	// Setting this is what pulls the charting bundle onto the page, so a
	// list with no chart never downloads it.
	chrome.Chart = d.Chart

	model := view.ListModel{
		Page:       chrome,
		Descriptor: d,
		Rows:       page.Rows,
		NextCursor: page.NextCursor,
		Aff:        h.affordances(r.Context(), identityFrom(r.Context()), d),
	}

	// The chart's table equivalent is rendered server-side rather than
	// fetched, so the numbers are in the HTML a reader receives. A table
	// that only materialises once a script has run is a table that is
	// missing for exactly the readers who most need it.
	if d.Chart != nil {
		data, err := d.Chart.Data(r.Context())
		if err != nil {
			// A failed aggregate does not cost the list its rows. The
			// section renders empty and the log carries the cause, which
			// is a better answer than a 500 over a summary panel.
			h.cfg.Logger.ErrorContext(r.Context(), "failed to resolve chart data",
				slog.String("resource", d.Name), slog.String("error", err.Error()))
		} else {
			model.Chart = data
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := render.List(model).Render(r.Context(), w); err != nil {
		h.serverError(w, r, "render list", err)
	}
}

// chartData serves a chart's aggregates as domain JSON.
//
// It lives on the UI subtree rather than in apispec because it serves this
// UI's own summary, not a public contract. What it returns is deliberately
// domain shaped -- labels and counts -- and never an ECharts option
// document: encoding a charting library's schema into a server response is
// a seam that can never be changed afterwards, since swapping the library
// would become an API break.
func (h *Handler) chartData(w http.ResponseWriter, r *http.Request) {
	// Resolved here rather than through the shared helper, because this
	// route serves JSON and the shared helper answers a declared view with
	// an HTML panel. A 200 carrying a page of markup to something that
	// asked for chart data is worse than a 404: a caller parses it, fails,
	// and reports that the chart is broken rather than that it does not
	// exist.
	d, ok := h.resourceOf(r)
	if !ok || d.Chart == nil {
		h.notFound(w, r)
		return
	}
	if !h.permits(r.Context(), identityFrom(r.Context()), d.Ops.List.Scope) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	data, err := d.Chart.Data(r.Context())
	if err != nil {
		h.serverError(w, r, "chart data for "+d.Name, err)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		h.cfg.Logger.ErrorContext(r.Context(), "failed to write chart data",
			slog.String("resource", d.Name), slog.String("error", err.Error()))
	}
}

// stream renders the live log page for one record.
//
// The page holds no log data itself. It carries the stream's URL in a data
// attribute and the browser's own EventSource connects to it, authenticated
// by the session cookie -- which is the entire reason this page can exist
// at all, since an EventSource cannot set an Authorization header and the
// API accepted nothing else before this phase.
func (h *Handler) stream(w http.ResponseWriter, r *http.Request) {
	d, ok := h.resolve(w, r, func(o view.Ops) *apispec.Endpoint { return o.Get })
	if !ok {
		return
	}
	if d.Stream == nil {
		h.notFound(w, r)
		return
	}

	id := chi.URLParam(r, "id")
	if d.Handlers != nil && d.Handlers.Get != nil {
		// Resolve the record first, so a stream page for something that
		// does not exist is a 404 rather than a page that connects and
		// then sits at "Connecting…" forever.
		if _, err := d.Handlers.Get(r.Context(), id); err != nil {
			h.notFound(w, r)
			return
		}
	}

	page := h.page(r, d.Title, d.Name)
	page.Stream = d.Stream

	model := view.StreamModel{
		Page:       page,
		Descriptor: d,
		ID:         id,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := render.Stream(model).Render(r.Context(), w); err != nil {
		h.serverError(w, r, "render stream", err)
	}
}

func (h *Handler) detail(w http.ResponseWriter, r *http.Request) {
	d, ok := h.resolve(w, r, func(o view.Ops) *apispec.Endpoint { return o.Get })
	if !ok {
		return
	}
	if !d.Implemented() || d.Handlers == nil || d.Handlers.Get == nil {
		h.renderDeclared(w, r, d)
		return
	}

	row, err := d.Handlers.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.notFound(w, r)
		return
	}

	model := view.DetailModel{
		Page:       h.page(r, d.Title, d.Name),
		Descriptor: d,
		Row:        row,
		Aff:        h.affordances(r.Context(), identityFrom(r.Context()), d),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := render.Detail(model).Render(r.Context(), w); err != nil {
		h.serverError(w, r, "render detail", err)
	}
}

func (h *Handler) newForm(w http.ResponseWriter, r *http.Request) {
	d, ok := h.resolve(w, r, func(o view.Ops) *apispec.Endpoint { return o.Create })
	if !ok {
		return
	}
	h.renderForm(w, r, d, "", map[string]string{}, view.FieldErrors{}, http.StatusOK)
}

func (h *Handler) editForm(w http.ResponseWriter, r *http.Request) {
	d, ok := h.resolve(w, r, func(o view.Ops) *apispec.Endpoint { return o.Update })
	if !ok {
		return
	}
	if d.Handlers == nil || d.Handlers.Form == nil {
		h.renderDeclared(w, r, d)
		return
	}

	id := chi.URLParam(r, "id")
	values, err := d.Handlers.Form(r.Context(), id)
	if err != nil {
		h.notFound(w, r)
		return
	}
	h.renderForm(w, r, d, id, values, view.FieldErrors{}, http.StatusOK)
}

// renderForm resolves every select's options before rendering, so no
// template performs I/O.
func (h *Handler) renderForm(w http.ResponseWriter, r *http.Request, d view.Descriptor,
	id string, values map[string]string, errs view.FieldErrors, status int) {

	options := map[string][]view.Option{}
	for _, f := range d.FormFields() {
		if f.Kind != view.KindSelect || f.Options == nil {
			continue
		}
		opts, err := f.Options(r.Context())
		if err != nil {
			h.serverError(w, r, "resolve options for "+f.Name, err)
			return
		}
		options[f.Name] = opts
	}

	model := view.FormModel{
		Page:       h.page(r, d.Title, d.Name),
		Descriptor: d,
		ID:         id,
		Values:     values,
		Errors:     errs,
		Options:    options,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := render.Form(model).Render(r.Context(), w); err != nil {
		h.serverError(w, r, "render form", err)
	}
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	d, ok := h.resolve(w, r, func(o view.Ops) *apispec.Endpoint { return o.Create })
	if !ok {
		return
	}
	if d.Handlers == nil || d.Handlers.Create == nil {
		h.renderDeclared(w, r, d)
		return
	}

	values, submitted, ok := h.formValues(w, r, d)
	if !ok {
		return
	}

	if errs := view.Validate(r.Context(), d.Fields, values); errs.Any() {
		// 422 rather than 200, so the status says what happened even
		// though the body is a form. The submitted values are redisplayed
		// rather than cleared: making someone retype a form to discover
		// what was wrong with it is its own accessibility problem.
		h.renderForm(w, r, d, "", submitted, errs, http.StatusUnprocessableEntity)
		return
	}

	id, errs, err := d.Handlers.Create(r.Context(), values)
	if err != nil {
		h.serverError(w, r, "create "+d.Name, err)
		return
	}
	if errs.Any() {
		h.renderForm(w, r, d, "", submitted, errs, http.StatusUnprocessableEntity)
		return
	}

	// See-other after a successful write, so a reload does not resubmit.
	h.redirect(w, r, resourcePath(h.cfg.Prefix, d.Name, id))
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	// A browser form can only GET or POST, so a delete arrives as a POST
	// carrying _method. It is read only after CSRF has already passed, so
	// it cannot be used to reach a write without a valid token.
	if r.FormValue("_method") == http.MethodDelete {
		h.destroy(w, r)
		return
	}

	d, ok := h.resolve(w, r, func(o view.Ops) *apispec.Endpoint { return o.Update })
	if !ok {
		return
	}
	if d.Handlers == nil || d.Handlers.Update == nil {
		h.renderDeclared(w, r, d)
		return
	}

	id := chi.URLParam(r, "id")
	values, submitted, ok := h.formValues(w, r, d)
	if !ok {
		return
	}

	if errs := view.Validate(r.Context(), d.Fields, values); errs.Any() {
		h.renderForm(w, r, d, id, submitted, errs, http.StatusUnprocessableEntity)
		return
	}

	errs, err := d.Handlers.Update(r.Context(), id, values)
	if err != nil {
		h.serverError(w, r, "update "+d.Name, err)
		return
	}
	if errs.Any() {
		h.renderForm(w, r, d, id, submitted, errs, http.StatusUnprocessableEntity)
		return
	}

	h.redirect(w, r, resourcePath(h.cfg.Prefix, d.Name, id))
}

func (h *Handler) destroy(w http.ResponseWriter, r *http.Request) {
	d, ok := h.resolve(w, r, func(o view.Ops) *apispec.Endpoint { return o.Delete })
	if !ok {
		return
	}
	if d.Handlers == nil || d.Handlers.Delete == nil {
		h.renderDeclared(w, r, d)
		return
	}

	if err := d.Handlers.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		h.serverError(w, r, "delete "+d.Name, err)
		return
	}
	h.redirect(w, r, path.Join(h.cfg.Prefix, d.Name))
}

// formValues parses a submission and narrows it to the declared fields.
//
// An undeclared field is refused rather than ignored. Silently dropping
// input a caller believed was accepted is how somebody ends up certain
// they changed something they did not, and it is also how a typo in a
// control name becomes a field that never saves.
func (h *Handler) formValues(w http.ResponseWriter, r *http.Request, d view.Descriptor) (view.Values, map[string]string, bool) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form submission", http.StatusBadRequest)
		return view.Values{}, nil, false
	}

	values, undeclared := view.NewValues(d.Fields, r.PostForm)
	if len(undeclared) > 0 {
		http.Error(w, "submission contains fields this resource does not declare", http.StatusBadRequest)
		return view.Values{}, nil, false
	}

	// Keep what was typed, for redisplay on a validation failure.
	submitted := make(map[string]string, len(d.Fields))
	for _, f := range d.FormFields() {
		submitted[f.Name] = values.Get(f.Name)
	}
	return values, submitted, true
}

// resourcePath builds a URL for one record, escaping the identifier.
//
// The escape is load-bearing rather than tidy: path.Join cleans its result,
// so an identifier of "../.." would not be a path segment, it would delete
// the prefix in front of it and send the caller somewhere else entirely.
// The view models escape for the same reason; this is the write path's
// equivalent, and gosec's taint analysis is what noticed it was missing.
func resourcePath(prefix, resource, id string) string {
	return path.Join(prefix, resource, url.PathEscape(id))
}

// redirect answers a successful write, honouring HTMX.
func (h *Handler) redirect(w http.ResponseWriter, r *http.Request, target string) {
	if wantsFragment(r) {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}
