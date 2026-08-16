// A controller starting and serving in a directory that used to block it
// forever, run through the shipped path rather than through the certificate
// package alone.
//
// internal/tlscert has its own recovery tests and they are not enough on
// their own: they prove the package returns usable material, while what an
// operator cares about is that the CONTROLLER starts, opens a real listener
// with what it was handed, and passes the container healthcheck it ships
// with. Every one of those steps sits between Ensure and a running
// deployment, and the healthcheck in particular has its own copy of the
// question, because it builds trust anchors out of the same directory.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/tlscert"
)

// TestControllerStartsInADirectoryThatUsedToBlockItForever is the evidence
// for the whole change, one subtest per state that used to be a permanent
// block.
//
// Each one ends with the shipped healthcheck subcommand probing the
// controller's own listener, which is the check an orchestrator runs and the
// one that decides whether a container lives.
func TestControllerStartsInADirectoryThatUsedToBlockItForever(t *testing.T) {
	tests := []struct {
		name string
		// wreck puts the directory into the state that used to block. It runs
		// after one successful start, so the directory holds real material
		// and real provenance records first.
		wreck func(t *testing.T, dir string)
	}{
		{
			name: "a serving bundle of zero length",
			wreck: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, tlscert.BundleFileName), nil)
			},
		},
		{
			name: "a serving bundle that is not PEM",
			wreck: func(t *testing.T, dir string) {
				writeTestFile(t, filepath.Join(dir, tlscert.BundleFileName), []byte("\x00not pem\n"))
			},
		},
		{
			name: "a serving bundle truncated past the certificate",
			wreck: func(t *testing.T, dir string) {
				path := filepath.Join(dir, tlscert.BundleFileName)
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("reading the bundle: %v", err)
				}
				writeTestFile(t, path, raw[:len(raw)/3])
			},
		},
		{
			name: "only a certificate, with no key anywhere",
			wreck: func(t *testing.T, dir string) {
				path := filepath.Join(dir, tlscert.CertFileName)
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("reading the certificate copy: %v", err)
				}
				if err := os.Remove(filepath.Join(dir, tlscert.BundleFileName)); err != nil {
					t.Fatalf("removing the bundle: %v", err)
				}
				if err := os.RemoveAll(filepath.Join(dir, tlscert.ProvisionedDirName)); err != nil {
					t.Fatalf("removing the records: %v", err)
				}
				writeTestFile(t, path, raw)
			},
		},
		{
			name: "a legacy provenance record nothing can read",
			wreck: func(t *testing.T, dir string) {
				if err := os.Mkdir(filepath.Join(dir, tlscert.ProvisionedFileName), 0o700); err != nil {
					t.Fatalf("putting a directory where the legacy record goes: %v", err)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearTLSEnvironment(t)
			dir := filepath.Join(t.TempDir(), "tls")
			t.Setenv("PLEIADES_TLS_AUTOCERT_DIR", dir)

			cfg, err := resolveTLS()
			if err != nil {
				t.Fatalf("resolveTLS(): %v", err)
			}
			// One ordinary start first, so the state under test is one a
			// working deployment fell into rather than one assembled from
			// nothing.
			captureProvisioning(t, cfg)
			tc.wreck(t, dir)

			serveAndProbe(t, cfg)

			// And the directory is working rather than survivable: a second
			// controller starts in it too.
			serveAndProbe(t, cfg)
		})
	}
}

// TestProvisionServingCertificate_NeverPrintsAKeyPathAsACertificate is the
// leak shape, asserted against the log line an operator actually reads.
//
// cert_file used to carry the path of serving.pem, which is the file holding
// the private key, and the documentation describes that field as a
// certificate path. An operator following it into a script would have handed
// this server's private key to something that only wanted the certificate.
func TestProvisionServingCertificate_NeverPrintsAKeyPathAsACertificate(t *testing.T) {
	clearTLSEnvironment(t)
	dir := filepath.Join(t.TempDir(), "tls")
	t.Setenv("PLEIADES_TLS_AUTOCERT_DIR", dir)

	cfg, err := resolveTLS()
	if err != nil {
		t.Fatalf("resolveTLS(): %v", err)
	}

	bundle := filepath.Join(dir, tlscert.BundleFileName)
	anchor := filepath.Join(dir, tlscert.CertFileName)
	// Both starts, because the reuse path is the one that was wrong: the
	// generate path already reported cert.pem.
	for _, logged := range []string{captureProvisioning(t, cfg), captureProvisioning(t, cfg)} {
		for _, field := range attributesOf(t, logged) {
			if !strings.Contains(field.name, "cert") && !strings.Contains(field.name, "anchor") {
				continue
			}
			if field.value == bundle {
				t.Errorf("the field %q carries %s, which holds the private key", field.name, bundle)
			}
		}
		if !strings.Contains(logged, `"trust_anchor_file":"`+anchor+`"`) {
			t.Errorf("the log does not offer the key-free copy as the trust anchor:\n%s", logged)
		}
		if !strings.Contains(logged, `"serving_file":"`+bundle+`"`) {
			t.Errorf("the log does not name the file the material really came from:\n%s", logged)
		}
	}

	// And the file offered as the trust anchor really has no key in it,
	// checked by parsing rather than by trusting its name.
	raw, err := os.ReadFile(anchor)
	if err != nil {
		t.Fatalf("reading the file offered as a trust anchor: %v", err)
	}
	for rest := raw; ; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if strings.Contains(block.Type, "PRIVATE KEY") {
			t.Fatalf("%s is offered as a certificate and holds a %s block", anchor, block.Type)
		}
	}
}

