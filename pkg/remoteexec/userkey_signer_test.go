// Tests of the signer every stored key is wrapped in: the order it offers
// algorithms in, the refusal that holds a legacy signature to the record
// whichever way it is asked for, a certificate signed for through a real
// handshake, and FIPS 140 mode, run in a child process under a real
// GODEBUG=fips140=on.
package remoteexec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/fips140"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// newTestSigner returns key as an ssh.AlgorithmSigner, failing the test when
// it is not one.
func newTestSigner(t *testing.T, key any) ssh.AlgorithmSigner {
	t.Helper()
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	algorithmSigner, ok := signer.(ssh.AlgorithmSigner)
	if !ok {
		t.Fatalf("a %T signer cannot choose its algorithm", key)
	}
	return algorithmSigner
}

// newRSATestSigner returns a new RSA key's signer.
func newRSATestSigner(t *testing.T) ssh.AlgorithmSigner {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return newTestSigner(t, key)
}

// TestUserKeySigner_OffersSHA2First proves an RSA key offers rsa-sha2-512,
// then rsa-sha2-256, then ssh-rsa, and every other key its own format only.
func TestUserKeySigner_OffersSHA2First(t *testing.T) {
	rsaSigner := newUserKeySigner(newRSATestSigner(t), "h:22", Legacy{}, nil)
	if got := rsaSigner.Algorithms(); !slices.Equal(got, []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256, ssh.KeyAlgoRSA}) {
		t.Errorf("an RSA key offers %v", got)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if got := newUserKeySigner(newTestSigner(t, priv), "h:22", Legacy{}, nil).Algorithms(); !slices.Equal(got, []string{ssh.KeyAlgoED25519}) {
		t.Errorf("an ed25519 key offers %v", got)
	}
}

// TestUserKeySigner_EveryWayOfAskingIsHeld proves a legacy signature is
// refused whether asked for by name or through Sign, which signs with an RSA
// key's own format, ssh-rsa; that it is made once the record allows it; and
// that an algorithm the key cannot sign with is an error of its own.
func TestUserKeySigner_EveryWayOfAskingIsHeld(t *testing.T) {
	key := newRSATestSigner(t)
	use := &userKeyUse{}
	held := newUserKeySigner(key, "h:22", Legacy{}, use)
	if _, err := held.Sign(rand.Reader, []byte("data")); !isSignatureRefusal(err) {
		t.Fatalf("Sign on a record allowing nothing: %v, want a refusal", err)
	}
	if got := use.snapshot(); got.refused != ssh.KeyAlgoRSA || got.signed != "" {
		t.Errorf("recorded %+v, want the ssh-rsa refusal", got)
	}
	sig, err := held.SignWithAlgorithm(rand.Reader, []byte("data"), ssh.KeyAlgoRSASHA256)
	if err != nil || sig.Format != ssh.KeyAlgoRSASHA256 {
		t.Fatalf("a SHA-2 signature: %v, %v", sig, err)
	}
	if err := key.PublicKey().Verify([]byte("data"), sig); err != nil {
		t.Errorf("the SHA-2 signature does not verify: %v", err)
	}
	if got := use.snapshot(); got.signed != ssh.KeyAlgoRSASHA256 || got.refused != "" {
		t.Errorf("recorded %+v, want the SHA-2 signature", got)
	}
	if _, err := held.SignWithAlgorithm(rand.Reader, []byte("data"), ssh.KeyAlgoED25519); err == nil || isSignatureRefusal(err) {
		t.Errorf("an RSA key asked for an ed25519 signature: %v, want an error that is not a legacy refusal", err)
	}

	allowed := newUserKeySigner(key, "h:22", Legacy{UserKeySignatures: true}, nil)
	legacySig, err := allowed.Sign(rand.Reader, []byte("data"))
	if err != nil || legacySig.Format != ssh.KeyAlgoRSA {
		t.Fatalf("Sign on a record allowing ssh-rsa: %v, %v", legacySig, err)
	}
	if err := key.PublicKey().Verify([]byte("data"), legacySig); err != nil {
		t.Errorf("the ssh-rsa signature does not verify: %v", err)
	}
}

