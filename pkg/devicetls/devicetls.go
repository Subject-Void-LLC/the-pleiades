// Package devicetls builds the TLS client configuration for one device
// from its inventory record and its stored credential: the version floor,
// the name its certificate is checked against, a pinned certificate
// authority, and a client certificate for mutual TLS.
//
// The floor is TLS 1.2 and nothing lowers it by default. An old device
// that cannot do better is reached only through a per-device opt-in that
// says what it is, in two separate tiers so that one weakening never
// brings the next with it: tls_allow_deprecated_versions for TLS 1.0 and
// 1.1 (RFC 8996 deprecated both), and tls_allow_legacy_ciphers for the
// cipher suites Go no longer offers by default (3DES, RC4 and RSA key
// exchange). Each is reported by Warnings wherever the device is used.
// SSL 3.0 is not reachable at all: Go does not implement it.
//
// generic_http and generic_grpc read their settings here, as do the
// onboarding probes and http.request's device mode, so the device, its
// probe and its method can never disagree about how it is reached.
package devicetls

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/pfx"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// The properties a device's TLS is set from.
const (
	MinVersionProperty         = "tls_min_version"
	AllowDeprecatedProperty    = "tls_allow_deprecated_versions"
	AllowLegacyCiphersProperty = "tls_allow_legacy_ciphers"
	ServerNameProperty         = "tls_server_name"
	CAPEMProperty              = "tls_ca_pem"
	ClientCertificateProperty  = "tls_client_certificate"
)

// Properties returns every property this package reads, for a device
// type's dispatch allowlist.
func Properties() []string {
	return []string{MinVersionProperty, AllowDeprecatedProperty, AllowLegacyCiphersProperty, ServerNameProperty, CAPEMProperty, ClientCertificateProperty}
}

// versions maps each accepted tls_min_version to its protocol version.
var versions = map[string]uint16{
	"1.0": tls.VersionTLS10,
	"1.1": tls.VersionTLS11,
	"1.2": tls.VersionTLS12,
	"1.3": tls.VersionTLS13,
}

// Settings is one device's TLS, validated.
type Settings struct {
	// MinVersion is the lowest protocol version the device may negotiate.
	MinVersion uint16
	// AllowLegacyCiphers adds the cipher suites Go offers only on request.
	AllowLegacyCiphers bool
	// ServerName is the name the device's certificate is checked against,
	// when it is not the host being dialed.
	ServerName string
	// ClientCertificate presents the device's stored certificate.
	ClientCertificate bool

	roots *x509.CertPool
	// caPEM is the pinned authority as written, for a client that takes
	// PEM rather than a pool (pkg/winrmexec's does).
	caPEM []byte
}

// Configured is implemented by a device whose TLS is set from its record.
type Configured interface {
	TLSSettings() Settings
}

// For returns device's TLS settings, or the defaults (a TLS 1.2 floor and
// the system's roots) for a device that sets none.
func For(device any) Settings {
	if c, ok := device.(Configured); ok {
		return c.TLSSettings()
	}
	return Settings{MinVersion: tls.VersionTLS12}
}

// Parse reads and validates the TLS settings in props. Every weakening
// must be asked for by its own flag, and a flag that allows nothing is a
// contradiction and refused, since it says the record's author believes
// something is enabled that is not.
func Parse(props inventory.Properties) (Settings, error) {
	raw := props.Raw()
	s := Settings{MinVersion: tls.VersionTLS12}

	allowDeprecated, err := boolProperty(raw, AllowDeprecatedProperty)
	if err != nil {
		return Settings{}, err
	}
	if s.AllowLegacyCiphers, err = boolProperty(raw, AllowLegacyCiphersProperty); err != nil {
		return Settings{}, err
	}
	if s.ClientCertificate, err = boolProperty(raw, ClientCertificateProperty); err != nil {
		return Settings{}, err
	}

	if v, present := raw[MinVersionProperty]; present {
		name, ok := versionName(v)
		version, known := versions[name]
		if !ok || !known {
			return Settings{}, fmt.Errorf("property %s must be 1.0, 1.1, 1.2 or 1.3", MinVersionProperty)
		}
		s.MinVersion = version
	}
	deprecated := s.MinVersion < tls.VersionTLS12
	switch {
	case deprecated && !allowDeprecated:
		return Settings{}, fmt.Errorf("property %s is %s, which RFC 8996 deprecated: set %s to true to allow it for this device, knowing it is insecure",
			MinVersionProperty, tls.VersionName(s.MinVersion), AllowDeprecatedProperty)
	case allowDeprecated && !deprecated:
		return Settings{}, fmt.Errorf("property %s is true and %s does not go below TLS 1.2, so it allows nothing: remove it, or set %s to the version this device needs",
			AllowDeprecatedProperty, MinVersionProperty, MinVersionProperty)
	}

	if name, present := raw[ServerNameProperty]; present {
		text, _ := name.(string)
		if text == "" || strings.ContainsFunc(text, func(r rune) bool { return r <= ' ' || r == 0x7f || r == '/' }) {
			return Settings{}, fmt.Errorf("property %s must be a host name", ServerNameProperty)
		}
		s.ServerName = text
	}
	if pemText, present := raw[CAPEMProperty]; present {
		text, _ := pemText.(string)
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(text)) {
			return Settings{}, fmt.Errorf("property %s holds no PEM certificate", CAPEMProperty)
		}
		s.roots = pool
		s.caPEM = []byte(text)
	}
	return s, nil
}

