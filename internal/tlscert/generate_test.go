// The proof that Generate produces a certificate a real TLS server can
// present and a real client will accept.
//
// RULE 0 applies to a helper as much as to a feature: asserting on the
// fields of the parsed certificate would prove that x509.CreateCertificate
// copies a template, which nobody doubts. The tests that matter here run a
// real crypto/tls handshake against a real net/http server started from
// the two files this package wrote, which is the only thing that catches a
// missing IP SAN or a wrong extended key usage.
package tlscert_test

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/tlscert"
)

// serveTLS starts a real HTTPS server on loopback with the generated pair
// and returns the port it bound.
//
// http.Server plus ServeTLS, not httptest.NewTLSServer: httptest mints its
// own certificate and installs it in the client it hands back, so a test
// built on it would pass no matter what this package generated.
func serveTLS(t *testing.T, cert tlscert.ServingCert) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening on loopback: %v", err)
	}

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "served over tls")
		}),
		ReadHeaderTimeout: 5 * time.Second,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
	}
	go func() {
		// ServeTLS returns ErrServerClosed on the Close below, which is
		// the normal end of this goroutine rather than a failure.
		_ = srv.ServeTLS(listener, cert.CertFile, cert.KeyFile)
	}()
	t.Cleanup(func() { _ = srv.Close() })

	return listener.Addr().(*net.TCPAddr).Port
}

// TestGeneratedCertIsAcceptedByBothLoopbackNames covers the mistake this
// package exists to make impossible: a certificate valid for one of the
// two names a local client might dial and not the other.
func TestGeneratedCertIsAcceptedByBothLoopbackNames(t *testing.T) {
	cert, err := tlscert.Generate(t.TempDir(), tlscert.Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	port := serveTLS(t, cert)

	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: cert.TLSClientConfig()},
		Timeout:   10 * time.Second,
	}

	// Both spellings, because the IP SAN and the DNS SAN are separate
	// fields and one of them being right hides the other being wrong.
	for _, host := range []string{"127.0.0.1", "localhost"} {
		t.Run(host, func(t *testing.T) {
			resp, err := client.Get("https://" + net.JoinHostPort(host, strconv.Itoa(port)))
			if err != nil {
				t.Fatalf("GET over TLS by %s: %v", host, err)
			}
			defer func() { _ = resp.Body.Close() }()

			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK || string(body) != "served over tls" {
				t.Fatalf("GET = %d %q, want 200 and the handler's body", resp.StatusCode, body)
			}
		})
	}
}

// TestGeneratedCertIsNotTrustedWithoutItsPool is the control.
//
// Without it, the test above would pass just as happily against a client
// that verified nothing, which is the failure mode this whole package was
// written to avoid.
func TestGeneratedCertIsNotTrustedWithoutItsPool(t *testing.T) {
	cert, err := tlscert.Generate(t.TempDir(), tlscert.Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	port := serveTLS(t, cert)

	// An empty pool, so no authority is trusted at all. The system pool
	// would also reject this certificate, but an empty one cannot pass by
	// accident on a machine with an unusual trust store.
	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			RootCAs:    x509.NewCertPool(),
			MinVersion: tls.VersionTLS12,
		}},
		Timeout: 10 * time.Second,
	}

	resp, err := client.Get("https://127.0.0.1:" + strconv.Itoa(port))
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("a client trusting no authority accepted the self-signed certificate")
	}
}

// TestGeneratedFilesAreOwnerOnly pins the permissions on both the pair and
// the directory holding it. The private key is a secret in the ordinary
// sense, and the one caller that needs the files wider (tools/devcert)
// relaxes them deliberately, which a silent default of 0644 here would
// make invisible.
func TestGeneratedFilesAreOwnerOnly(t *testing.T) {
	// A subdirectory that does not exist yet, because the mode this checks
	// is only forced on a directory this package created.
	dir := filepath.Join(t.TempDir(), "tls")
	cert, err := tlscert.Generate(dir, tlscert.Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	for _, path := range []string{cert.CertFile, cert.KeyFile} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s has mode %#o, want 0600", path, perm)
		}
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat %s: %v", dir, err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("%s has mode %#o, want 0700", dir, perm)
	}
}

// TestGenerateHonoursTheRequestedTTL proves Options.TTL reaches the
// certificate, which is what lets a throwaway development certificate be
// short lived while the controller's persisted one is not.
func TestGenerateHonoursTheRequestedTTL(t *testing.T) {
	const want = 3 * time.Hour

	cert, err := tlscert.Generate(t.TempDir(), tlscert.Options{TTL: want})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Compared against the requested TTL plus the backdating the generator
	// applies, rather than against a literal, so the two cannot drift.
	if lifetime := cert.Leaf.NotAfter.Sub(cert.Leaf.NotBefore); lifetime > want+2*time.Minute || lifetime < want {
		t.Errorf("the certificate is valid for %v, want about %v", lifetime, want)
	}
}

// TestGenerateSortsExtraNamesIntoTheRightSANField is the mistake that
// produces a certificate looking correct in every dump and failing every
// handshake: an address listed as a DNS name, or a hostname listed as an
// IP.
func TestGenerateSortsExtraNamesIntoTheRightSANField(t *testing.T) {
	cert, err := tlscert.Generate(t.TempDir(), tlscert.Options{
		// A hostname, an IPv4 address, an IPv6 address, a name and an
		// address that each duplicate one already present (the name
		// differing only in case), and two entries a naive split of an
		// environment variable leaves behind.
		ExtraNames: []string{"controller.example.test", "10.1.2.3", "fd00::1", "LOCALHOST", "127.0.0.1", "", "  "},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	wantDNS := []string{"localhost", "controller.example.test"}
	if got := cert.Leaf.DNSNames; !equalStrings(got, wantDNS) {
		t.Errorf("DNS names = %v, want %v (a duplicate differing only in case must not become a second entry)", got, wantDNS)
	}

	wantIPs := []string{"127.0.0.1", "::1", "10.1.2.3", "fd00::1"}
	got := make([]string, 0, len(cert.Leaf.IPAddresses))
	for _, ip := range cert.Leaf.IPAddresses {
		got = append(got, ip.String())
	}
	if !equalStrings(got, wantIPs) {
		t.Errorf("IP addresses = %v, want %v", got, wantIPs)
	}
}

// TestGenerateRefusesADirectoryThatIsAFile is the same configuration
// mistake TestEnsureRefusesADirectoryThatIsAFile covers, on the other entry
// point, because the two resolve the directory through the same helper and
// a caller reaching either one deserves the same error naming the path.
func TestGenerateRefusesADirectoryThatIsAFile(t *testing.T) {
	occupied := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(occupied, []byte("this is a file"), 0o600); err != nil {
		t.Fatalf("writing %s: %v", occupied, err)
	}

	if _, err := tlscert.Generate(occupied, tlscert.Options{}); err == nil {
		t.Fatal("Generate accepted a path that is a file rather than a directory")
	} else if !strings.Contains(err.Error(), occupied) {
		t.Errorf("the error %q does not name the path that is wrong", err)
	}
}

// equalStrings compares two slices element by element, in order.
//
// Order matters here rather than only membership: the loopback names are
// always first, which is what makes cmd/controller's healthcheck able to
// send the first DNS name as SNI and reach the right listener.
func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if !strings.EqualFold(got[i], want[i]) {
			return false
		}
	}
	return true
}
