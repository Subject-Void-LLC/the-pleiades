// Tests for the gNMI probe through the real onboarding path, against a gNMI
// server over a real loopback gRPC connection (pkg/gnmi/gnmitest).
package onboard

import (
	"context"
	"errors"
	"strings"
	"testing"

	gpb "github.com/openconfig/gnmi/proto/gnmi"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi/gnmitest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// gnmiLogin is the credential the test servers ask for.
var gnmiLogin = gnmitest.Options{Username: "admin", Password: "gnmi-Onboard-5"}

// gnmiRepo is a project holding one generic_gnmi device for s, its
// authority pinned, and a function onboarding it with secrets.
func gnmiRepo(t *testing.T, name string, s *gnmitest.Server, secrets map[string]string) func() (Result, error) {
	t.Helper()
	repo, _ := newRepo(t, name, generic.TypeGNMI, map[string]inventory.PropertyValue{
		devicetls.CAPEMProperty: s.AuthorityPEM(), generic.GRPCTargetProperty: s.Addr(),
	})
	return func() (Result, error) {
		return onboardWith(context.Background(), repo, name, staticSecrets(secrets), fixedNow, Lookup, options{})
	}
}

// TestGNMIProbe_OnboardsFromTheServersCapabilities walks a new generic_gnmi
// device to active and checks what onboarding recorded, with the count as
// text so a second onboarding of the unchanged device writes nothing.
func TestGNMIProbe_OnboardsFromTheServersCapabilities(t *testing.T) {
	s := gnmitest.New(t, gnmiLogin)
	run := gnmiRepo(t, "g1", s, gnmitest.Secrets(s))
	res, err := run()
	if err != nil {
		t.Fatal(err)
	}
	if res.State != "active" || res.Protocol != "gnmi" || !res.Changed {
		t.Fatalf("result %+v, want the device active through the gnmi probe", res)
	}
	if res.Facts["gnmi_version"] != "0.7.0" || res.Facts["gnmi_model_count"] != "3" || res.Facts["gnmi_models_digest"] == "" {
		t.Errorf("facts %v", res.Facts)
	}
	if encs, _ := res.Facts["gnmi_encodings"].([]any); len(encs) != 3 || encs[0] != "json" {
		t.Errorf("gnmi_encodings = %v, want the server's three", res.Facts["gnmi_encodings"])
	}
	if !strings.Contains(strings.Join(res.Capabilities, ","), string(capability.NameGNMI)) {
		t.Errorf("capabilities %v, want GNMICapable granted", res.Capabilities)
	}
	if _, complete := res.Facts["yang_completeness"]; complete {
		t.Error("the probe says how complete a module list is, and Capabilities is not a YANG library")
	}
	again, err := run()
	if err != nil {
		t.Fatal(err)
	}
	if again.Changed {
		t.Errorf("a second onboarding of an unchanged device changed the record: %+v", again)
	}
}

// TestGNMIProbe_ARefusedCredentialProvesNothing sends a wrong password once.
func TestGNMIProbe_ARefusedCredentialProvesNothing(t *testing.T) {
	s := gnmitest.New(t, gnmiLogin)
	run := gnmiRepo(t, "g1", s, map[string]string{wire.SecretUsername: "admin", wire.SecretPassword: "wrong"})
	res, err := run()
	var refused *gnmi.RefusedError
	if !errors.As(err, &refused) || res.State == "active" || s.Refusals() != 1 {
		t.Errorf("onboarding with a wrong password = %+v, %v after %d refusals; want one refusal and not active", res, err, s.Refusals())
	}
}

// TestGNMIProbe_AnUntrustedCertificateProvesNothing pins another authority.
func TestGNMIProbe_AnUntrustedCertificateProvesNothing(t *testing.T) {
	s := gnmitest.New(t, gnmiLogin)
	other := gnmitest.New(t, gnmitest.Options{})
	repo, _ := newRepo(t, "g1", generic.TypeGNMI, map[string]inventory.PropertyValue{
		devicetls.CAPEMProperty: other.AuthorityPEM(), generic.GRPCTargetProperty: s.Addr(),
	})
	res, err := onboardWith(context.Background(), repo, "g1", staticSecrets(gnmitest.Secrets(s)), fixedNow, Lookup, options{})
	var untrusted *devicetls.UntrustedError
	if !errors.As(err, &untrusted) || res.State == "active" || len(s.RPCs()) != 0 {
		t.Errorf("onboarding = %+v, %v with %v answered; want the certificate refused and nothing sent", res, err, s.RPCs())
	}
}

// TestGNMIProbe_OverPlaintextSaysNothingIsVerified onboards a Stratum-like
// server that takes no credential over plaintext, and holds the result to
// saying so on every line it could.
func TestGNMIProbe_OverPlaintextSaysNothingIsVerified(t *testing.T) {
	s := gnmitest.New(t, gnmitest.Options{Stratum: true, Plaintext: true, Models: []*gpb.ModelData{{Name: "openconfig-interfaces", Version: "2.4.1"}, {Name: "bad\x07model"}}})
	item := build(t, generic.TypeGNMI, map[string]inventory.PropertyValue{
		generic.GRPCTargetProperty: s.Addr(), generic.GRPCPlaintextProperty: true, generic.GNMIAuthProperty: gnmi.AuthNone,
	})
	probed, err := gnmiProber{}.Probe(context.Background(), item, nil)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(probed.Warnings, "\n")
	for _, want := range []string{"without TLS (grpc_plaintext)", "nothing proves which server answered this probe", "1 malformed"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings %q, want %q said", joined, want)
		}
	}
	if probed.Facts["gnmi_model_count"] != "1" {
		t.Errorf("facts %v, want the one good model", probed.Facts)
	}
	if (gnmiProber{}).Protocol() != "gnmi" {
		t.Error("the probe does not name its protocol")
	}
}