// TestProvisionServingCertificate_DoesNotClaimAnOperatorsCertificateAsItsOwn
// covers the false headline.
//
// A directory holding an operator's own certificate and key is served exactly
// as it is, and the startup line still said "serving TLS from a self-signed
// certificate this controller provisioned for itself". That names the wrong
// author, states a limitation that is not the real one, and points at
// settings that would not change anything.
func TestProvisionServingCertificate_DoesNotClaimAnOperatorsCertificateAsItsOwn(t *testing.T) {
	clearTLSEnvironment(t)
	dir := filepath.Join(t.TempDir(), "tls")
	t.Setenv("PLEIADES_TLS_AUTOCERT_DIR", dir)

	// An operator's own pair, dropped into the auto-provisioning directory:
	// generated elsewhere so this package holds no record of it, then copied
	// in under the two names the two-file layout uses.
	theirs, err := tlscert.Generate(filepath.Join(t.TempDir(), "operator"), tlscert.Options{})
	if err != nil {
		t.Fatalf("preparing an operator's pair: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}
	for _, name := range []string{tlscert.CertFileName, tlscert.KeyFileName} {
		raw, err := os.ReadFile(filepath.Join(filepath.Dir(theirs.CertFile), name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		writeTestFile(t, filepath.Join(dir, name), raw)
	}

	cfg, err := resolveTLS()
	if err != nil {
		t.Fatalf("resolveTLS(): %v", err)
	}
	logged := captureProvisioning(t, cfg)

	if strings.Contains(logged, "provisioned for itself") {
		t.Errorf("the controller claims an operator's certificate as one it wrote:\n%s", logged)
	}
	for _, want := range []string{"did not write", "never renewed or replaced", "TLS_CERT_FILE"} {
		if !strings.Contains(logged, want) {
			t.Errorf("the startup log does not mention %q:\n%s", want, logged)
		}
	}
}

// TestProvisionServingCertificate_ExplainsAFirstStart covers the most common
// start there is.
//
// replaced_because is the one place the reason a certificate was written
// exists. On a first start it used to be "stat /data/tls/cert.pem: no such
// file or directory", which reads like a fault rather than like a cold start.
func TestProvisionServingCertificate_ExplainsAFirstStart(t *testing.T) {
	clearTLSEnvironment(t)
	dir := filepath.Join(t.TempDir(), "tls")
	t.Setenv("PLEIADES_TLS_AUTOCERT_DIR", dir)

	cfg, err := resolveTLS()
	if err != nil {
		t.Fatalf("resolveTLS(): %v", err)
	}
	logged := captureProvisioning(t, cfg)

	if !strings.Contains(logged, "no certificate is stored in") {
		t.Errorf("a first start does not explain itself:\n%s", logged)
	}
	if strings.Contains(logged, "no such file or directory") {
		t.Errorf("a first start reports a raw syscall error:\n%s", logged)
	}
}

// serveAndProbe runs the shipped start-up path, opens a real TLS listener
// with exactly the material it returned, and probes it with the shipped
// healthcheck subcommand.
//
// The probe is the half that a certificate test cannot reach: it builds its
// trust anchors out of the same directory, so a recovery that left the
// anchors unbuildable would start a controller an orchestrator then kills.
func serveAndProbe(t *testing.T, cfg tlsSettings) {
	t.Helper()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, redact.Shared().HandlerOptions(slog.LevelInfo)))
	// The shipped entry point, not tlscert.Ensure: what is being proven is
	// that a CONTROLLER starts here.
	pair, err := prepareServingCertificate(cfg, logger)
	if err != nil {
		t.Fatalf("the directory is still blocked: %v", err)
	}
	if pair == nil || len(pair.Certificate) == 0 {
		t.Fatal("no certificate material came back for the listener to serve")
	}
	if !strings.Contains(buf.String(), `"level":"WARN"`) {
		t.Errorf("the start-up said nothing at WARN about the certificate it is serving:\n%s", buf.String())
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening on loopback: %v", err)
	}
	srv := &http.Server{
		Handler:           newReadinessRouter(t, func(context.Context) error { return nil }),
		ReadHeaderTimeout: 5 * time.Second,
		// Exactly the material prepareServingCertificate returned, which is
		// the whole reason it returns material rather than two paths.
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{*pair}},
	}
	go func() { _ = srv.ServeTLS(listener, "", "") }()
	t.Cleanup(func() { _ = srv.Close() })

	t.Setenv("LISTEN_ADDR", listener.Addr().String())
	if code := runHealthcheck([]string{healthcheckCommand}); code != 0 {
		t.Fatalf("the controller's own healthcheck against its own listener = %d, want 0", code)
	}
}

// logField is one attribute out of a JSON log line.
type logField struct {
	name  string
	value string
}

// attributesOf pulls the string attributes out of every JSON line in a
// captured log, so a test can ask what a FIELD carries rather than whether a
// substring appears somewhere.
func attributesOf(t *testing.T, logged string) []logField {
	t.Helper()

	var fields []logField
	for _, line := range strings.Split(strings.TrimSpace(logged), "\n") {
		if line == "" {
			continue
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(line), &decoded); err != nil {
			t.Fatalf("the log line %q is not JSON: %v", line, err)
		}
		for name, value := range decoded {
			if text, ok := value.(string); ok {
				fields = append(fields, logField{name: name, value: text})
			}
		}
	}
	return fields
}

// writeTestFile writes a file this test is planting, at the same 0600 this
// package writes its own material with.
func writeTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
