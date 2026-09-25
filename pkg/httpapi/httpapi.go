// Package httpapi holds the rules for calling a device's HTTP API with the
// device's own credential: what a base URL may be, how a relative URL is
// joined to it, which redirects may be followed, and how the credential is
// attached. Onboarding's probe and http.request's device mode both use it,
// so the two cannot disagree about where a credential may be sent.
package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// The ways a device's stored credential is sent.
const (
	AuthNone   = "none"
	AuthBasic  = "basic"
	AuthBearer = "bearer"
)

// ValidAuth reports whether mode is one of the credential modes.
func ValidAuth(mode string) bool {
	return mode == AuthNone || mode == AuthBasic || mode == AuthBearer
}

// AllowPlaintextCredentialsProperty is the per-device flag that lets a
// stored credential cross the network unencrypted, to an http:// API.
const AllowPlaintextCredentialsProperty = "http_allow_plaintext_credentials"

// ValidateBaseURL parses raw as a device's API base URL. It must be http
// or https with a host. User information is refused, since a credential
// belongs in the credential store and not in inventory; a query or
// fragment is refused, since a relative URL is joined to the base and
// either would be dropped or duplicated.
//
// A credential mode other than none needs https unless allowPlaintext
// says, for this one device, that sending the credential in the clear is
// accepted (PlaintextWarning then says so at every use). allowPlaintext
// on a URL that sends no credential in the clear allows nothing, and is
// refused as the contradiction it is.
func ValidateBaseURL(raw, auth string, allowPlaintext bool) (*url.URL, error) {
	if raw == "" {
		return nil, errors.New("the base URL is empty")
	}
	if hasControl(raw) {
		return nil, errors.New("the base URL contains a control character")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("the base URL is not a URL")
	}
	switch {
	case u.Scheme != "http" && u.Scheme != "https":
		return nil, errors.New("the base URL must be http or https")
	case u.Hostname() == "":
		return nil, errors.New("the base URL has no host")
	case u.User != nil:
		return nil, errors.New("the base URL carries user information: store the credential with add-credential instead")
	case u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "":
		return nil, errors.New("the base URL has a query or fragment")
	case !ValidAuth(auth):
		return nil, fmt.Errorf("the credential mode must be %s, %s or %s", AuthNone, AuthBasic, AuthBearer)
	case auth != AuthNone && u.Scheme != "https" && !allowPlaintext:
		return nil, fmt.Errorf("%s authentication over http:// sends the credential unencrypted: use https, or set %s to true for this device, knowing anyone on the network path can read it",
			auth, AllowPlaintextCredentialsProperty)
	case allowPlaintext && (auth == AuthNone || u.Scheme == "https"):
		return nil, fmt.Errorf("%s is true and this device sends no credential over http://, so it allows nothing: remove it", AllowPlaintextCredentialsProperty)
	}
	return u, nil
}

// SendsPlaintextCredential reports whether a request to base with auth
// carries a credential unencrypted.
func SendsPlaintextCredential(base *url.URL, auth string) bool {
	return auth != AuthNone && base.Scheme == "http"
}

// PlaintextWarning says what sending the named device's credential over
// http:// means, and what to do about it.
func PlaintextWarning(device string) string {
	return fmt.Sprintf("device %q sends its credential over unencrypted http:// (%s): anyone on the network path can read it; rotate the credential, and move the device to https:// when it can",
		device, AllowPlaintextCredentialsProperty)
}

// Join resolves ref, a path relative to the device's API, against base.
// ref must begin with a single slash and may carry a query, and nothing it
// contains can move the request off base's origin: a scheme, a host, a
// second leading slash (a protocol-relative URL) and a ".." segment
// climbing above base's own path are each refused.
func Join(base *url.URL, ref string) (*url.URL, error) {
	if !strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "//") || strings.HasPrefix(ref, `/\`) {
		return nil, errors.New("a device URL must be a path beginning with one /")
	}
	if hasControl(ref) {
		return nil, errors.New("a device URL contains a control character")
	}
	rel, err := url.Parse(ref)
	if err != nil {
		return nil, errors.New("a device URL is not a URL path")
	}
	if rel.Scheme != "" || rel.Host != "" || rel.User != nil || rel.Fragment != "" {
		return nil, errors.New("a device URL must be a path, not a URL")
	}
	for _, seg := range strings.Split(rel.Path, "/") {
		if seg == ".." || seg == "." {
			return nil, errors.New("a device URL may not contain . or .. segments")
		}
	}
	out := *base
	out.Path = strings.TrimSuffix(base.Path, "/") + rel.Path
	out.RawPath = ""
	out.RawQuery = rel.RawQuery
	return &out, nil
}

// SameOrigin reports whether a and b share scheme, host and port. A
// device-mode request follows a redirect only within its origin, since
// the credential it carries belongs to that origin alone.
func SameOrigin(a, b *url.URL) bool {
	return a.Scheme == b.Scheme && strings.EqualFold(a.Hostname(), b.Hostname()) && port(a) == port(b)
}

// Authorize attaches the device credential in secrets to req as mode
// says. A mode that needs a credential the store does not hold is an
// error, never a request sent without one.
func Authorize(req *http.Request, mode string, secrets map[string]string) error {
	switch mode {
	case AuthNone:
		return nil
	case AuthBasic:
		user, pass := secrets[wire.SecretUsername], secrets[wire.SecretPassword]
		if user == "" || pass == "" {
			return errors.New("basic authentication needs a stored username and password for this device")
		}
		req.SetBasicAuth(user, pass)
		return nil
	case AuthBearer:
		token := secrets[wire.SecretPassword]
		if token == "" {
			return errors.New("bearer authentication needs the token stored as this device's password")
		}
		req.Header.Set("Authorization", "Bearer "+token)
		return nil
	}
	return fmt.Errorf("unknown credential mode %q", mode)
}

// port returns u's port, defaulting by scheme.
func port(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

// hasControl reports whether s contains an ASCII control character.
func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}