// isSignatureRefusal reports whether err is the signer's own refusal.
func isSignatureRefusal(err error) bool {
	_, ok := errors.AsType[*LegacySignatureRefusedError](err)
	return ok
}

// TestUserKeySigner_AnOuterWrapperCannotGetAroundIt proves a signer built on
// top of the wrapped key and restricted to ssh-rsa, as the library builds one
// to retry an RSA certificate on OpenSSH 7.2 to 7.7, still reaches the refusal.
func TestUserKeySigner_AnOuterWrapperCannotGetAroundIt(t *testing.T) {
	outer, err := ssh.NewSignerWithAlgorithms(newUserKeySigner(newRSATestSigner(t), "h:22", Legacy{}, nil), []string{ssh.KeyAlgoRSA})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := outer.(ssh.AlgorithmSigner).SignWithAlgorithm(rand.Reader, []byte("data"), ssh.KeyAlgoRSA); !isSignatureRefusal(err) {
		t.Errorf("an outer ssh-rsa-only signer: %v, want the refusal", err)
	}
}

// TestKeyAuth_RefusesASignerThatCannotChoose proves a signer that cannot
// choose its algorithm is refused, since the library would sign with its
// key's own format, ssh-rsa for an RSA key, and nothing could hold it.
func TestKeyAuth_RefusesASignerThatCannotChoose(t *testing.T) {
	if _, err := keyAuth("u", plainSigner{newRSATestSigner(t)}); err == nil || !strings.Contains(err.Error(), "cannot choose its signature algorithm") {
		t.Errorf("keyAuth = %v, want a refusal", err)
	}
}

// plainSigner is an ssh.Signer that hides every other interface its key has.
type plainSigner struct{ s ssh.Signer }

// PublicKey returns the key's public half.
func (p plainSigner) PublicKey() ssh.PublicKey { return p.s.PublicKey() }

// Sign signs with the key's own format.
func (p plainSigner) Sign(r io.Reader, data []byte) (*ssh.Signature, error) { return p.s.Sign(r, data) }

