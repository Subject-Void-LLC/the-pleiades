// Package batfish holds the batfish device type: a Batfish service, the
// network model net.batfish.check asks about a change before it is made.
package batfish

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// TypeService is the batfish device type.
const TypeService = "batfish"

// The properties a service's record holds.
const (
	// APIBaseProperty is the service's base URL, e.g.
	// http://127.0.0.1:9996.
	APIBaseProperty = "api_base"
	// AllowPlaintextProperty lets an http:// base URL name an address that
	// is not loopback. Batfish has no authentication and its API carries
	// every configuration it is given (secrets masked), so this is for a
	// private network the operator has decided to trust, and every run
	// against it says so.
	AllowPlaintextProperty = "batfish_allow_plaintext"
)

// Service is a batfish device.
type Service struct {
	*record.Base
	base      *url.URL
	plaintext bool
	tls       devicetls.Settings
}

// NewService builds a batfish device from rec, refusing a base URL that is
// not http:// or https:// with a host; plain http:// to an address that is
// not loopback unless the record allows it; the allowance on an https://
// base or a loopback one, where it allows nothing; and TLS settings on a
// plain http:// base.
func NewService(rec record.Record) (inventory.InventoryItem, error) {
	fail := func(err error) (inventory.InventoryItem, error) {
		return nil, fmt.Errorf("%s %s: %w", TypeService, rec.Name, err)
	}
	props := inventory.NewProperties(rec.Properties)
	raw, _ := props.String(APIBaseProperty)
	base, err := url.Parse(strings.TrimRight(raw, "/"))
	if raw == "" || err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Hostname() == "" ||
		base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") {
		return fail(fmt.Errorf("property %s must be the service's http:// or https:// base URL, e.g. http://127.0.0.1:9996", APIBaseProperty))
	}
	allow, present := rec.Properties[AllowPlaintextProperty]
	plaintext, isBool := allow.(bool)
	if present && !isBool {
		return fail(fmt.Errorf("property %s must be true or false", AllowPlaintextProperty))
	}
	loopback := isLoopback(base.Hostname())
	switch {
	case base.Scheme == "http" && !loopback && !plaintext:
		return fail(fmt.Errorf("property %s: Batfish has no authentication, so plain http:// is allowed to a loopback address only; use https:// or set %s: true for a private network you trust", APIBaseProperty, AllowPlaintextProperty))
	case plaintext && (base.Scheme == "https" || loopback):
		return fail(fmt.Errorf("property %s allows plain http:// to an address that is not loopback, and %s is not one", AllowPlaintextProperty, base))
	}
	if base.Scheme == "http" {
		for _, key := range devicetls.Properties() {
			if _, present := rec.Properties[key]; present {
				return fail(fmt.Errorf("property %s applies to a TLS connection, and this service's has none", key))
			}
		}
	}
	settings, err := devicetls.Parse(props)
	if err != nil {
		return fail(err)
	}
	caps, _, err := record.Declared(TypeService, rec, []capability.Name{capability.NameNetworkAddressable, capability.NameBatfishAPI})
	if err != nil {
		return nil, err
	}
	return &Service{Base: record.NewBase(rec, caps), base: base, plaintext: plaintext, tls: settings}, nil
}

// isLoopback reports whether host is a loopback address or localhost.
func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// HasCapability checks the declared set AND the structural assertion.
func (s *Service) HasCapability(name capability.Name) bool {
	return s.Declares(name) && capability.Implements(s, name)
}

// BatfishBaseURL implements capability.BatfishAPICapable.
func (s *Service) BatfishBaseURL() string { return s.base.String() }

// BatfishPlaintextWarning implements capability.BatfishAPICapable.
func (s *Service) BatfishPlaintextWarning() string {
	if !s.plaintext {
		return ""
	}
	return fmt.Sprintf("%s is reached over plain HTTP at %s (%s): Batfish has no authentication, so anyone on that network can read and change what this run sends it", s.Name(), s.base.Host, AllowPlaintextProperty)
}

// TLSSettings returns the service's validated TLS settings.
func (s *Service) TLSSettings() devicetls.Settings { return s.tls }

// IPAddress returns the base URL's host.
func (s *Service) IPAddress() string { return s.base.Hostname() }

// SharedAcrossRuns implements inventory.Shared: each run asks in a network
// of its own on the service, so no lease serializes runs that use it.
func (s *Service) SharedAcrossRuns() bool { return true }

func init() {
	record.RegisterType(TypeService, NewService)
	// Batfish takes no credential, and a template's machine credential is
	// never sent to it.
	record.RegisterOwnCredentialType(TypeService)
	record.RegisterDispatchProperties(TypeService, append([]string{APIBaseProperty, AllowPlaintextProperty}, devicetls.Properties()...)...)
}
