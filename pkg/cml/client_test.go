// Tests of the CML client against answers a real controller gives in
// shapes the sandbox cannot be made to, and of its TLS pinning against a
// local certificate.
package cml_test

import (
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// controller serves a CML 2 API with one lab of two nodes: R1, whose
// configuration names three addresses and the controller observed one, and
// a PC whose configuration is the newer list of files.
func controller(t *testing.T, login func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v0/system_information" && r.URL.Path != "/api/v0/authenticate" && r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/v0/system_information":
			_, _ = w.Write([]byte(`{"version":"2.7.2+build.26","ready":true}`))
		case "/api/v0/authenticate":
			login(w, r)
		case "/api/v0/labs":
			_, _ = w.Write([]byte(`["lab1"]`))
		case "/api/v0/labs/lab1":
			_, _ = w.Write([]byte(`{"lab_title":"default-lab","state":"STARTED"}`))
		case "/api/v0/labs/lab1/nodes":
			_, _ = w.Write([]byte(`[{"id":"n1","label":"R1","node_definition":"iol-xe","state":"BOOTED",
				"configuration":"hostname R1\ninterface E0/0\n ip address 10.10.20.171 255.255.255.0\ninterface E0/1\n ip address 1.1.1.1 255.255.255.0\n ip address 10.10.20.171 255.255.255.0 secondary\n"},
				{"id":"n2","label":"PC1","node_definition":"desktop","state":"STOPPED",
				"configuration":[{"name":"node.cfg","content":"ip addr add 10.10.20.177/24 dev eth0"},{"name":"boot.sh","content":"ifconfig eth1 192.0.2.7 netmask 255.255.255.0 up"}]},
				{"id":"n3","label":"ext-conn-0","node_definition":"external_connector","state":"BOOTED","configuration":null}]`))
		case "/api/v0/labs/lab1/layer3_addresses":
			_, _ = w.Write([]byte(`{"n1":{"name":"R1","interfaces":{"aa":{"ip4":["10.10.20.171/24","bogus"]}}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// client is a client for srv trusting the test server's own certificate.
func client(t *testing.T, srv *httptest.Server, creds cml.Credentials) *cml.Client {
	t.Helper()
	c, err := cml.New(srv.URL, srv.Client(), creds)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestClient_ReadsALabsNodes: the version, the labs with their titles, and
// each node with the addresses the controller observed and, apart, the
// ones its configuration names, in either configuration shape.
func TestClient_ReadsALabsNodes(t *testing.T) {
	srv := controller(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`"tok"`)) })
	c := client(t, srv, cml.Credentials{Username: "developer", Password: "pw"})
	ctx := context.Background()
	if _, err := c.Labs(ctx); err == nil || !strings.Contains(err.Error(), "Connect first") {
		t.Errorf("Labs before Connect = %v", err)
	}
	if v, err := c.Connect(ctx); err != nil || v != "2.7.2+build.26" {
		t.Fatalf("Connect = %q, %v", v, err)
	}
	labs, err := c.Labs(ctx)
	if err != nil || len(labs) != 1 || labs[0].ID != "lab1" || labs[0].Title != "default-lab" {
		t.Fatalf("Labs = %+v, %v", labs, err)
	}
	nodes, err := c.Nodes(ctx, "lab1")
	if err != nil || len(nodes) != 3 {
		t.Fatalf("Nodes = %+v, %v", nodes, err)
	}
	r1, pc1, ext := nodes[0], nodes[1], nodes[2]
	if len(r1.Addresses) != 1 || r1.Addresses[0] != netip.MustParseAddr("10.10.20.171") {
		t.Errorf("R1's observed addresses = %v", r1.Addresses)
	}
	if got := r1.ConfiguredAddresses; len(got) != 2 || got[0] != netip.MustParseAddr("1.1.1.1") || got[1] != netip.MustParseAddr("10.10.20.171") {
		t.Errorf("R1's configured addresses = %v, want 1.1.1.1 and 10.10.20.171 once each", got)
	}
	if len(pc1.Addresses) != 0 || len(pc1.ConfiguredAddresses) != 2 || pc1.ConfiguredAddresses[0] != netip.MustParseAddr("10.10.20.177") || pc1.ConfiguredAddresses[1] != netip.MustParseAddr("192.0.2.7") {
		t.Errorf("PC1 = %+v", pc1)
	}
	if len(ext.ConfiguredAddresses) != 0 || ext.NodeDefinition != "external_connector" {
		t.Errorf("ext-conn-0 = %+v", ext)
	}
	if _, err := c.Nodes(ctx, "nope"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("an unknown lab = %v", err)
	}
}

// TestConnect_RefusesWhatItCannotUse: no username, a refused login, an
// empty token, a controller that is not ready or not CML, and base URLs
// that are not one.
func TestConnect_RefusesWhatItCannotUse(t *testing.T) {
	ctx := context.Background()
	refused := controller(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) })
	if _, err := client(t, refused, cml.Credentials{Username: "developer", Password: "x"}).Connect(ctx); err == nil || !strings.Contains(err.Error(), "refused the login") {
		t.Errorf("a refused login = %v", err)
	}
	if _, err := client(t, refused, cml.Credentials{}).Connect(ctx); err == nil || !strings.Contains(err.Error(), "without a login") {
		t.Errorf("no username = %v", err)
	}
	// A validation error echoes the request, password included, so the
	// answer to a login is never quoted.
	echoes := controller(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v0/authenticate" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"detail":[{"input":{"password":"x-ECHOED-SECRET"}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"version":"2.7.2","ready":true}`))
	})
	if _, err := client(t, echoes, cml.Credentials{Username: "u", Password: "x-ECHOED-SECRET"}).Connect(ctx); err == nil || !strings.Contains(err.Error(), "422") || strings.Contains(err.Error(), "ECHOED-SECRET") {
		t.Errorf("a login answer echoing the password = %v", err)
	}
	empty := controller(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`""`)) })
	if _, err := client(t, empty, cml.Credentials{Username: "u"}).Connect(ctx); err == nil || !strings.Contains(err.Error(), "no token") {
		t.Errorf("an empty token = %v", err)
	}
	for name, body := range map[string]string{"not ready": `{"version":"2.7.2","ready":false}`, "not CML": `{"hello":"world"}`} {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
		if _, err := client(t, srv, cml.Credentials{Username: "u"}).Connect(ctx); err == nil {
			t.Errorf("%s: Connect succeeded", name)
		}
		srv.Close()
	}
	for _, base := range []string{"", "ftp://cml", "https://", "https://cml/api", "https://cml?x=1", "https://u:p@cml"} {
		if _, err := cml.New(base, nil, cml.Credentials{}); err == nil {
			t.Errorf("New(%q) accepted", base)
		}
	}
}

