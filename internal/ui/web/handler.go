// Package web serves the rendered UI: one fixed route table over every
// registered resource, with the resource itself as a path parameter.
//
// That is the structural decision the whole design rests on. Adding a view
// adds no route, no handler and no wiring in the composition root, so the
// cost of a new resource is the two files its descriptor lives in. It also
// bounds metric and span label cardinality, since the pattern a request
// reports is always /ui/{resource} rather than one label per view.
//
// The trade it pays is that api.RequireScope cannot be mounted per route
// with a static scope. RequireResourceScope replaces it, resolving the
// descriptor first and reading the scope off the endpoint that descriptor
// names -- the same auth.Admission value the JSON API uses, so a UI
// request and an API request for the same operation are evaluated by one
// chain. The "a route must declare a scope" invariant is not dropped, it
// moves: view.Register refuses a descriptor whose operations name no
// endpoint.
package web

import (
	"context"
	"log/slog"
	"net/http"
	"path"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/auth"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/render"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/session"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/static"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
	"github.com/go-chi/chi/v5"
)

// Config is everything the UI needs from its composition root.
type Config struct {
	// Prefix is where the UI is mounted, e.g. "/ui".
	Prefix string

	// Version is the build identifier shown in the sidebar.
	Version string

	// Banner is the environment or classification marking. It comes from
	// deployment configuration and there is deliberately no way for a
	// signed-in user to change or dismiss it: a classification marking a
	// user can edit is a security failure, and an environment banner a
	// user can turn off is not a control.
	Banner view.Banner

	// Sessions issues and revokes the browser's session.
	Sessions session.Store

	// Cookie reads and writes the session cookie.
	Cookie session.CookieCodec

	// Tokens validates the token a caller exchanges at login. It is the
	// same evaluator the Bearer path uses, so login proves an identity
	// exactly the way an API call does and there is no second notion of
	// who a caller is.
	Tokens api.TokenValidator

	// HATEOAS decides which affordances an identity may exercise. It is
	// the same generator, built from the same admission chain, that
	// computes the JSON API's _links array -- which is what makes a
	// rendered button and an API response unable to disagree.
	HATEOAS auth.HATEOASGenerator

	// Admission enforces a resource's scope on each request.
	Admission api.Admitter

	Logger *slog.Logger
}

// Handler serves the UI subtree.
type Handler struct {
	cfg Config
}

// New builds the UI handler.
func New(cfg Config) *Handler {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Prefix == "" {
		cfg.Prefix = "/ui"
	}
	return &Handler{cfg: cfg}
}

// Routes returns the UI's router.
//
// The table is fixed. Every entry below exists once, for every resource
// that will ever be registered.
func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()

	r.Use(h.securityHeaders)

	// Unauthenticated: the login page and the assets needed to render it.
	r.Handle("/static/*", http.StripPrefix(path.Join(h.cfg.Prefix, "static"), static.Handler("")))
	r.Get("/login", h.showLogin)
	r.Post("/login", h.doLogin)

	// Everything else requires a session.
	r.Group(func(r chi.Router) {
		r.Use(h.requireSession)
		r.Use(h.csrf)

		r.Post("/logout", h.doLogout)
		r.Post("/theme", h.setTheme)
		r.Post("/skin", h.setSkin)
		r.Post("/a11y", h.setA11y)

		r.Get("/", h.index)
		r.Get("/{resource}", h.list)
		r.Get("/{resource}/new", h.newForm)
		// Static segments before the {id} parameter, which chi resolves in
		// that order, so a record can never be named "new" or "chart.json"
		// and shadow one of these.
		r.Get("/{resource}/chart.json", h.chartData)
		r.Post("/{resource}", h.create)
		r.Get("/{resource}/{id}", h.detail)
		r.Get("/{resource}/{id}/edit", h.editForm)
		r.Get("/{resource}/{id}/logs", h.stream)
		r.Post("/{resource}/{id}", h.update)
		r.Delete("/{resource}/{id}", h.destroy)
		// Record actions last, so every static segment above wins the
		// route match. view.Register refuses an action named after one of
		// them, so this ordering is enforced rather than merely relied on.
		r.Get("/{resource}/{id}/{action}", h.actionForm)
		r.Post("/{resource}/{id}/{action}", h.runAction)
	})

	return r
}

