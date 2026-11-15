// generic_restconf: a device configured over RESTCONF (RFC 8040) and nothing
// else this platform knows of.
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

// Restconf is generic_restconf: a device that serves RESTCONF at one origin.
// It reads generic_http's base_url, http_auth and
// http_allow_plaintext_credentials, and a device's TLS settings, so the two
// HTTP-based types are written alike; unlike generic_http its credential is
// sent as basic authentication unless the record says otherwise, since RFC
// 8040 section 2.5 requires a RESTCONF server to authenticate every client.
//
// RestconfCapable itself is discovered: onboarding reads the origin's
// host-meta (with no credential; on a 404, as IOS XE answers it to anything
// but a valid login, the conventional /restconf is tried), then the server's
// capabilities and its YANG library, and grants it only when one of those
// reads under the root answers. Over
// https the server's certificate is always verified; over http:// (allowed
// only with http_auth none, or with http_allow_plaintext_credentials) it is
// granted with a warning that nothing proved which server answered.
type Restconf struct {
	*record.Base
	baseURL        *url.URL
	auth           string
	allowPlaintext bool
	tls            devicetls.Settings
	// stale says why this device's discovery grants nothing, or is empty.
	stale string
}

// NewRestconf builds a generic_restconf device from rec, refusing a base URL
// or credential mode that could leak a credential (httpapi.ValidateBaseURL),
// a base URL with a path (the root is the device's host-meta's to name), and
// a TLS setting on an http:// origin.
func NewRestconf(rec record.Record) (inventory.InventoryItem, error) {
	props := inventory.NewProperties(rec.Properties)
	raw, _ := props.String(BaseURLProperty)
	auth, err := restconfAuthOf(props)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", TypeRestconf, rec.Name, err)
	}
	allowPlaintext, err := strictBool(rec.Properties, httpapi.AllowPlaintextCredentialsProperty)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", TypeRestconf, rec.Name, err)
	}
	base, err := httpapi.ValidateBaseURL(raw, auth, allowPlaintext)
	if err != nil {
		return nil, fmt.Errorf("%s %s: property %s: %w", TypeRestconf, rec.Name, BaseURLProperty, err)
	}
	if base.Path != "" && base.Path != "/" {
		return nil, fmt.Errorf("%s %s: property %s must be an origin with no path (https://host or https://host:port): the RESTCONF root is found from its /.well-known/host-meta", TypeRestconf, rec.Name, BaseURLProperty)
	}
	settings, err := deviceTLS(rec, base.Scheme == "https")
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", TypeRestconf, rec.Name, err)
	}
	caps, stale, err := record.Declared(TypeRestconf, rec, []capability.Name{capability.NameNetworkAddressable})
	if err != nil {
		return nil, err
	}
	return &Restconf{Base: record.NewBase(rec, caps), baseURL: base, auth: auth, allowPlaintext: allowPlaintext, tls: settings, stale: stale}, nil
}

// restconfAuthOf reads the http_auth property, defaulting to basic.
func restconfAuthOf(props inventory.Properties) (string, error) {
	if _, present := props.Raw()[HTTPAuthProperty]; !present {
		return httpapi.AuthBasic, nil
	}
	return httpAuthOf(props)
}

// DiscoveryBinding implements inventory.DiscoveryBinder: the digest a
// discovery must carry to hold for this record as it is now.
func (r *Restconf) DiscoveryBinding() string {
	return Binding(TypeRestconf, r.Properties())
}

// StaleDiscovery implements inventory.StaleDiscoverer.
func (r *Restconf) StaleDiscovery() string {
	return r.stale
}

// HasCapability checks the declared set AND the structural assertion.
func (r *Restconf) HasCapability(name capability.Name) bool {
	return r.Declares(name) && capability.Implements(r, name)
}

// RestconfBaseURL returns the validated origin.
func (r *Restconf) RestconfBaseURL() string {
	return r.baseURL.String()
}

// RestconfAuth returns the credential mode, which NewRestconf validated.
func (r *Restconf) RestconfAuth() string {
	return r.auth
}

// RestconfAllowPlaintextCredentials reports whether this device's record
// accepts sending its credential over http://.
func (r *Restconf) RestconfAllowPlaintextCredentials() bool {
	return r.allowPlaintext
}

// TLSSettings returns the device's validated TLS settings.
func (r *Restconf) TLSSettings() devicetls.Settings {
	return r.tls
}

// IPAddress returns the origin's host.
func (r *Restconf) IPAddress() string {
	return r.baseURL.Hostname()
}
