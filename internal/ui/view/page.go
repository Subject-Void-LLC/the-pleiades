package view

import (
	"encoding/json"
	"path"
	"strings"
)

// Theme names the three states the appearance control can be in.
//
// Three, not two, and the third is the default. A boolean toggle can say
// "light" or "dark" but has no way left to say "whatever the operating
// system is currently set to" once the user has touched it -- so a user
// who switches their OS to dark at sunset would keep getting whichever
// value they last clicked. ThemeSystem is the absence of an override, and
// the stylesheet resolves it through prefers-color-scheme.
type Theme string

const (
	ThemeSystem Theme = ""
	ThemeLight  Theme = "light"
	ThemeDark   Theme = "dark"
)

// ParseTheme reads a submitted theme value, falling back to system for
// anything unrecognized. An unknown value is not an error worth failing a
// request over: the honest response to "I do not know what you asked for"
// is to stop overriding and follow the platform.
func ParseTheme(raw string) Theme {
	switch Theme(strings.TrimSpace(raw)) {
	case ThemeLight:
		return ThemeLight
	case ThemeDark:
		return ThemeDark
	default:
		return ThemeSystem
	}
}

// ThemeOption is one button in the appearance control.
type ThemeOption struct {
	Value string
	Label string
	// Pressed is the literal aria-pressed value. It is a string rather
	// than a bool because that attribute is tri-state in ARIA and
	// rendering a Go bool would emit "false" where the attribute should
	// be absent or "false" inconsistently across templates.
	Pressed string
}

// NavItem is one sidebar entry, already filtered by what the requesting
// identity may actually reach.
type NavItem struct {
	Label   string
	Href    string
	Current bool
}

// PageModel is the chrome every page renders inside: what it is called,
// how it is themed, what the caller may navigate to, and the CSRF token
// this request's forms must carry.
//
// It is assembled per request in internal/ui/web and passed down. Nothing
// in a template reaches for a global to find any of it, which is what
// keeps the templates free of decisions.
type PageModel struct {
	// Title is the view's name. The document title is derived from it.
	Title string

	// Theme is the resolved appearance override, or ThemeSystem.
	Theme string

	// Skin is the resolved theme family, one of the Skin constants in
	// skin.go. It is a separate axis from Theme because the two answer
	// different questions and compose -- every skin has a light mode and
	// a dark one.
	Skin string

	// A11y is the explicit in-app accessibility override. It is never a
	// degraded mode: it changes rendering and nothing else.
	A11y bool

	// Version is the build identifier shown under the brand.
	Version string

	// Prefix is the UI subtree's mount point, e.g. "/ui". Every URL a
	// template emits is built from it rather than hardcoded, so the
	// mount point stays a composition-root decision.
	Prefix string

	// CSRFToken is this session's derived token.
	//
	// The sign-in page carries one too, and it is a different and weaker
	// kind: there is no session to bind it to yet, so it is a double-submit
	// pair against a per-process key rather than a per-session one. Both
	// travel in the same field so a template does not have to know which it
	// holds. See internal/ui/web's preAuthCSRF for why the pre-auth version
	// cannot be as strong and why that is unavoidable.
	CSRFToken string

	// PasswordLogin reports whether this deployment offers local password
	// sign-in, so the login form can show the fields it can actually honor.
	//
	// A deployment federating against an external issuer holds no local
	// credentials, and rendering a password box there would be an
	// affordance that can only ever fail. Meaningful on the sign-in page
	// alone.
	PasswordLogin bool

	// Notice is a short message about what just happened, shown above the
	// page's own content.
	//
	// Deliberately a fixed string chosen by the handler rather than an
	// error passed through from below. An error from a store is written
	// for an operator reading logs and may name why a credential was
	// refused; a page is read by whoever is at the browser, who is not
	// necessarily the account's owner.
	Notice string

	// Subject is who the session says is signed in. It is shown beside the
	// sign-out control rather than on its own: an operator with access to
	// several identities needs to know which one is about to run a job,
	// and a sign-out button with no indication of what it signs out of is
	// a control you have to click to understand.
	Subject string

	// Nav is the sidebar, in order, already filtered by authorization.
	Nav []NavSection

	// ShowSettings reports whether this caller may reach the deployment's
	// own settings.
	//
	// Computed by the handler through the same admission chain the settings
	// route itself checks, rather than decided here: a template that made
	// its own judgement would be a second opinion about authorization, and
	// the one that drifts is always the one nobody is testing.
	ShowSettings bool

	// ReturnTo is the URL of the page being rendered, carried in a hidden
	// field by every appearance control so a preference change puts the
	// reader back exactly where they were.
	//
	// Path and query, on this origin. It exists because a Referer is
	// routinely stripped by privacy settings, proxies and referrer
	// policies, and because the query is where a record's tab and a list's
	// cursor now live: a control that returns you to the path alone is one
	// that quietly sends you to a different tab every time you use it.
	ReturnTo string

	// Banner is the environment or classification marking. It is on the
	// page model rather than read from config inside a template because a
	// template must not reach for global state, and because the login page
	// needs it too -- somebody signing in to production should know that
	// before they authenticate, not after.
	Banner Banner

	// Chart is set when this page renders one, which is what decides
	// whether the ECharts bundle is requested at all. A megabyte of
	// charting library on a page with no chart is a megabyte nobody
	// asked for.
	Chart *ChartSpec

	// Stream is set on the live log page, and decides whether the stream
	// script is requested. Same reasoning as Chart: every page that does
	// not read a stream should not carry the code for reading one.
	Stream *StreamSpec
}