// securityHeaders sets the policy every UI response carries.
//
// The content security policy is the load-bearing one, and it carries no
// 'unsafe-inline' in either script-src or style-src. That is affordable
// only because the theme is resolved server-side: the usual no-flash
// bootstrap is an inline script, and needing one would have meant either a
// per-response nonce or a weakened policy.
func (h *Handler) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy",
			"default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; "+
				"connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// requireSession resolves the session cookie or sends the caller to login.
//
// A browser gets a redirect where the JSON API gets a 401, which is the
// whole reason api.IdentityMiddleware takes its failure renderer as a
// parameter: the same condition owes two different answers depending on
// who is asking.
func (h *Handler) requireSession(next http.Handler) http.Handler {
	// Delegated to api.IdentityMiddleware rather than resolving the
	// cookie here, so the identity lands in the one context key
	// api.IdentityFromContext reads. That matters beyond tidiness: a view
	// resource adapting an in-process port -- the Jobs view stamping the
	// actor on a dispatch, for one -- has to be able to ask who is
	// signed in, and it must get the same answer the JSON API would give
	// for the same request rather than a parallel one this package
	// invented.
	identity := api.IdentityMiddleware(
		h.redirectToLogin,
		session.CookieSource{Store: h.cfg.Sessions, Cookie: h.cfg.Cookie},
	)

	// The raw session token stays on this package's own key. It is not an
	// identity -- it is the secret the CSRF token is derived from and the
	// handle logout deletes the row by -- and it has no business in a
	// context key the whole API can read.
	withToken := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), tokenCtxKey, h.cfg.Cookie.Read(r))
		next.ServeHTTP(w, r.WithContext(ctx))
	})

	return identity(withToken)
}

