package web

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

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
		// The cursor this page was read from, so an empty page can tell
		// "nothing exists" apart from "you have paged past the end".
		Cursor: r.URL.Query().Get("after"),
		// Rebuilt from the parsed values rather than echoed from
		// r.URL.RequestURI(), so a refresh carries the reader's narrowing
		// without reflecting whatever else was in the query string back
		// into an attribute.
		RefreshURL: h.refreshURL(r, d, limit),
		Aff:        h.affordances(r.Context(), identityFrom(r.Context()), d),
		// Empty parent: a collection page's sections hang off the
		// collection, not off any row of it.
		Sections: h.loadSections(r, d, ""),
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
	// Same URL, same read, same model: only the amount of surrounding page
	// differs. Content negotiation rather than a second route is what keeps
	// the promise that adding a view adds no routing.
	if wantsFragment(r) {
		if err := render.ListFragment(model).Render(r.Context(), w); err != nil {
			h.serverError(w, r, "render list fragment", err)
		}
		return
	}
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

// download serves one record in one of the forms its view declares.
//
// It resolves the descriptor itself rather than through the shared helper,
// for the reason chartData gives: this route does not serve HTML, and
// answering a caller that asked for a file with a page of markup is worse
// than a 404, because a browser saves it under the requested name and the
// operator opens a corrupt artefact instead of learning it does not exist.
//
// Every header is set before Write is called, which is what makes the
// Available gate load-bearing rather than decorative: once the first byte
// is written the status is committed, and a failure after that can only be
// logged. A partial file is the one outcome worth working to avoid here,
// since the caller has no way to tell one from a complete one.
func (h *Handler) download(w http.ResponseWriter, r *http.Request) {
	d, ok := h.resourceOf(r)
	if !ok || len(d.Downloads) == 0 {
		h.notFound(w, r)
		return
	}

	id := chi.URLParam(r, "id")
	format := chi.URLParam(r, "format")

	var spec *view.DownloadSpec
	for i := range d.Downloads {
		if d.Downloads[i].Name == format {
			spec = &d.Downloads[i]
			break
		}
	}
	if spec == nil {
		h.notFound(w, r)
		return
	}

	// The same scope reading the record needs. A download is a read of one
	// record in another encoding, so a caller who may not open the page
	// may not save it either, and gating it on anything else would make
	// the file the way around the page.
	if d.Ops.Get == nil || !h.permits(r.Context(), identityFrom(r.Context()), d.Ops.Get.Scope) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// Checked again here, not merely when the link was drawn. The page may
	// have been rendered while the record still had something to give --
	// a log retention window is the case that really expires -- and a link
	// followed afterwards must answer honestly rather than save an empty
	// file under a confident name.
	if !spec.Offers(r.Context(), id) {
		h.notFound(w, r)
		return
	}

	w.Header().Set("Content-Type", spec.ContentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// The filename is sanitised by FilenameFor rather than escaped,
	// because this is a header rather than a document: a quote or a
	// newline reaching here is a header-injection question.
	w.Header().Set("Content-Disposition", `attachment; filename="`+spec.FilenameFor(id)+`"`)

	if err := spec.Write(r.Context(), w, id); err != nil {
		// Nowhere to report it: the status went out with the first byte.
		// Logged with the format and the record so an operator who is
		// handed a short file has something to correlate it against.
		h.cfg.Logger.ErrorContext(r.Context(), "failed to write download",
			slog.String("resource", d.Name),
			slog.String("format", format),
			slog.String("error", err.Error()))
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

// lookupAction resolves the {action} path parameter against the descriptor
// and enforces the action's own scope.
//
// The scope comes from the endpoint the action names, exactly as it does
// for every CRUD operation, so an action is never gated by something the
// UI invented -- and an action nobody declared is a 404 rather than a
// route that falls through to something else.
func (h *Handler) lookupAction(w http.ResponseWriter, r *http.Request) (view.Descriptor, view.RecordAction, bool) {
	d, ok := h.resourceOf(r)
	if !ok {
		h.notFound(w, r)
		return view.Descriptor{}, view.RecordAction{}, false
	}
	if !d.Implemented() {
		h.renderDeclared(w, r, d)
		return view.Descriptor{}, view.RecordAction{}, false
	}

	name := chi.URLParam(r, "action")
	for _, a := range d.Actions {
		if a.Name != name {
			continue
		}
		if !h.permits(r.Context(), identityFrom(r.Context()), a.Endpoint.Scope) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return view.Descriptor{}, view.RecordAction{}, false
		}
		return d, a, true
	}

	h.notFound(w, r)
	return view.Descriptor{}, view.RecordAction{}, false
}

// actionForm renders an action's prompt, or runs it straight away when it
// has nothing to prompt for.
func (h *Handler) actionForm(w http.ResponseWriter, r *http.Request) {
	d, action, ok := h.lookupAction(w, r)
	if !ok {
		return
	}
	if !action.Prompts() {
		// An action with no prompt is still a state change, so it must not
		// happen on a GET. Redirect to the record and let the rendered
		// button post; a GET that ran a job is one a link prefetcher or a
		// corporate scanner would eventually run for somebody.
		h.redirect(w, r, resourcePath(h.cfg.Prefix, d.Name, chi.URLParam(r, "id")))
		return
	}
	id := chi.URLParam(r, "id")
	fields, ok := h.actionFields(w, r, d, action, id)
	if !ok {
		return
	}
	values, ok := h.actionValues(w, r, d, action, id, fields)
	if !ok {
		return
	}
	h.renderAction(w, r, d, action, id, fields, values, view.FieldErrors{}, http.StatusOK)
}

// actionValues resolves an action's prefill for one record and checks it
// against the controls the form will draw.
//
// A failure here fails the request rather than rendering the form empty,
// for the reason actionFields fails rather than rendering no controls, and
// with more at stake. An empty prompt is a form that does nothing; a
// SILENTLY empty prefill is a form that looks like the record's current
// state, is not, and writes its blanks over what was stored the moment
// somebody presses the button they were offered.
func (h *Handler) actionValues(w http.ResponseWriter, r *http.Request, d view.Descriptor,
	action view.RecordAction, id string, fields []view.Field) (map[string]string, bool) {

	values, err := action.ResolveValues(r.Context(), id)
	if err != nil {
		h.serverError(w, r, "resolve prefill for "+d.Name+"/"+action.Name, err)
		return nil, false
	}
	if err := view.NarrowPrefill(fields, false, values); err != nil {
		h.serverError(w, r, "prefill for "+d.Name+"/"+action.Name, err)
		return nil, false
	}
	return values, true
}

// actionFields resolves an action's prompt for one record.
//
// A failure here fails the request rather than rendering an empty form. The
// alternative is a launch form with no controls, which is not "this
// template opens nothing" but "we could not find out", and the two must not
// look the same when the button underneath runs production work.
func (h *Handler) actionFields(w http.ResponseWriter, r *http.Request, d view.Descriptor,
	action view.RecordAction, id string) ([]view.Field, bool) {

	fields, err := action.ResolveFields(r.Context(), id)
	if err != nil {
		h.serverError(w, r, "resolve fields for "+d.Name+"/"+action.Name, err)
		return nil, false
	}
	return fields, true
}

// renderAction resolves every select's options before rendering, so no
// template performs I/O.
func (h *Handler) renderAction(w http.ResponseWriter, r *http.Request, d view.Descriptor,
	action view.RecordAction, id string, fields []view.Field, values map[string]string,
	errs view.FieldErrors, status int) {

	options := map[string][]view.Option{}
	for _, f := range fields {
		if !f.OffersChoices() || f.Options == nil {
			continue
		}
		opts, err := f.Options(r.Context())
		if err != nil {
			h.serverError(w, r, "resolve options for "+f.Name, err)
			return
		}
		options[f.Name] = opts
	}

	model := view.ActionModel{
		Page:       h.page(r, d.Title, d.Name),
		Descriptor: d,
		Action:     action,
		ID:         id,
		Fields:     fields,
		Values:     values,
		Errors:     errs,
		Options:    options,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := render.Action(model).Render(r.Context(), w); err != nil {
		h.serverError(w, r, "render action", err)
	}
}

// runAction performs a record action.
func (h *Handler) runAction(w http.ResponseWriter, r *http.Request) {
	d, action, ok := h.lookupAction(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")

	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form submission", http.StatusBadRequest)
		return
	}

	// Narrowed to this record's own prompt, so an action cannot be steered
	// by a parameter it never asked for -- the same reason a create
	// submission is narrowed to its resource's fields. For a launch that
	// means the template's locked fields are refused rather than merely
	// ignored: the form never offered them, so a submission carrying one
	// was not produced by the form.
	fields, ok := h.actionFields(w, r, d, action, id)
	if !ok {
		return
	}

	// Never editing: an action prompt is not an edit form. It carries the
	// action's own resolved fields rather than the resource's, and each of
	// them is answered afresh every time the action runs.
	values, undeclared := view.NewValues(fields, r.PostForm, false)
	if len(undeclared) > 0 {
		http.Error(w, "submission contains fields this action does not declare", http.StatusBadRequest)
		return
	}

	submitted := make(map[string]string, len(fields))
	for _, f := range fields {
		submitted[f.Name] = values.Get(f.Name)
	}

	if errs := view.Validate(r.Context(), fields, values); errs.Any() {
		h.renderAction(w, r, d, action, id, fields, submitted, errs, http.StatusUnprocessableEntity)
		return
	}

	redirect, errs, err := action.Submit(r.Context(), id, values)
	if err != nil {
		h.serverError(w, r, "run "+d.Name+"/"+action.Name, err)
		return
	}
	if errs.Any() {
		h.renderAction(w, r, d, action, id, fields, submitted, errs, http.StatusUnprocessableEntity)
		return
	}

	if redirect == "" {
		redirect = resourcePath(h.cfg.Prefix, d.Name, id)
	}
	h.redirect(w, r, redirect)
}

// lookupRowAction resolves the descriptor, the control and the two ids a
// row action is addressed by, enforcing the control's scope on the way.
//
// Shared by the GET that draws the form and the POST that runs it, so the
// two cannot come to different conclusions about what the URL named or who
// is allowed to reach it.
func (h *Handler) lookupRowAction(w http.ResponseWriter, r *http.Request) (view.Descriptor, view.RowAction, string, string, bool) {
	d, ok := h.resourceOf(r)
	if !ok {
		h.notFound(w, r)
		return view.Descriptor{}, view.RowAction{}, "", "", false
	}
	if !d.Implemented() {
		h.renderDeclared(w, r, d)
		return view.Descriptor{}, view.RowAction{}, "", "", false
	}

	action, found := d.RowAction(chi.URLParam(r, "action"))
	if !found {
		h.notFound(w, r)
		return view.Descriptor{}, view.RowAction{}, "", "", false
	}
	if !h.permits(r.Context(), identityFrom(r.Context()), action.Endpoint.Scope) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return view.Descriptor{}, view.RowAction{}, "", "", false
	}

	id, row := chi.URLParam(r, "id"), chi.URLParam(r, "row")
	if row == "" {
		h.notFound(w, r)
		return view.Descriptor{}, view.RowAction{}, "", "", false
	}
	return d, action, id, row, true
}

// rowActionForm renders a row control's prompt, prefilled from the row.
//
// A control that does not prompt answers 404 rather than redirecting, which
// is where this differs from actionForm one segment up. A record action's
// control is a link in a header, so a caller can genuinely arrive at its
// URL by clicking and redirecting them to the record is a kindness. A non
// prompting row control is a form button and never a link, so nothing on
// any page draws a GET here: one that arrives was typed, prefetched or
// scanned. There is no page at this address, and saying so is the honest
// answer.
func (h *Handler) rowActionForm(w http.ResponseWriter, r *http.Request) {
	d, action, id, row, ok := h.lookupRowAction(w, r)
	if !ok {
		return
	}
	if !action.Prompts() {
		h.notFound(w, r)
		return
	}

	fields := action.Fields
	values, err := action.ResolveValues(r.Context(), id, row)
	if err != nil {
		h.rowActionFailed(w, r, d, action, id, err)
		return
	}
	// Narrowed against the EDIT set, because that is what this form draws:
	// the control naming the row is Immutable, offered by the add form
	// beside this one and withheld here, so a prefill naming it would be a
	// value nothing renders.
	if err := view.NarrowPrefill(fields, true, values); err != nil {
		h.serverError(w, r, "prefill for "+d.Name+"/"+action.Name, err)
		return
	}
	h.renderRowAction(w, r, d, action, id, row, fields, values, view.FieldErrors{}, http.StatusOK)
}

// renderRowAction resolves every select's options before rendering, so no
// template performs I/O. It is renderAction's twin and differs only in
// carrying the row.
func (h *Handler) renderRowAction(w http.ResponseWriter, r *http.Request, d view.Descriptor,
	action view.RowAction, id, row string, fields []view.Field, values map[string]string,
	errs view.FieldErrors, status int) {

	options := map[string][]view.Option{}
	for _, f := range fields {
		if !f.OffersChoices() || f.Options == nil {
			continue
		}
		opts, err := f.Options(r.Context())
		if err != nil {
			h.serverError(w, r, "resolve options for "+f.Name, err)
			return
		}
		options[f.Name] = opts
	}

	model := view.ActionModel{
		Page:       h.page(r, d.Title, d.Name),
		Descriptor: d,
		// A synthesized RecordAction carrying this control's own label and
		// heading, because ActionModel renders one form and a row control
		// differs from a record action only in what it is addressed by.
		Action:  view.RecordAction{Name: action.Name, Label: action.Label, Heading: action.Heading},
		ID:      id,
		Row:     row,
		Fields:  fields,
		Values:  values,
		Errors:  errs,
		Options: options,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := render.Action(model).Render(r.Context(), w); err != nil {
		h.serverError(w, r, "render row action", err)
	}
}

// runRowAction performs a row action: one control on one row of one of the
// record's sections.
//
// The gates are the record action path's, in the same order and for the same
// reasons. RowAction.Applies is deliberately NOT re-consulted here, exactly
// as lookupAction does not re-consult Descriptor.Applies: a row can stop
// qualifying between the page rendering and the button being pressed, so a
// check here would narrow that race without closing it, and a caller who
// posts the URL by hand never passed through the renderer at all. Submit is
// the authority, and a row action whose Submit trusts the control it was
// reached from is wrong however many times this handler asks.
func (h *Handler) runRowAction(w http.ResponseWriter, r *http.Request) {
	d, action, id, row, ok := h.lookupRowAction(w, r)
	if !ok {
		return
	}

	var values view.Values
	fields := action.Fields
	if action.Prompts() {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "malformed form submission", http.StatusBadRequest)
			return
		}
		// editing true: the row exists, so an Immutable control was never
		// drawn and a submission carrying one did not come from this form.
		var undeclared []string
		values, undeclared = view.NewValues(fields, r.PostForm, true)
		if len(undeclared) > 0 {
			http.Error(w, "submission contains fields this action does not declare", http.StatusBadRequest)
			return
		}
		if errs := view.Validate(r.Context(), fields, values); errs.Any() {
			h.renderRowAction(w, r, d, action, id, row, fields, submittedValues(fields, values), errs,
				http.StatusUnprocessableEntity)
			return
		}
	}

	redirect, errs, err := action.Submit(r.Context(), id, row, values)
	if err != nil {
		h.rowActionFailed(w, r, d, action, id, err)
		return
	}
	if errs.Any() {
		h.renderRowAction(w, r, d, action, id, row, fields, submittedValues(fields, values), errs,
			http.StatusUnprocessableEntity)
		return
	}

	if redirect == "" {
		redirect = resourcePath(h.cfg.Prefix, d.Name, id)
	}
	h.redirect(w, r, redirect)
}

// submittedValues echoes a failed submission back to the form, so a
// redisplay shows what was typed rather than clearing it.
func submittedValues(fields []view.Field, values view.Values) map[string]string {
	out := make(map[string]string, len(fields))
	for _, f := range fields {
		out[f.Name] = values.Get(f.Name)
	}
	return out
}

// rowActionFailed answers a row control's error, separating a refusal the
// operator can act on from a fault that is nobody's doing.
func (h *Handler) rowActionFailed(w http.ResponseWriter, r *http.Request, d view.Descriptor,
	action view.RowAction, id string, err error) {

	var refused view.Refused
	if errors.As(err, &refused) {
		// A rule the operator can satisfy, answered in the store's own
		// words rather than logged where they cannot see it. 422 rather
		// than 500, the same status a form's validation failure carries.
		h.renderNotice(w, r, d, id, action.Label+" was refused", refused.Message)
		return
	}
	h.serverError(w, r, "run "+d.Name+"/"+action.Name, err)
}

// renderNotice answers a refused write with the reason, in the operator's
// own terms and on a page they can get back from.
func (h *Handler) renderNotice(w http.ResponseWriter, r *http.Request, d view.Descriptor, id, heading, body string) {
	model := view.NoticeModel{
		Page:       h.page(r, d.Title, d.Name),
		Descriptor: d,
		ID:         id,
		Heading:    heading,
		Body:       body,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnprocessableEntity)
	if err := render.Notice(model).Render(r.Context(), w); err != nil {
		h.serverError(w, r, "render notice", err)
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
		Sections:   h.loadSections(r, d, chi.URLParam(r, "id")),
		// Unvalidated on purpose: this arrives from the query string, and
		// DetailModel.CurrentTab is the one place that decides what an
		// unrecognised value means. Sanitising it twice would give two
		// answers to that question.
		Tab: r.URL.Query().Get("tab"),
	}

	// Resolved here rather than inside Chrome(), which has no context and
	// must not acquire one: deciding whether a download exists can mean
	// asking the broker how much of a job's output it still holds.
	model.Downloads = model.ResolveDownloads(r.Context())

	// A record page's title is the record, not the view it belongs to. The
	// browser tab is the one place a reader distinguishes eight open jobs
	// from each other, and "Jobs // Pleiades" eight times over does not.
	model.Page.Title = model.RecordName()

	// Only the Output tab reads a stream, so only it pulls the stream
	// script onto the page. Same argument as the charting bundle: code for
	// a thing this page does not do is code nobody asked for.
	if model.ShowStream() {
		model.Page.Stream = d.Stream
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if wantsFragment(r) {
		if err := render.DetailFragment(model).Render(r.Context(), w); err != nil {
			h.serverError(w, r, "render detail fragment", err)
		}
		return
	}
	if err := render.Detail(model).Render(r.Context(), w); err != nil {
		h.serverError(w, r, "render detail", err)
	}
}

// loadSections resolves every declared detail section before rendering.
//
// A section that fails to load is logged and rendered as its own empty
// state rather than failing the page. The record's own fields are the
// answer to "what is this", and losing them because a related table could
// not be read would turn a partial outage into a total one -- on the page
// somebody opened precisely because something is already wrong.
func (h *Handler) loadSections(r *http.Request, d view.Descriptor, id string) []view.LoadedSection {
	if len(d.Sections) == 0 {
		return nil
	}

	out := make([]view.LoadedSection, 0, len(d.Sections))
	for _, spec := range d.Sections {
		if !spec.Implemented() {
			// A declared section reaches no port, so there is nothing to
			// load and nothing that could fail. It renders the same honest
			// panel a declared view does.
			out = append(out, view.LoadedSection{Spec: spec})
			continue
		}

		rows, err := spec.Rows(r.Context(), id)
		if err != nil {
			h.cfg.Logger.ErrorContext(r.Context(), "failed to load detail section",
				slog.String("resource", d.Name),
				slog.String("section", spec.Title),
				slog.String("error", err.Error()))
			rows = nil
		}
		// Resolved after the rows, because the only thing a note has to
		// say so far is about the rows that were just loaded.
		var note string
		if spec.Note != nil {
			note = spec.Note(r.Context(), id)
		}
		out = append(out, view.LoadedSection{Spec: spec, Rows: rows, Note: note})
	}
	return out
}

func (h *Handler) newForm(w http.ResponseWriter, r *http.Request) {
	d, ok := h.resolve(w, r, func(o view.Ops) *apispec.Endpoint { return o.Create })
	if !ok {
		return
	}
	fields, ok := h.resolveFields(w, r, d, "")
	if !ok {
		return
	}
	// A create form prefills from the query string for the same reason
	// resolveFields reads it: the no-JavaScript path returns here with the
	// driving control's chosen value, and it has to come back selected or
	// the person has to choose it twice.
	h.renderForm(w, r, d, "", fields, drivingValues(r, fields), view.FieldErrors{}, http.StatusOK)
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

	fields, ok := h.resolveFields(w, r, d, id)
	if !ok {
		return
	}
	h.renderForm(w, r, d, id, fields, values, view.FieldErrors{}, http.StatusOK)
}

// renderForm resolves every select's options before rendering, so no
// template performs I/O.
//
// fields is this render's resolved control set: the descriptor's static
// form fields for a create, and ResolveFormFields' merged answer for an
// edit. It is a parameter rather than derived here from d and id, because
// the id alone would be ambiguous the moment a validation failure
// redisplays a create form (id is legitimately empty on both a create and
// an edit whose record could not be found) -- the caller already knows
// which mode it is in and resolved accordingly.
func (h *Handler) renderForm(w http.ResponseWriter, r *http.Request, d view.Descriptor,
	id string, fields []view.Field, values map[string]string, errs view.FieldErrors, status int) {

	options := map[string][]view.Option{}
	for _, f := range fields {
		if !f.OffersChoices() || f.Options == nil {
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
		FieldSet:   fields,
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

	fields, ok := h.resolveFields(w, r, d, "")
	if !ok {
		return
	}
	values, submitted, ok := h.formValues(w, r, fields, false)
	if !ok {
		return
	}

	if errs := view.Validate(r.Context(), fields, values); errs.Any() {
		// 422 rather than 200, so the status says what happened even
		// though the body is a form. The submitted values are redisplayed
		// rather than cleared: making someone retype a form to discover
		// what was wrong with it is its own accessibility problem.
		h.renderForm(w, r, d, "", fields, submitted, errs, http.StatusUnprocessableEntity)
		return
	}

	id, errs, err := d.Handlers.Create(r.Context(), values)
	if err != nil {
		h.serverError(w, r, "create "+d.Name, err)
		return
	}
	if errs.Any() {
		h.renderForm(w, r, d, "", fields, submitted, errs, http.StatusUnprocessableEntity)
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
	fields, ok := h.resolveFields(w, r, d, id)
	if !ok {
		return
	}

	values, submitted, ok := h.formValues(w, r, fields, true)
	if !ok {
		return
	}

	if errs := view.Validate(r.Context(), fields, values); errs.Any() {
		h.renderForm(w, r, d, id, fields, submitted, errs, http.StatusUnprocessableEntity)
		return
	}

	errs, err := d.Handlers.Update(r.Context(), id, values)
	if err != nil {
		h.serverError(w, r, "update "+d.Name, err)
		return
	}
	if errs.Any() {
		h.renderForm(w, r, d, id, fields, submitted, errs, http.StatusUnprocessableEntity)
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
//
// fields is the resolved control set the render side already agreed on --
// the descriptor's static fields for a create, ResolveFormFields' merged
// answer for an edit -- so the renderer and the narrower cannot disagree
// about what this submission was allowed to carry. editing selects which
// mode NewValues measures Immutable against; an immutable field posted to
// an update is therefore undeclared rather than quietly dropped.
// drivingValues prefills a create form from the query string, restricted to
// the fields the descriptor declares.
//
// It exists for the no-JavaScript path through a dependent form. Choosing a
// credential type submits a GET back to this same page carrying the choice,
// and without this the select would render unselected and the person would
// have to make the same choice twice to get past it. Only declared fields
// are read, so a crafted link cannot seed a control that does not exist,
// and this feeds a render rather than a write: the create itself reads
// r.PostForm through formValues and never sees any of this.
func drivingValues(r *http.Request, fields []view.Field) map[string]string {
	out := make(map[string]string, len(fields))
	if r.URL == nil {
		return out
	}
	q := r.URL.Query()
	for _, f := range fields {
		if v := strings.TrimSpace(q.Get(f.Name)); v != "" {
			out[f.Name] = v
		}
	}
	return out
}

// resolveFields is the control set one form render or submission works
// against: the descriptor's static fields, plus whatever FieldsFor resolves
// from the record and the driving values already submitted.
//
// This is the first of two narrowings and the reason they are not the same
// call. The declared field set depends on a submitted value (a credential's
// type declares the inputs beneath it), and narrowing a submission needs
// the declared set, so the static set goes first: it is fixed, it is what a
// driving control belongs to, and it is enough to read the value the
// resolution turns on. The caller then narrows a second time against the
// merged set this returns, through formValues, which is the pass that
// enforces the contract and rejects anything undeclared.
//
// Two deliberate asymmetries with that second pass:
//
// It reads r.Form rather than r.PostForm, so a query string may take part.
// That is what makes the no-JavaScript path work: choosing a type submits a
// GET carrying ?credential_type=N, and the form comes back with that type's
// controls on it. Letting the query choose which CONTROLS appear is safe in
// a way that letting it supply VALUES would not be, and formValues still
// reads only r.PostForm, so nothing reached from here can be written by a
// crafted link.
//
// It ignores the undeclared-field check. On this pass the dynamic fields
// are by definition not declared yet, so failing on them would reject every
// submission it exists to serve. Nothing read here reaches a domain object;
// it only decides which controls exist, and the second pass rejects an
// undeclared field against the set that actually matters.
func (h *Handler) resolveFields(w http.ResponseWriter, r *http.Request, d view.Descriptor, id string) ([]view.Field, bool) {
	static := d.FormFieldsFor(id != "")
	if d.FieldsFor == nil {
		return static, true
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form submission", http.StatusBadRequest)
		return nil, false
	}
	driving, _ := view.NewValues(static, r.Form, id != "")
	fields, err := d.ResolveFormFields(r.Context(), view.Resolve{ID: id, Values: driving})
	if err != nil {
		h.serverError(w, r, "resolve fields for "+d.Name, err)
		return nil, false
	}
	return fields, true
}

func (h *Handler) formValues(w http.ResponseWriter, r *http.Request, fields []view.Field, editing bool) (view.Values, map[string]string, bool) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form submission", http.StatusBadRequest)
		return view.Values{}, nil, false
	}

	values, undeclared := view.NewValues(fields, r.PostForm, editing)
	if len(undeclared) > 0 {
		http.Error(w, "submission contains fields this resource does not declare", http.StatusBadRequest)
		return view.Values{}, nil, false
	}

	// Keep what was typed, for redisplay on a validation failure.
	submitted := make(map[string]string, len(fields))
	for _, f := range fields {
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

// refreshURL is where a live list re-requests itself.
//
// Built from the values this handler parsed rather than from the raw request
// URI, for two reasons. A reflected query string is caller-controlled text
// reaching an attribute, and templ escaping it is a mitigation rather than a
// reason to put it there. And the parsed set is the honest one: a parameter
// this handler ignored should not silently come back on every tick.
//
// The cursor is deliberately omitted. A reader who paged forward is looking
// at a fixed window, and refreshing it in place would be the one behaviour
// nobody wants: rows shifting under a cursor that was chosen to hold them
// still.
func (h *Handler) refreshURL(r *http.Request, d view.Descriptor, limit int) string {
	base := path.Join(h.cfg.Prefix, d.Name)
	if r.URL.Query().Get("after") != "" {
		return ""
	}

	q := url.Values{}
	if search := r.URL.Query().Get("q"); search != "" {
		q.Set("q", search)
	}
	if limit != defaultPageSize {
		q.Set("limit", strconv.Itoa(limit))
	}
	if len(q) == 0 {
		return base
	}
	return base + "?" + q.Encode()
}
