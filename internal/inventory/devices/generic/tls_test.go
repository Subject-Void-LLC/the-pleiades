// Tests for the TLS settings and the plaintext-credential flag the
// generic_http and generic_grpc types read from their records.
package generic_test

import (
	"crypto/tls"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// TestTLSSettings_ReachTheAccessors: the settings a record declares are the
// ones each type hands its callers, and the plaintext flag is readable.
func TestTLSSettings_ReachTheAccessors(t *testing.T) {
	h, err := buildWith(t, generic.TypeHTTP, map[string]inventory.PropertyValue{
		devicetls.MinVersionProperty: "1.0", devicetls.AllowDeprecatedProperty: true,
		devicetls.ServerNameProperty: "api.internal",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s := h.(devicetls.Configured).TLSSettings(); s.MinVersion != tls.VersionTLS10 || s.ServerName != "api.internal" {
		t.Errorf("generic_http settings %+v", s)
	}

	plain, err := buildWith(t, generic.TypeHTTP, map[string]inventory.PropertyValue{
		generic.BaseURLProperty: "http://api.example.com", generic.HTTPAuthProperty: "basic",
		httpapi.AllowPlaintextCredentialsProperty: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !plain.(interface{ HTTPAllowPlaintextCredentials() bool }).HTTPAllowPlaintextCredentials() {
		t.Error("the plaintext flag the record sets does not read back")
	}

	g, err := buildWith(t, generic.TypeGRPC, map[string]inventory.PropertyValue{devicetls.MinVersionProperty: "1.3"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s := g.(devicetls.Configured).TLSSettings(); s.MinVersion != tls.VersionTLS13 {
		t.Errorf("generic_grpc settings %+v", s)
	}
}

// TestTLSSettings_RefuseWhatCannotApply covers each refusal: a TLS setting
// on a connection that has no TLS, a malformed setting or flag, a
// weakening HTTP/2 forbids, and a gRPC target with no usable host.
func TestTLSSettings_RefuseWhatCannotApply(t *testing.T) {
	for _, tc := range []struct {
		name  string
		typ   string
		props map[string]inventory.PropertyValue
		want  string
	}{
		{"a TLS setting on http://", generic.TypeHTTP,
			map[string]inventory.PropertyValue{generic.BaseURLProperty: "http://api.example.com", devicetls.MinVersionProperty: "1.3"},
			"has none"},
		{"an unknown TLS version", generic.TypeHTTP,
			map[string]inventory.PropertyValue{devicetls.MinVersionProperty: "1.4"}, devicetls.MinVersionProperty},
		{"the plaintext flag as text", generic.TypeHTTP,
			map[string]inventory.PropertyValue{httpapi.AllowPlaintextCredentialsProperty: "yes"}, httpapi.AllowPlaintextCredentialsProperty},
		{"a TLS setting on plaintext gRPC", generic.TypeGRPC,
			map[string]inventory.PropertyValue{generic.GRPCPlaintextProperty: true, devicetls.ServerNameProperty: "x.internal"},
			"has none"},
		{"an unknown TLS version on gRPC", generic.TypeGRPC,
			map[string]inventory.PropertyValue{devicetls.MinVersionProperty: "1.4"}, devicetls.MinVersionProperty},
		{"deprecated TLS on gRPC", generic.TypeGRPC,
			map[string]inventory.PropertyValue{devicetls.MinVersionProperty: "1.0", devicetls.AllowDeprecatedProperty: true},
			"HTTP/2"},
		{"legacy ciphers on gRPC", generic.TypeGRPC,
			map[string]inventory.PropertyValue{devicetls.AllowLegacyCiphersProperty: true}, "HTTP/2"},
		{"no gRPC target", generic.TypeGRPC,
			map[string]inventory.PropertyValue{generic.GRPCTargetProperty: ""}, "is required"},
		{"a control character in the gRPC host", generic.TypeGRPC,
			map[string]inventory.PropertyValue{generic.GRPCTargetProperty: "a\x01b:443"}, "not a name or address"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildWith(t, tc.typ, tc.props, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v, want it to contain %q", err, tc.want)
			}
		})
	}
}
