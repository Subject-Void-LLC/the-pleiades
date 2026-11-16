// generic_gnmi: a device managed over gNMI (OpenConfig's gRPC Network
// Management Interface) and nothing else this platform knows of.
package generic

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// The properties generic_gnmi reads beside generic_grpc's target and
// grpc_plaintext.
const (
	// GNMIAuthProperty is how the stored credential is sent: password (the
	// default), bearer or none.
	GNMIAuthProperty = "gnmi_auth"
	// GRPCAllowPlaintextCredentialsProperty accepts sending the credential
	// over a plaintext connection, a further opt-in beside grpc_plaintext.
	GRPCAllowPlaintextCredentialsProperty = "grpc_allow_plaintext_credentials" // #nosec G101 -- a property's name, not a credential
)

// GNMI is generic_gnmi: a device that serves gNMI at one host:port.
//
// It reads generic_grpc's target and grpc_plaintext, so the two gRPC types
// are written alike, and is a type of its own rather than generic_grpc with
// one more capability, for two measured reasons. Its credential is sent as
// the username and password metadata the gNMI specification names (Stratum,
// Nokia SR Linux and IOS XE take it so), where generic_grpc's probe sends a
// stored password as a bearer token: one record cannot mean both. And
// GNMICapable is proved by gNMI's own Capabilities RPC, which says nothing
// about the health and reflection services generic_grpc's discovery asks,
// and a gNMI server need not serve either.
//
// GNMICapable itself is discovered: onboarding asks the server's
// Capabilities, with the device's own TLS (always verified) and credential,
// and grants it only on an answer. A plaintext connection is the record's
// explicit choice (grpc_plaintext) and is warned about on every onboarding
// and run; a credential over one is a further explicit choice
// (grpc_allow_plaintext_credentials), warned about too.
type GNMI struct {
	*record.Base
	host           string
	auth           string
	plaintext      bool
	allowPlaintext bool
	tls            devicetls.Settings
	// stale says why this device's discovery grants nothing, or is empty.
	stale string
}

// NewGNMI builds a generic_gnmi device from rec, refusing a target that is
// not host:port, a credential mode it does not know, a flag that is not a
// boolean, a plaintext-credential flag that allows nothing (no plaintext
// connection, or no credential sent), and a TLS setting gRPC cannot use.
func NewGNMI(rec record.Record) (inventory.InventoryItem, error) {
	props := inventory.NewProperties(rec.Properties)
	target, _ := props.String(GRPCTargetProperty)
	host, err := ValidateGRPCTarget(target)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", TypeGNMI, rec.Name, err)
	}
	auth, err := gnmiAuthOf(props)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", TypeGNMI, rec.Name, err)
	}
	plaintext, err := strictBool(rec.Properties, GRPCPlaintextProperty)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", TypeGNMI, rec.Name, err)
	}
	allowPlaintext, err := strictBool(rec.Properties, GRPCAllowPlaintextCredentialsProperty)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", TypeGNMI, rec.Name, err)
	}
	// A flag that can allow nothing is refused, not ignored: it says the
	// record's author believes a credential crosses plaintext when none does.
	if allowPlaintext && (!plaintext || auth == gnmi.AuthNone) {
		return nil, fmt.Errorf("%s %s: %s allows sending a credential over a plaintext connection, and this record makes none (it needs %s true and %s other than none)",
			TypeGNMI, rec.Name, GRPCAllowPlaintextCredentialsProperty, GRPCPlaintextProperty, GNMIAuthProperty)
	}
	settings, err := deviceTLS(rec, !plaintext)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", TypeGNMI, rec.Name, err)
	}
	// gRPC runs over HTTP/2, which requires TLS 1.2 and forbids the legacy
	// suites (RFC 7540 section 9.2), as generic_grpc's constructor says.
	if settings.Deprecated() || settings.AllowLegacyCiphers {
		return nil, fmt.Errorf("%s %s: gRPC runs over HTTP/2, which requires TLS 1.2 and forbids legacy cipher suites, so %s and %s cannot apply",
			TypeGNMI, rec.Name, devicetls.AllowDeprecatedProperty, devicetls.AllowLegacyCiphersProperty)
	}
	caps, stale, err := record.Declared(TypeGNMI, rec, []capability.Name{capability.NameNetworkAddressable})
	if err != nil {
		return nil, err
	}
	return &GNMI{Base: record.NewBase(rec, caps), host: host, auth: auth, plaintext: plaintext, allowPlaintext: allowPlaintext, tls: settings, stale: stale}, nil
}

// gnmiAuthOf reads gnmi_auth, defaulting to password: the gNMI
// specification's own credential.
func gnmiAuthOf(props inventory.Properties) (string, error) {
	raw, present := props.Raw()[GNMIAuthProperty]
	if !present {
		return gnmi.AuthPassword, nil
	}
	auth, ok := raw.(string)
	switch {
	case !ok:
		return "", fmt.Errorf("property %s must be text", GNMIAuthProperty)
	case auth == gnmi.AuthPassword, auth == gnmi.AuthBearer, auth == gnmi.AuthNone:
		return auth, nil
	}
	return "", fmt.Errorf("property %s must be %s, %s or %s", GNMIAuthProperty, gnmi.AuthPassword, gnmi.AuthBearer, gnmi.AuthNone)
}

// DiscoveryBinding implements inventory.DiscoveryBinder: the digest a
// discovery must carry to hold for this record as it is now.
func (g *GNMI) DiscoveryBinding() string {
	return Binding(TypeGNMI, g.Properties())
}

// StaleDiscovery implements inventory.StaleDiscoverer.
func (g *GNMI) StaleDiscovery() string {
	return g.stale
}

// HasCapability checks the declared set AND the structural assertion.
func (g *GNMI) HasCapability(name capability.Name) bool {
	return g.Declares(name) && capability.Implements(g, name)
}

// GNMITarget returns the validated host:port.
func (g *GNMI) GNMITarget() string {
	target, _ := g.Properties().String(GRPCTargetProperty)
	return target
}

// GNMIAuth returns the credential mode, which NewGNMI validated.
func (g *GNMI) GNMIAuth() string {
	return g.auth
}

// GNMIPlaintext reports whether grpc_plaintext is true.
func (g *GNMI) GNMIPlaintext() bool {
	return g.plaintext
}

// GNMIAllowPlaintextCredentials reports whether the record accepts sending
// its credential over the plaintext connection it asked for.
func (g *GNMI) GNMIAllowPlaintextCredentials() bool {
	return g.allowPlaintext
}

// TLSSettings returns the device's validated TLS settings.
func (g *GNMI) TLSSettings() devicetls.Settings {
	return g.tls
}

// IPAddress returns the target's host.
func (g *GNMI) IPAddress() string {
	return g.host
}