// TestConnect_TrustsWhatTheDeviceTLSSettingsPin: through the settings a
// cml_controller's record holds, a pinned certificate is the one accepted
// (in capitals too); a pinned CA verifies the chain and the name; and
// anything else fails carrying the certificate's fingerprint to check.
func TestConnect_TrustsWhatTheDeviceTLSSettingsPin(t *testing.T) {
	srv := controller(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`"tok"`)) })
	leaf := srv.Certificate()
	connect := func(props map[string]inventory.PropertyValue) error {
		settings, err := devicetls.Parse(inventory.NewProperties(props))
		if err != nil {
			return err
		}
		cfg, err := settings.Config(nil)
		if err != nil {
			return err
		}
		c, _ := cml.New(srv.URL, &http.Client{Transport: &http.Transport{TLSClientConfig: cfg}}, cml.Credentials{Username: "u"})
		_, err = c.Connect(context.Background())
		return err
	}
	pin := devicetls.Fingerprint(leaf)
	if err := connect(map[string]inventory.PropertyValue{devicetls.FingerprintProperty: pin}); err != nil {
		t.Errorf("the pinned certificate: %v", err)
	}
	if err := connect(map[string]inventory.PropertyValue{devicetls.FingerprintProperty: strings.ToUpper(pin)}); err != nil {
		t.Errorf("the pin in capitals: %v", err)
	}
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw}))
	if err := connect(map[string]inventory.PropertyValue{devicetls.CAPEMProperty: ca}); err != nil {
		t.Errorf("the pinned CA, the right name: %v", err)
	}
	var untrusted *devicetls.UntrustedError
	if err := connect(map[string]inventory.PropertyValue{devicetls.CAPEMProperty: ca, devicetls.ServerNameProperty: "10.9.9.9"}); !errors.As(err, &untrusted) {
		t.Errorf("the pinned CA, a name it does not hold = %v, want UntrustedError", err)
	}
	if err := connect(map[string]inventory.PropertyValue{devicetls.FingerprintProperty: "sha256:" + strings.Repeat("0", 64)}); !errors.As(err, &untrusted) || untrusted.Fingerprint != pin || !strings.Contains(untrusted.Error(), "--tls-fingerprint") {
		t.Errorf("another pin = %v, want an UntrustedError naming the real fingerprint", err)
	}
	if err := connect(nil); !errors.As(err, &untrusted) || untrusted.Fingerprint != pin {
		t.Errorf("the system's CAs = %v, want UntrustedError", err)
	}
}