// TestConnect_AnRSACertificateIsHeldToo proves an RSA certificate, which the
// library signs for with its key's algorithm, is refused on a server that
// takes it only with ssh-rsa unless the record sets the property, through a
// real handshake with a server that trusts the certificate's authority.
func TestConnect_AnRSACertificateIsHeldToo(t *testing.T) {
	_, caKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := newTestSigner(t, caKey)
	key := newRSATestSigner(t)
	cert := &ssh.Certificate{Key: key.PublicKey(), CertType: ssh.UserCert, ValidPrincipals: []string{"u"}, ValidBefore: ssh.CertTimeInfinity}
	if err := cert.SignCert(rand.Reader, ca); err != nil {
		t.Fatal(err)
	}
	certSigner, err := ssh.NewCertSigner(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := keyAuth("u", certSigner)
	if err != nil {
		t.Fatal(err)
	}

	verified, err := certLogin(t, ca.PublicKey(), auth, Legacy{})
	if refused, ok := errors.AsType[*LegacySignatureRefusedError](err); !ok || refused.Key != "RSA key certificate" || len(verified) != 0 {
		t.Fatalf("a certificate on a record allowing nothing: %v (verified %v), want a refusal", err, verified)
	}
	verified, err = certLogin(t, ca.PublicKey(), auth, Legacy{UserKeySignatures: true})
	if err != nil || !slices.Equal(verified, []string{ssh.KeyAlgoRSA}) {
		t.Fatalf("a certificate on a record allowing ssh-rsa: %v, verified %v", err, verified)
	}
}

// certLogin logs in once with auth, under legacy, to a server that trusts
// certificates ca signs and takes a key only with ssh-rsa, returning the
// signature algorithms the server verified.
func certLogin(t *testing.T, ca ssh.PublicKey, auth Auth, legacy Legacy) ([]string, error) {
	t.Helper()
	_, hostKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	host := newTestSigner(t, hostKey)
	checker := &ssh.CertChecker{IsUserAuthority: func(auth ssh.PublicKey) bool { return bytes.Equal(auth.Marshal(), ca.Marshal()) }}
	var mu sync.Mutex
	var verified []string
	config := &ssh.ServerConfig{
		PublicKeyCallback:       checker.Authenticate,
		PublicKeyAuthAlgorithms: []string{ssh.KeyAlgoRSA},
		VerifiedPublicKeyCallback: func(_ ssh.ConnMetadata, _ ssh.PublicKey, perms *ssh.Permissions, algorithm string) (*ssh.Permissions, error) {
			mu.Lock()
			defer mu.Unlock()
			verified = append(verified, algorithm)
			return perms, nil
		},
	}
	config.AddHostKey(host)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	served := make(chan struct{})
	go func() {
		defer close(served)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if sc, chans, reqs, err := ssh.NewServerConn(conn, config); err == nil {
			go ssh.DiscardRequests(reqs)
			go func() {
				for ch := range chans {
					_ = ch.Reject(ssh.Prohibited, "no channels")
				}
			}()
			_ = sc.Wait()
		}
	}()

	addr := listener.Addr().String()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	client, chans, reqs, err := ssh.NewClientConn(conn, addr, auth.clientConfig(ssh.FixedHostKey(host.PublicKey()), addr, 5*time.Second, legacy, nil))
	if err == nil {
		_ = ssh.NewClient(client, chans, reqs).Close()
	}
	<-served
	mu.Lock()
	defer mu.Unlock()
	return verified, err
}

// fipsChild names the environment variable that runs
// TestUserKeySigner_FIPSModeRefusesByName's body in the child process.
const fipsChild = "REMOTEEXEC_TEST_FIPS_CHILD"

// TestUserKeySigner_FIPSModeRefusesByName proves that under FIPS 140 mode a
// legacy signature is refused by this package, without naming the property,
// even on a record that sets it, while a SHA-2 signature is still made. FIPS
// mode is set only at process start, so the body runs in a child process of
// this test binary under GODEBUG=fips140=on.
func TestUserKeySigner_FIPSModeRefusesByName(t *testing.T) {
	if os.Getenv(fipsChild) == "1" {
		if !fips140.Enabled() {
			t.Fatal("the child is not in FIPS 140 mode")
		}
		signer := newUserKeySigner(newRSATestSigner(t), "h:22", Legacy{UserKeySignatures: true}, nil)
		_, err := signer.SignWithAlgorithm(rand.Reader, []byte("data"), ssh.KeyAlgoRSA)
		refused, ok := errors.AsType[*LegacySignatureRefusedError](err)
		if !ok || refused.Property != "" || !strings.Contains(err.Error(), "FIPS 140 mode forbids") {
			t.Fatalf("ssh-rsa under FIPS 140 mode: %v, want a refusal naming FIPS and no property", err)
		}
		if _, err := signer.SignWithAlgorithm(rand.Reader, []byte("data"), ssh.KeyAlgoRSASHA512); err != nil {
			t.Fatalf("rsa-sha2-512 under FIPS 140 mode: %v", err)
		}
		return
	}
	if fips140.Enabled() {
		t.Skip("this process is already in FIPS 140 mode; the child would prove nothing more")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestUserKeySigner_FIPSModeRefusesByName$", "-test.count=1", "-test.v") // #nosec G204 -- this test binary, re-run for one test
	cmd.Env = append(os.Environ(), "GODEBUG=fips140=on", fipsChild+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "--- PASS: TestUserKeySigner_FIPSModeRefusesByName") {
		t.Fatalf("the FIPS 140 child: %v\n%s", err, out)
	}
}
