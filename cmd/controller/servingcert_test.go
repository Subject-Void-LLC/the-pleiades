// Tests for the certificate this process ends up serving, in all three
// transport arrangements.
//
// They run from inside package main because the functions are unexported and
// because what they decide (which certificate a listener presents) is not
// observable from outside without standing up a real server. The half that
// IS observable that way, namely that the probe and the listener agree, is
// proven against a real HTTPS server in healthcheck_test.go.
package main

import (
	"bytes"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/tlscert"
)

// TestProvisionServingCertificate_GeneratesThenReuses is the behavior an
// operator actually notices. A controller that minted a fresh certificate
// on every restart would retrain everybody to click through browser
// warnings and would break any client that pinned the previous one.
func TestProvisionServingCertificate_GeneratesThenReuses(t *testing.T) {
	clearTLSEnvironment(t)
	dir := filepath.Join(t.TempDir(), "tls")
	t.Setenv("PLEIADES_TLS_AUTOCERT_DIR", dir)

	cfg, err := resolveTLS()
	if err != nil {
		t.Fatalf("resolveTLS(): %v", err)
	}

	first := captureProvisioning(t, cfg)
	if !strings.Contains(first, `"provisioning":"generated"`) {
		t.Errorf("the first start did not report a generated certificate:\n%s", first)
	}
	firstBytes := readFile(t, cfg.CertFile)

	second := captureProvisioning(t, cfg)
	if !strings.Contains(second, `"provisioning":"reused"`) {
		t.Errorf("the second start did not reuse the stored certificate:\n%s", second)
	}
	if !bytes.Equal(firstBytes, readFile(t, cfg.CertFile)) {
		t.Error("the second start replaced the certificate on disk instead of reusing it")
	}
}

// TestProvisionServingCertificate_SaysWhatItIsAndHowToReplaceIt pins the
// loud half.
//
// A convenience that quietly degrades a security property has to keep
// saying so. This asserts the exact facts an operator needs: that the
// certificate is self-signed and therefore encrypts without
// authenticating, where it lives, when it expires, and the names of the
// settings that replace it with a real one. Dropping any of them is how a
// deployment meant to be temporary becomes the one still running in a year.
func TestProvisionServingCertificate_SaysWhatItIsAndHowToReplaceIt(t *testing.T) {
	clearTLSEnvironment(t)
	dir := filepath.Join(t.TempDir(), "tls")
	t.Setenv("PLEIADES_TLS_AUTOCERT_DIR", dir)

	cfg, err := resolveTLS()
	if err != nil {
		t.Fatalf("resolveTLS(): %v", err)
	}
	logged := captureProvisioning(t, cfg)

	for _, want := range []string{
		"self-signed",
		"does not authenticate",
		"TLS_CERT_FILE",
		"TLS_KEY_FILE",
		"PLEIADES_TLS_TERMINATED_UPSTREAM",
		autocertHostsVar,
		"PLEIADES_TLS_AUTOCERT_DIR=" + dir,
		// The paths and the expiry, which are what an operator needs in
		// order to hand the certificate to a client or to know when it
		// stops working. The path assertion doubles as proof that the
		// masking ruleset does not eat these attribute names.
		filepath.Join(dir, tlscert.CertFileName),
		filepath.Join(dir, tlscert.BundleFileName),
		`"expires":"`,
		// WARN, not INFO. A line at info level in a JSON log nobody greps
		// is not a warning anybody receives.
		`"level":"WARN"`,
	} {
		if !strings.Contains(logged, want) {
			t.Errorf("the startup log does not mention %q:\n%s", want, logged)
		}
	}
}

// TestPrepareServingCertificate_AConfiguredPairBeatsGeneration is the guard
// that nothing executed.
//
// The rule "an operator's own certificate always wins" lived as a bare `if`
// inside main(), which no test runs, so nothing in the suite would have
// noticed if a refactor made a configured deployment quietly provision a
// self-signed certificate beside the real one and serve the wrong identity.
// The rule now lives in a function, and this is the test of it: with
// TLS_CERT_FILE and TLS_KEY_FILE set, the auto-provisioning directory is
// still empty afterwards and the material served is the configured pair.
func TestPrepareServingCertificate_AConfiguredPairBeatsGeneration(t *testing.T) {
	clearTLSEnvironment(t)

	configured, err := tlscert.Generate(filepath.Join(t.TempDir(), "operator"), tlscert.Options{})
	if err != nil {
		t.Fatalf("preparing an operator's certificate: %v", err)
	}
	autocertDir := filepath.Join(t.TempDir(), "tls")
	t.Setenv("TLS_CERT_FILE", configured.CertFile)
	t.Setenv("TLS_KEY_FILE", configured.KeyFile)
	t.Setenv("PLEIADES_TLS_AUTOCERT_DIR", autocertDir)

	cfg, err := resolveTLS()
	if err != nil {
		t.Fatalf("resolveTLS(): %v", err)
	}
	if cfg.Mode != tlsModeServe {
		t.Fatalf("resolveTLS() mode = %v with a configured pair, want tlsModeServe", cfg.Mode)
	}

	var buf bytes.Buffer
	pair, err := prepareServingCertificate(cfg, slog.New(slog.NewJSONHandler(&buf, nil)))
	if err != nil {
		t.Fatalf("prepareServingCertificate: %v", err)
	}

	// The identity actually being served, compared against the configured
	// one. A path comparison would pass even if the material behind it were
	// something else.
	if pair == nil || len(pair.Certificate) == 0 || !bytes.Equal(pair.Certificate[0], configured.Leaf.Raw) {
		t.Error("the material returned for the listener is not the operator's configured certificate")
	}
	// And nothing was written anywhere else, which is the half a silent
	// regression would break.
	if _, err := os.Stat(autocertDir); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s exists (stat error %v), so a configured deployment provisioned a certificate it will never serve", autocertDir, err)
	}
}