// redirectToLogin answers an unauthenticated request.
//
// HTMX needs a header rather than a 302: a redirect issued to a fragment
// request is followed by the browser and swapped into whatever element
// triggered it, so an unauthenticated click would paste a login page into
// a table cell. HX-Redirect tells HTMX to navigate the whole document
// instead.
func (h *Handler) redirectToLogin(w http.ResponseWriter, r *http.Request) {
	target := path.Join(h.cfg.Prefix, "login")
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", target)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

type ctxKey int

const tokenCtxKey ctxKey = iota

// identityFrom reads the signed-in identity, which is written by
// api.IdentityMiddleware and is therefore the same value every JSON
// handler sees for the same request.
func identityFrom(ctx context.Context) *auth.Identity {
	id, _ := api.IdentityFromContext(ctx)
	return id
}

func tokenFrom(ctx context.Context) string {
	tok, _ := ctx.Value(tokenCtxKey).(string)
	return tok
}

// resourceOf resolves the {resource} path parameter to a descriptor.
//
// The parameter reaches a registry lookup and nothing else -- never a
// filesystem path, never a query fragment -- so an unregistered name is a
// map miss and a 404 rather than a traversal question.
func (h *Handler) resourceOf(r *http.Request) (view.Descriptor, bool) {
	return view.Lookup(chi.URLParam(r, "resource"))
}

// page assembles the chrome for a request.
func (h *Handler) page(r *http.Request, title, current string) view.PageModel {
	identity := identityFrom(r.Context())

	nav := view.BuildNavSections(h.cfg.Prefix, current, view.Nav(), func(d view.Descriptor) bool {
		// A view whose listing the caller cannot reach is not shown. This
		// is the entire replacement for the previous UI's hardcoded
		// six-item nav: membership is decided by the same chain that
		// would enforce it on the request.
		if d.Ops.List == nil {
			return true
		}
		return h.permits(r.Context(), identity, d.Ops.List.Scope)
	})

	return view.PageModel{
		Banner:    h.cfg.Banner,
		Title:     title,
		Theme:     h.themeOf(r),
		Skin:      h.skinOf(r),
		A11y:      h.a11yOf(r),
		Version:   h.cfg.Version,
		Prefix:    h.cfg.Prefix,
		CSRFToken: h.csrfTokenFor(r),
		Subject:   subjectOf(identity),
		Nav:       nav,
	}
}

// subjectOf names the signed-in identity for the chrome, and is what makes
// the sign-out control render at all.
func subjectOf(id *auth.Identity) string {
	if id == nil {
		return ""
	}
	return id.Subject
}

// permits asks the shared admission chain a single yes/no question.
func (h *Handler) permits(ctx context.Context, id *auth.Identity, scope auth.Scope) bool {
	if h.cfg.Admission == nil || id == nil {
		return false
	}
	return h.cfg.Admission.Evaluate(ctx, id, auth.AdmissionRequest{RequiredScope: scope}) == nil
}

// affordances computes which relations this identity may exercise on a
// descriptor, from the same generator the JSON API's _links uses.
func (h *Handler) affordances(ctx context.Context, id *auth.Identity, d view.Descriptor) view.Affordances {
	candidates := d.Candidates()
	if len(candidates) == 0 || h.cfg.HATEOAS == nil {
		return view.NewAffordances(nil)
	}

	permitted, err := h.cfg.HATEOAS.Permitted(ctx, id, candidates)
	if err != nil {
		// Never swallowed. HTML cannot express the JSON API's absent
		// _links state, so the honest rendering is no action buttons plus
		// a visible alert -- silently showing a read-only page would
		// repeat FAILURE_PATTERNS #73 in a new medium.
		h.cfg.Logger.ErrorContext(ctx, "failed to evaluate UI affordances",
			slog.String("resource", d.Name), slog.String("error", err.Error()))
		return view.UnknownAffordances()
	}

	// Re-intersect against our own candidate set, exactly as links.go
	// does, so a generator cannot introduce a relation this UI does not
	// serve.
	offered := make(map[auth.LinkRel]bool, len(candidates))
	for _, c := range candidates {
		offered[c.Rel] = true
	}
	kept := make([]auth.LinkRel, 0, len(permitted))
	for _, rel := range permitted {
		if offered[rel] {
			kept = append(kept, rel)
		}
	}
	return view.NewAffordances(kept)
}

// notFound renders a 404 without leaking whether a resource exists.
func (h *Handler) notFound(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not found", http.StatusNotFound)
}

// serverError logs the real cause and tells the caller only the category.
func (h *Handler) serverError(w http.ResponseWriter, r *http.Request, op string, err error) {
	h.cfg.Logger.ErrorContext(r.Context(), "ui handler failed",
		slog.String("op", op), slog.String("error", err.Error()))
	http.Error(w, "internal error", http.StatusInternalServerError)
}

// index redirects to the first view the caller can actually reach, rather
// than to a hardcoded landing page they may have no access to.
func (h *Handler) index(w http.ResponseWriter, r *http.Request) {
	page := h.page(r, "", "")
	first := view.FirstHref(page.Nav)
	if first == "" {
		http.Error(w, "no views available", http.StatusForbidden)
		return
	}
	http.Redirect(w, r, first, http.StatusSeeOther)
}

// renderDeclared serves the honest panel for a skeleton view.
func (h *Handler) renderDeclared(w http.ResponseWriter, r *http.Request, d view.Descriptor) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	model := view.DeclaredModel{
		Page:       h.page(r, d.Title, d.Name),
		Descriptor: d,
		Reason:     declaredReason(d),
	}
	if err := render.Declared(model).Render(r.Context(), w); err != nil {
		h.serverError(w, r, "render declared", err)
	}
}

// declaredReason says what is actually missing, so the panel is useful
// rather than merely apologetic.
func declaredReason(d view.Descriptor) string {
	if d.Ops.List == nil && d.Ops.Get == nil {
		return "This view has no backing API yet. Its shape is registered so the " +
			"navigation and the contract are real, but nothing serves it."
	}
	return "This view is registered but its handlers are not implemented."
}

// wantsFragment reports whether HTMX asked for a fragment.
//
// It affects rendering and nothing else. A client-controlled header that
// could widen access is the classic version of this bug, so authorization
// is decided before this is ever consulted.
func wantsFragment(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("HX-Request"), "true")
}
