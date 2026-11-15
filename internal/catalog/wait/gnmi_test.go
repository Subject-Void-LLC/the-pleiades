// Tests for wait.gnmi against a gNMI server over a real loopback gRPC
// connection and real TLS (gnmitest) through the method's own client path: a
// server answering UNAVAILABLE for its first tries, as a starting gNMI
// service does, is waited through; one presenting a certificate the device
// does not trust ends the wait before any call is sent; and one refusing the
// login is sent it once.
package wait_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/wait"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi/gnmitest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// gnmiLogin is the credential the test servers ask for.
var gnmiLogin = gnmitest.Options{Username: "admin", Password: "wait-Gnmi-8"}

// lateGNMI is a server whose Capabilities answers UNAVAILABLE for its first
// bad calls, then answers.
func lateGNMI(t *testing.T, bad int32) *gnmitest.Server {
	t.Helper()
	var seen atomic.Int32
	opts := gnmiLogin
	opts.Hook = func(_ context.Context, rpc string) error {
		if strings.HasSuffix(rpc, "/Capabilities") && seen.Add(1) <= bad {
			return status.Error(codes.Unavailable, "gnmi server starting")
		}
		return nil
	}
	return gnmitest.New(t, opts)
}

func TestGNMI_Registered(t *testing.T) {
	d, ok := collection.Lookup("wait.gnmi")
	if !ok || d.Manifest.Status != collection.StatusImplemented || d.Invoke == nil {
		t.Fatalf("wait.gnmi: %+v", d.Manifest)
	}
	m := d.Manifest
	if m.Reversibility.Reversible || !m.Reversibility.ReadOnly || d.Check != nil || m.NoCheckReason == "" {
		t.Errorf("a wait changes nothing, has no check, and says why: %+v", m)
	}
	if len(m.RequiredCapabilities) != 1 || m.RequiredCapabilities[0] != capability.NameGNMI || m.SupportedTransports[0] != capability.TransportGNMI {
		t.Errorf("requirements %v over %v, want GNMICapable over gnmi", m.RequiredCapabilities, m.SupportedTransports)
	}
	for _, p := range m.Doc.Params {
		if strings.Contains(p.Name, "insecure") || strings.Contains(p.Name, "plaintext") {
			t.Errorf("wait.gnmi declares %s, a way to weaken the device's transport", p.Name)
		}
	}
}

// TestGNMI_WaitsThroughUnavailableUntilTheServerAnswers waits through three
// refusals, each try a client of its own.
func TestGNMI_WaitsThroughUnavailableUntilTheServerAnswers(t *testing.T) {
	s := lateGNMI(t, 3)
	rc := &waitContext{secrets: gnmitest.Secrets(s), stats: map[string]any{}}
	result, err := wait.GNMI(context.Background(), rc, gnmitest.NewDevice(t, s), map[string]any{"timeout": 60, "sleep": 1, "connect_timeout": 5})
	if err != nil || result.Changed {
		t.Fatalf("%+v, %v", result, err)
	}
	if rc.stats["version"] != "0.7.0" || rc.stats["failures"] != 3 || rc.stats["tries"] != 4 {
		t.Errorf("stats %v, want the version and three failed tries of four", rc.stats)
	}
	diff, _ := rc.stats[sdk.StatDiff].(map[string]any)
	if diff[sdk.DiffBefore].(map[string]any)["version"] != "0.7.0" {
		t.Errorf("diff %v", rc.stats[sdk.StatDiff])
	}
}

// TestGNMI_AnUntrustedCertificateStopsAtOnce pins another authority: the wait
// ends on its first try, having sent nothing.
func TestGNMI_AnUntrustedCertificateStopsAtOnce(t *testing.T) {
	s := gnmitest.New(t, gnmiLogin)
	other := gnmitest.New(t, gnmitest.Options{})
	rc := &waitContext{secrets: gnmitest.Secrets(s), stats: map[string]any{}}
	device := gnmitest.NewDeviceWith(t, s, map[string]inventory.PropertyValue{devicetls.CAPEMProperty: other.AuthorityPEM()})
	_, err := wait.GNMI(context.Background(), rc, device, map[string]any{"timeout": 60, "sleep": 1, "connect_timeout": 5})
	if err == nil || !strings.Contains(err.Error(), "do not trust") || len(s.RPCs()) != 0 {
		t.Fatalf("wait = %v after %v answered, want it stopped before any call", err, s.RPCs())
	}
}

// TestGNMI_ARefusedLoginIsSentOnce sends a wrong password once.
func TestGNMI_ARefusedLoginIsSentOnce(t *testing.T) {
	s := gnmitest.New(t, gnmiLogin)
	secrets := gnmitest.Secrets(s)
	secrets[wire.SecretPassword] = "wrong"
	rc := &waitContext{secrets: secrets, stats: map[string]any{}}
	_, err := wait.GNMI(context.Background(), rc, gnmitest.NewDevice(t, s), map[string]any{"timeout": 60, "sleep": 1, "connect_timeout": 5})
	if err == nil || !strings.Contains(err.Error(), "refused this login") || s.Refusals() != 1 {
		t.Fatalf("wait = %v after %d refusals, want one", err, s.Refusals())
	}
}

// TestGNMI_AStatItCannotRecordFailsTheTask: a ready server whose diff,
// version, tries or warnings stat cannot be recorded fails the task. The
// device pins the server's certificate, so it has a warning to record.
func TestGNMI_AStatItCannotRecordFailsTheTask(t *testing.T) {
	for _, key := range []string{sdk.StatDiff, "version", "tries", sdk.StatWarnings} {
		t.Run(key, func(t *testing.T) {
			s := gnmitest.New(t, gnmiLogin)
			rc := &waitContext{secrets: gnmitest.Secrets(s), stats: map[string]any{}, failOn: key}
			device := gnmitest.NewDeviceWith(t, s, map[string]inventory.PropertyValue{devicetls.FingerprintProperty: devicetls.Fingerprint(s.Certificate())})
			_, err := wait.GNMI(context.Background(), rc, device, map[string]any{"timeout": 10, "sleep": 1, "connect_timeout": 5})
			if !errors.Is(err, errWaitStat) {
				t.Errorf("failing the %s stat: %v", key, err)
			}
		})
	}
}

// TestGNMI_RefusesADeviceServingNoGNMI and a record it cannot use.
func TestGNMI_RefusesADeviceServingNoGNMI(t *testing.T) {
	rc := &waitContext{stats: map[string]any{}}
	if _, err := wait.GNMI(context.Background(), rc, nil, map[string]any{"timeout": 1}); err == nil {
		t.Error("no device: nil")
	}
	s := gnmitest.New(t, gnmiLogin)
	device := gnmitest.NewDevice(t, s)
	device.Auth = "basic"
	if _, err := wait.GNMI(context.Background(), &waitContext{secrets: gnmitest.Secrets(s), stats: map[string]any{}}, device, map[string]any{"timeout": 60, "sleep": 1}); err == nil || len(s.RPCs()) != 0 {
		t.Errorf("an unusable record: %v, want it refused at once", err)
	}
	if _, err := wait.GNMI(context.Background(), rc, gnmitest.NewDevice(t, s), map[string]any{"timeout": "soon"}); err == nil {
		t.Error("a malformed timeout: nil")
	}
}