// TestPrepareServingCertificate_SaysSoWhenAConfiguredCertificateHasExpired
// is the silent outage.
//
// An expired certificate fails every handshake. Replacing it is not an
// option: substituting a self-signed one would serve a different identity
// than every client of this deployment was told to expect, and would make
// the failure look like a recovery. Serving it is right and saying nothing
// is not, because "the site stopped working and nothing was logged" is a far
// longer outage than "the site stopped working and the controller named the
// expiry".
func TestPrepareServingCertificate_SaysSoWhenAConfiguredCertificateHasExpired(t *testing.T) {
	clearTLSEnvironment(t)

	// A lifetime that runs out while this test is starting. Generate always
	// dates from the current clock, so this is the only way to get a real
	// expired pair out of the shipped generator rather than a hand-made one.
	expired, err := tlscert.Generate(filepath.Join(t.TempDir(), "operator"), tlscert.Options{TTL: time.Millisecond})
	if err != nil {
		t.Fatalf("preparing an expired certificate: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if !time.Now().After(expired.Leaf.NotAfter) {
		t.Fatalf("the planted certificate is still valid until %s, so this test proves nothing", expired.Leaf.NotAfter)
	}

	t.Setenv("TLS_CERT_FILE", expired.CertFile)
	t.Setenv("TLS_KEY_FILE", expired.KeyFile)

	cfg, err := resolveTLS()
	if err != nil {
		t.Fatalf("resolveTLS(): %v", err)
	}

	var buf bytes.Buffer
	pair, err := prepareServingCertificate(cfg, slog.New(slog.NewJSONHandler(&buf, redact.Shared().HandlerOptions(slog.LevelInfo))))
	if err != nil {
		t.Fatalf("prepareServingCertificate refused to start rather than serving the expired certificate: %v", err)
	}
	if pair == nil || !bytes.Equal(pair.Certificate[0], expired.Leaf.Raw) {
		t.Error("the certificate served is not the one the operator configured, so a different identity was substituted")
	}

	logged := buf.String()
	for _, want := range []string{
		"expired",
		expired.CertFile,
		// ERROR, because every handshake is failing. A line at WARN reads
		// like a thing to look at later.
		`"level":"ERROR"`,
		// And why it was not swapped out, or the next reader "fixes" this by
		// adding a fallback that changes the server's identity.
		"different identity",
	} {
		if !strings.Contains(logged, want) {
			t.Errorf("the startup log does not mention %q:\n%s", want, logged)
		}
	}
}

// TestPrepareServingCertificate_UpstreamLoadsNothing pins the third mode:
// there is no certificate because there is no TLS on this listener, and the
// controller still has to say that plain HTTP is what an ingress promised to
// cover.
func TestPrepareServingCertificate_UpstreamLoadsNothing(t *testing.T) {
	clearTLSEnvironment(t)
	t.Setenv("PLEIADES_TLS_TERMINATED_UPSTREAM", "true")

	cfg, err := resolveTLS()
	if err != nil {
		t.Fatalf("resolveTLS(): %v", err)
	}

	var buf bytes.Buffer
	pair, err := prepareServingCertificate(cfg, slog.New(slog.NewJSONHandler(&buf, nil)))
	if err != nil {
		t.Fatalf("prepareServingCertificate: %v", err)
	}
	if pair != nil {
		t.Error("a certificate was loaded for a listener that serves plain HTTP")
	}
	if !strings.Contains(buf.String(), "plain HTTP") {
		t.Errorf("the startup log does not say the listener is plain HTTP:\n%s", buf.String())
	}
}

// TestAutocertOptions_RefusesANameACertificateCannotCarry is the
// configuration error reported where an operator can act on it.
//
// The failure it replaces came out of crypto/x509 as
// "SAN dNSName is malformed", naming neither the value nor the variable it
// was read from.
func TestAutocertOptions_RefusesANameACertificateCannotCarry(t *testing.T) {
	clearTLSEnvironment(t)
	t.Setenv(autocertHostsVar, "controller,contrôleur.example.test")

	_, err := resolveTLS()
	if err == nil {
		t.Fatal("resolveTLS accepted a subject alternative name a certificate cannot carry")
	}
	for _, want := range []string{autocertHostsVar, "contr", "punycode"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error %q does not mention %q", err, want)
		}
	}
}

// captureProvisioning runs the real provisioning step against a logger
// writing into a buffer, and returns everything it logged.
//
// The logger is built exactly the way main() builds its own, masking
// ruleset included, so this test cannot pass on a line the shipped binary
// would have redacted.
func captureProvisioning(t *testing.T, cfg tlsSettings) string {
	t.Helper()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, redact.Shared().HandlerOptions(slog.LevelInfo)))
	pair, err := prepareServingCertificate(cfg, logger)
	if err != nil {
		t.Fatalf("prepareServingCertificate: %v", err)
	}
	if cfg.ServesTLS() && (pair == nil || len(pair.Certificate) == 0) {
		t.Fatal("no certificate material came back, so the listener would have to re-read the files and could race a second controller")
	}
	return buf.String()
}

// readFile reads a file the test just caused to be written, failing rather
// than returning an error.
func readFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return raw
}
