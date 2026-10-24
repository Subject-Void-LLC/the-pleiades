// Tests of the CML client against a real CML 2 controller: Cisco's DevNet
// CML sandbox, reached over its VPN (tests/cml/vpn.sh), named by the
// PLEIADES_CML_* settings tests/cml/.env holds.
package cml_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// sandbox returns the controller's settings, skipping when none are set.
func sandbox(t *testing.T) (base, user, password, fingerprint string) {
	t.Helper()
	base, user, password, fingerprint = os.Getenv("PLEIADES_CML_URL"), os.Getenv("PLEIADES_CML_USERNAME"), os.Getenv("PLEIADES_CML_PASSWORD"), os.Getenv("PLEIADES_CML_TLS_FINGERPRINT")
	testsupport.Require(t, "a CML controller", base != "" && user != "" && fingerprint != "",
		"set PLEIADES_CML_URL, PLEIADES_CML_USERNAME, PLEIADES_CML_PASSWORD and PLEIADES_CML_TLS_FINGERPRINT (tests/cml/.env.example), and run on the sandbox's VPN: tests/cml/vpn.sh run")
	return base, user, password, fingerprint
}

// pinned is an HTTP client trusting only the certificate fingerprint pins,
// built as a cml_controller's record builds it; with no fingerprint it
// trusts the system's CAs.
func pinned(t *testing.T, fingerprint string) *http.Client {
	t.Helper()
	props := map[string]inventory.PropertyValue{}
	if fingerprint != "" {
		props[devicetls.FingerprintProperty] = fingerprint
	}
	settings, err := devicetls.Parse(inventory.NewProperties(props))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := settings.Config(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Timeout: time.Minute, Transport: &http.Transport{TLSClientConfig: cfg}}
}

// TestClient_AgainstARealController: the client logs in through the pinned
// certificate, lists the labs, and reads a lab's nodes with what the
// controller observed and what each configuration names; a wrong password
// and a certificate nothing vouches for are refused.
func TestClient_AgainstARealController(t *testing.T) {
	base, user, password, fingerprint := sandbox(t)
	ctx := context.Background()
	c, err := cml.New(base, pinned(t, fingerprint), cml.Credentials{Username: user, Password: password})
	if err != nil {
		t.Fatal(err)
	}
	version, err := c.Connect(ctx)
	if err != nil || !strings.HasPrefix(version, "2.") {
		t.Fatalf("Connect = %q, %v; want a CML 2 version", version, err)
	}
	labs, err := c.Labs(ctx)
	if err != nil || len(labs) == 0 || labs[0].ID == "" || labs[0].Title == "" {
		t.Fatalf("Labs = %+v, %v; want at least one titled lab", labs, err)
	}
	nodes, err := c.Nodes(ctx, labs[0].ID)
	if err != nil || len(nodes) == 0 {
		t.Fatalf("Nodes = %+v, %v; want the lab's nodes", nodes, err)
	}
	named := 0
	for _, n := range nodes {
		if n.ID == "" || n.Label == "" || n.NodeDefinition == "" || n.State == "" {
			t.Errorf("node %+v is missing a field the import reads", n)
		}
		named += len(n.ConfiguredAddresses)
	}
	if named == 0 {
		t.Error("no node's configuration named an address; the sandbox's routers each name several")
	}

	wrong, _ := cml.New(base, pinned(t, fingerprint), cml.Credentials{Username: user, Password: "not-the-password"})
	if _, err := wrong.Connect(ctx); err == nil || !strings.Contains(err.Error(), "refused the login") {
		t.Errorf("a wrong password = %v, want the login refused", err)
	}
	// The sandbox's certificate is its own CA's, so the system's CAs do
	// not vouch for it, and the refusal carries the fingerprint to check.
	unpinned, _ := cml.New(base, pinned(t, ""), cml.Credentials{Username: user, Password: password})
	var untrusted *devicetls.UntrustedError
	if _, err := unpinned.Connect(ctx); !errors.As(err, &untrusted) || untrusted.Fingerprint != strings.ToLower(fingerprint) {
		t.Errorf("unpinned = %v, want an UntrustedError naming %s", err, fingerprint)
	}
}
