// A real-host check of how the Adapter treats a client certificate the
// host refuses: an answer, which is neither retried nor counted against
// the circuit. It runs whenever the WinRM certificate gates run
// (PLEIADES_WINRM_HOST and PLEIADES_WINRM_CA) and skips otherwise.
package winrm

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/breaker"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// TestStrangerCertificate_IsAnAnswerNotANetworkFailure presents a client
// certificate the host has never been told about, more times than the
// circuit's threshold, and proves each is sent once and none opens the
// circuit.
//
// A certificate refused during the TLS handshake reaches Go as a
// *net.OpError from the peer ("remote error"), which the retry used to
// treat as a network failure: three attempts per task, and, with the
// breaker, a wrong certificate on one task would have closed the address
// to every other. Windows may instead finish the handshake and refuse
// with an HTTP status; either way the host answered, and the log line
// records which it was.
func TestStrangerCertificate_IsAnAnswerNotANetworkFailure(t *testing.T) {
	host, caPath := os.Getenv("PLEIADES_WINRM_HOST"), os.Getenv("PLEIADES_WINRM_CA")
	if host == "" || caPath == "" {
		t.Skip("needs a real Windows host with a certificate listener: set PLEIADES_WINRM_HOST and PLEIADES_WINRM_CA")
	}
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := devicetls.Parse(inventory.NewProperties(map[string]inventory.PropertyValue{devicetls.CAPEMProperty: string(caPEM)}))
	if err != nil {
		t.Fatal(err)
	}

	var sent int
	var last error
	tr := newTransport(winrmexec.Options{Timeout: time.Minute}, func(ctx context.Context, target winrmexec.Target, auth winrmexec.Auth, cmd winrmexec.Command, opts winrmexec.Options) (winrmexec.Result, error) {
		sent++
		res, err := winrmexec.Execute(ctx, target, auth, cmd, opts)
		last = err
		return res, err
	})
	target := transport.Target{Endpoint: transport.NetworkEndpoint{Host: host, Port: 5986}, TLS: pinned}
	stranger := strangerCredential(t)

	tries := breaker.DefaultThreshold + 1
	for i := 0; i < tries; i++ {
		_, err := tr.Exec(context.Background(), target, stranger, `C:\Windows\System32\whoami.exe`)
		if err == nil {
			t.Fatal("the host accepted a certificate it was never told about")
		}
		if errors.Is(err, breaker.ErrOpen) {
			t.Fatalf("try %d: a refused certificate opened the circuit: %v", i, err)
		}
	}
	if sent != tries {
		t.Fatalf("sent %d times for %d tries: a refused certificate was retried", sent, tries)
	}
	t.Logf("the host refused the stranger with: %v", last)
}

// strangerCredential returns a client certificate and key, freshly made
// and self-signed, that no host has ever been told about.
func strangerCredential(t *testing.T) credential.Credential {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "stranger"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return credential.Credential{
		CertificatePEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		PrivateKeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}),
	}
}

// TestSilentPort_OpensItsOwnCircuitOnly points the Adapter at a port on
// the real host that nothing listens on, then at the host's real
// listener. The lab host's firewall drops packets to a closed port rather
// than refusing them, which is the case that used to be invisible: every
// attempt ended at the operation's bound with no shell open, reported as
// a command that "may still be running", so it was neither retried nor
// counted. The dead port's circuit must open within two tasks and then
// refuse at once, and the real port on the same host must still be sent
// to, because a circuit belongs to an address, not a host.
func TestSilentPort_OpensItsOwnCircuitOnly(t *testing.T) {
	host, caPath := os.Getenv("PLEIADES_WINRM_HOST"), os.Getenv("PLEIADES_WINRM_CA")
	if host == "" || caPath == "" {
		t.Skip("needs a real Windows host with a certificate listener: set PLEIADES_WINRM_HOST and PLEIADES_WINRM_CA")
	}
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := devicetls.Parse(inventory.NewProperties(map[string]inventory.PropertyValue{devicetls.CAPEMProperty: string(caPEM)}))
	if err != nil {
		t.Fatal(err)
	}
	var sent int
	tr := newTransport(winrmexec.Options{Timeout: 2 * time.Second}, func(ctx context.Context, target winrmexec.Target, auth winrmexec.Auth, cmd winrmexec.Command, opts winrmexec.Options) (winrmexec.Result, error) {
		sent++
		return winrmexec.Execute(ctx, target, auth, cmd, opts)
	})
	stranger := strangerCredential(t)
	dead := transport.Target{Endpoint: transport.NetworkEndpoint{Host: host, Port: 5987}, TLS: pinned}

	var last error
	for i := 0; i < 2; i++ {
		_, last = tr.Exec(context.Background(), dead, stranger, `C:\Windows\System32\whoami.exe`)
	}
	if !errors.Is(last, breaker.ErrOpen) || sent != breaker.DefaultThreshold {
		t.Fatalf("after two tasks: err = %v, sent = %d; want the circuit open after %d failures", last, sent, breaker.DefaultThreshold)
	}
	start := time.Now()
	if _, err := tr.Exec(context.Background(), dead, stranger, `C:\Windows\System32\whoami.exe`); !errors.Is(err, breaker.ErrOpen) {
		t.Fatalf("third task: err = %v, want a refusal from the open circuit", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond || sent != breaker.DefaultThreshold {
		t.Fatalf("the open circuit took %v and sent %d; it must refuse at once and send nothing", elapsed, sent)
	}

	live := transport.Target{Endpoint: transport.NetworkEndpoint{Host: host, Port: 5986}, TLS: pinned}
	if _, err := tr.Exec(context.Background(), live, stranger, `C:\Windows\System32\whoami.exe`); err == nil || errors.Is(err, breaker.ErrOpen) {
		t.Fatalf("the real listener: err = %v, want the host's own refusal of a stranger, not a circuit", err)
	}
	if sent != breaker.DefaultThreshold+1 {
		t.Fatalf("sent = %d, want the real listener's call sent", sent)
	}
}