// floor is the settings' lowest version, TLS 1.2 for settings that name
// none: a zero Settings, built without Parse, never reads as TLS 1.0.
func (s Settings) floor() uint16 {
	if s.MinVersion == 0 {
		return tls.VersionTLS12
	}
	return s.MinVersion
}

// Deprecated reports whether the settings allow a deprecated version.
func (s Settings) Deprecated() bool { return s.floor() < tls.VersionTLS12 }

// PinnedCA reports whether the settings trust a pinned authority rather
// than the system's roots.
func (s Settings) PinnedCA() bool { return s.roots != nil }

// CAPEM returns the pinned authority as PEM, or nil when the settings use
// the system's roots.
func (s Settings) CAPEM() []byte { return s.caPEM }

// Warnings says, in words a person acts on, what each weakening in s
// allows for the named device. It is empty for the default settings.
func (s Settings) Warnings(device string) []string {
	var out []string
	if s.Deprecated() {
		out = append(out, fmt.Sprintf("device %q allows %s (%s): deprecated by RFC 8996 and open to known attacks; keep it only while the device cannot be upgraded",
			device, tls.VersionName(s.floor()), AllowDeprecatedProperty))
	}
	if s.AllowLegacyCiphers {
		out = append(out, fmt.Sprintf("device %q allows legacy TLS cipher suites (%s): 3DES, RC4 and RSA key exchange are broken or give no forward secrecy; keep it only while the device cannot be upgraded",
			device, AllowLegacyCiphersProperty))
	}
	return out
}

// Config builds the client configuration for a connection to the device,
// taking the client certificate from secrets (the device's flattened
// credential) when the settings present one. Certificates are always
// verified: there is no setting here that turns verification off.
func (s Settings) Config(secrets map[string]string) (*tls.Config, error) {
	cfg := &tls.Config{
		MinVersion: s.floor(),
		ServerName: s.ServerName,
		RootCAs:    s.roots,
	}
	if s.AllowLegacyCiphers {
		for _, suite := range tls.CipherSuites() {
			cfg.CipherSuites = append(cfg.CipherSuites, suite.ID)
		}
		for _, suite := range tls.InsecureCipherSuites() {
			cfg.CipherSuites = append(cfg.CipherSuites, suite.ID)
		}
	}
	if s.ClientCertificate {
		cert, err := ClientCertificate(secrets)
		if err != nil {
			return nil, err
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

// ClientCertificate builds the certificate a device's credential holds:
// a PKCS#12 bundle (opened with the credential's passphrase), or a
// certificate and an unencrypted private key. A credential with neither,
// or with both, is refused rather than guessed at.
func ClientCertificate(secrets map[string]string) (tls.Certificate, error) {
	bundle := secrets[wire.SecretPFXBase64]
	certPEM, keyPEM := []byte(secrets[wire.SecretCertificatePEM]), []byte(secrets[wire.SecretPrivateKeyPEM])
	switch {
	case bundle != "" && (len(certPEM) > 0 || len(keyPEM) > 0):
		return tls.Certificate{}, errors.New("the credential holds a PKCS#12 bundle and a separate certificate or key, two ways to supply one identity")
	case bundle != "":
		var err error
		if certPEM, keyPEM, err = pfx.Decode(bundle, secrets[wire.SecretPassphrase]); err != nil {
			return tls.Certificate{}, err
		}
	case len(certPEM) == 0 || len(keyPEM) == 0:
		return tls.Certificate{}, fmt.Errorf("%s is true and the credential holds no client certificate and key: store them with add-credential --certificate and --key, or --pfx",
			ClientCertificateProperty)
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("the credential's client certificate and key do not form a usable pair (an encrypted PEM key is not supported; use a PKCS#12 bundle): %w", err)
	}
	return cert, nil
}

// boolProperty reads key as a boolean, false when absent. Anything else,
// "yes" included, is refused: a weakening is asked for with true.
func boolProperty(raw map[string]inventory.PropertyValue, key string) (bool, error) {
	v, present := raw[key]
	if !present {
		return false, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("property %s must be true or false", key)
	}
	return b, nil
}

// versionName reads a tls_min_version, which YAML and add-host --set may
// hand over as text ("1.2") or as a number (1.2).
func versionName(v inventory.PropertyValue) (string, bool) {
	switch n := v.(type) {
	case string:
		return strings.TrimSpace(n), true
	case float64:
		return strconv.FormatFloat(n, 'f', 1, 64), true
	case int:
		return strconv.Itoa(n) + ".0", true
	}
	return "", false
}