// DocumentTitle is the <title> text, most specific part first so a long
// browser tab still shows the useful half.
func (p PageModel) DocumentTitle() string {
	if p.Title == "" {
		return "Pleiades"
	}
	return p.Title + " // The Pleiades"
}

// AssetPath builds the served URL for a content-hashed asset name.
func (p PageModel) AssetPath(hashed string) string {
	return path.Join(p.Prefix, "static", hashed)
}

// ThemeAction is where the appearance form posts.
func (p PageModel) ThemeAction() string {
	return path.Join(p.Prefix, "theme")
}

// SkinAction and A11yAction are where the appearance controls post.
func (p PageModel) SkinAction() string { return path.Join(p.Prefix, "skin") }
func (p PageModel) A11yAction() string { return path.Join(p.Prefix, "a11y") }

// LogoutAction is where the sign-out control posts.
//
// A POST rather than a link, deliberately. Sign-out changes server state --
// it deletes the session row -- and anything a browser, a link prefetcher
// or a corporate scanner can trigger by following a GET will eventually be
// triggered by one of them. Going through the form also means sign-out
// carries the CSRF token like every other write.
func (p PageModel) LogoutAction() string { return path.Join(p.Prefix, "logout") }

// SignedIn reports whether this page has an authenticated session, which is
// what decides whether the sign-out control renders at all. The login page
// shares this chrome and must not offer to sign out of nothing.
func (p PageModel) SignedIn() bool { return p.Subject != "" }

// CSRFHeaders renders the hx-headers attribute that carries the CSRF token
// on every HTMX request in the application.
//
// One attribute on <body> covers every fragment request a page can make,
// so a template author never writes CSRF handling and cannot forget to.
// It is JSON-encoded rather than concatenated because the value is an
// HMAC in base64url, and hand-building JSON around caller-adjacent data is
// how an injection gets written.
func (p PageModel) CSRFHeaders() string {
	if p.CSRFToken == "" {
		return "{}"
	}
	encoded, err := json.Marshal(map[string]string{"X-CSRF-Token": p.CSRFToken})
	if err != nil {
		// Marshalling a map of two strings cannot fail; returning an
		// empty object rather than panicking keeps a rendering bug from
		// becoming an outage.
		return "{}"
	}
	return string(encoded)
}

// ThemeOptions returns the appearance control's three buttons, with the
// active one marked pressed.
func (p PageModel) ThemeOptions() []ThemeOption {
	current := ParseTheme(p.Theme)
	options := []struct {
		value Theme
		label string
	}{
		{ThemeSystem, "System"},
		{ThemeLight, "Light"},
		{ThemeDark, "Dark"},
	}

	out := make([]ThemeOption, 0, len(options))
	for _, o := range options {
		pressed := "false"
		if o.value == current {
			pressed = "true"
		}
		out = append(out, ThemeOption{Value: string(o.value), Label: o.label, Pressed: pressed})
	}
	return out
}

// BuildNav turns the registered views into sidebar entries, keeping only
// those the caller may reach.
//
// permitted is supplied by the caller rather than computed here, because
// deciding what an identity may do is the authorization chain's job and
// this package must not become a second place that answers it. Ordering
// and permission are two questions; folding them together is how a nav
// ends up with a permission table of its own, which is the exact thing
// this UI exists to not have.
func BuildNav(prefix, currentName string, descriptors []Descriptor, permitted func(Descriptor) bool) []NavItem {
	items := make([]NavItem, 0, len(descriptors))
	for _, d := range descriptors {
		if permitted != nil && !permitted(d) {
			continue
		}
		items = append(items, NavItem{
			Label:   d.NavLabel,
			Href:    path.Join(prefix, d.Name),
			Current: d.Name == currentName,
		})
	}
	return items
}

// navHref builds one sidebar entry's URL.
func navHref(prefix, name string) string { return path.Join(prefix, name) }
