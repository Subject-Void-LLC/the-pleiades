// generic_http: a device that answers an HTTP API at one base URL.
package generic

import (
	"fmt"
	"net/url"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// The properties generic_http reads.
const (
	BaseURLProperty     = "base_url"
	HTTPAuthProperty    = "http_auth"
	OpenAPIPathProperty = "openapi_path"
)

// HTTP is generic_http: a device that answers an HTTP API at one base URL.
// HTTPAPICapable is discovered: onboarding makes a verified, authenticated
// request to the base URL and only an answer grants it.
type HTTP struct {
	*record.Base
	baseURL        *url.URL
	allowPlaintext bool
	tls            devicetls.Settings
}

// NewHTTP builds a generic_http device from rec, refusing a base URL or
// credential mode that could leak a credential or reach something other
// than an HTTP API (httpapi.ValidateBaseURL), and an openapi_path that is
// not a path on that API.
func NewHTTP(rec record.Record) (inventory.InventoryItem, error) {
	props := inventory.NewProperties(rec.Properties)
	raw, _ := props.String(BaseURLProperty)
	auth, err := httpAuthOf(props)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", TypeHTTP, rec.Name, err)
	}
	allowPlaintext, err := strictBool(rec.Properties, httpapi.AllowPlaintextCredentialsProperty)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", TypeHTTP, rec.Name, err)
	}
	base, err := httpapi.ValidateBaseURL(raw, auth, allowPlaintext)
	if err != nil {
		return nil, fmt.Errorf("%s %s: property %s: %w", TypeHTTP, rec.Name, BaseURLProperty, err)
	}
	settings, err := deviceTLS(rec, base.Scheme == "https")
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", TypeHTTP, rec.Name, err)
	}
	if err := validateOpenAPIPath(props); err != nil {
		return nil, fmt.Errorf("%s %s: %w", TypeHTTP, rec.Name, err)
	}
	caps, err := declared(TypeHTTP, rec, []capability.Name{capability.NameNetworkAddressable})
	if err != nil {
		return nil, err
	}
	return &HTTP{Base: record.NewBase(rec, caps), baseURL: base, allowPlaintext: allowPlaintext, tls: settings}, nil
}

// httpAuthOf reads the http_auth property, defaulting to none: a stored
// credential is sent only when the record says how.
func httpAuthOf(props inventory.Properties) (string, error) {
	raw, present := props.Raw()[HTTPAuthProperty]
	if !present {
		return httpapi.AuthNone, nil
	}
	if s, _ := raw.(string); httpapi.ValidAuth(s) {
		return s, nil
	}
	return "", fmt.Errorf("property %s must be %s, %s or %s", HTTPAuthProperty, httpapi.AuthNone, httpapi.AuthBasic, httpapi.AuthBearer)
}

// validateOpenAPIPath refuses an openapi_path httpapi.Join would refuse.
func validateOpenAPIPath(props inventory.Properties) error {
	raw, present := props.Raw()[OpenAPIPathProperty]
	if !present {
		return nil
	}
	s, _ := raw.(string)
	if _, err := httpapi.Join(&url.URL{Scheme: "https", Host: "base.invalid"}, s); err != nil {
		return fmt.Errorf("property %s: %w", OpenAPIPathProperty, err)
	}
	return nil
}

// HasCapability checks the declared set AND the structural assertion.
func (h *HTTP) HasCapability(name capability.Name) bool {
	return h.Declares(name) && capability.Implements(h, name)
}

// HTTPBaseURL returns the validated base URL.
func (h *HTTP) HTTPBaseURL() string {
	return h.baseURL.String()
}

// HTTPAuth returns the credential mode, which NewHTTP validated.
func (h *HTTP) HTTPAuth() string {
	auth, _ := httpAuthOf(h.Properties())
	return auth
}

// HTTPAllowPlaintextCredentials reports whether this device's record
// accepts sending its credential over http://.
func (h *HTTP) HTTPAllowPlaintextCredentials() bool {
	return h.allowPlaintext
}

// TLSSettings returns the device's validated TLS settings.
func (h *HTTP) TLSSettings() devicetls.Settings {
	return h.tls
}

// OpenAPIPath returns the path of the API's OpenAPI document, or the empty
// string when the record names none. Onboarding reads it when present.
func (h *HTTP) OpenAPIPath() string {
	p, _ := h.Properties().String(OpenAPIPathProperty)
	return p
}

// IPAddress returns the base URL's host.
func (h *HTTP) IPAddress() string {
	return h.baseURL.Hostname()
}
