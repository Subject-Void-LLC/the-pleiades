// The things this package must never do, each one proven by making it try.
//
// Every case here was found by an adversarial pass over the auto-provisioning
// work rather than by a failing build, and every one of them is silent: a
// destroyed private key, a process that hangs with no log line, a boot
// refusal over an optimisation, an expired certificate served without a
// word, and a configuration mistake reported from somewhere that names
// neither the value nor the setting it came from.
package tlscert_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/tlscert"
)

// TestEnsureNeverReplacesAnOperatorsOwnPair is the destroyed-key case.
//
// An operator who copies their real certificate and key into the directory
// this package manages has done something reasonable and slightly wrong. The
// wrong part is fixable with one environment variable. What is not fixable is
// a private key overwritten by a self-signed one, because that key exists in
// exactly one place and its certificate was issued against it.
//
// The pair planted here fails every reuse rule this package has: it carries
// none of the loopback names and it is already expired. Under the rules
// alone it would be replaced twice over.
func TestEnsureNeverReplacesAnOperatorsOwnPair(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	planted := plantOperatorPair(t, dir, "operator.example.test", time.Now().Add(-90*24*time.Hour), time.Now().Add(-time.Hour))

	got, err := tlscert.Ensure(dir, tlscert.Options{})
	if err != nil {
		t.Fatalf("Ensure refused to start over an operator's own certificate rather than serving it: %v", err)
	}
	if got.Generated {
		t.Fatal("Ensure generated a replacement, which means it destroyed a private key that exists nowhere else")
	}
	if got.SelfProvisioned {
		t.Error("Ensure reported an operator's certificate as one it provisioned")
	}

	// The bytes on disk, because that is what was at risk. A returned value
	// that happens to match proves nothing about what is still in the file.
	onDisk := readPEMBlock(t, filepath.Join(dir, tlscert.CertFileName))
	if !onDisk.Equal(planted) {
		t.Error("the certificate on disk is not the one the operator put there")
	}
	if _, err := os.Stat(filepath.Join(dir, tlscert.KeyFileName)); err != nil {
		t.Errorf("the operator's key is gone: %v", err)
	}

	// And the caller has to be told, or the operator learns about it when
	// the certificate expires and nothing renewed it. The message has to name
	// the file that stopped the replacement, because "somewhere in this
	// directory" is not something an operator can act on.
	for _, want := range []string{filepath.Join(dir, tlscert.KeyFileName), "not written by this controller", "never", "TLS_CERT_FILE"} {
		if !strings.Contains(got.Warning, want) {
			t.Errorf("the warning %q does not mention %q", got.Warning, want)
		}
	}
}

// TestEnsureStartsBesideBytesItCannotRead is the rule's boundary, and it is
// the boundary that used to be in the wrong place.
//
// A file that is not a certificate, not a key and not PEM at all is not a
// secret: nothing can serve it and nobody can lose it. Refusing to start over
// one blocked every controller in the directory forever and protected
// nothing. So the start succeeds. What does NOT change is the bytes: refusing
// to overwrite a file and refusing to start are different promises, and only
// the first was ever worth making, so those bytes are still there afterwards
// and the file is never offered as this server's trust anchor.
func TestEnsureStartsBesideBytesItCannotRead(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}
	certPath := filepath.Join(dir, tlscert.CertFileName)
	planted := []byte("this is somebody's certificate in a format this does not read")
	writeFile(t, certPath, planted)

	got, err := tlscert.Ensure(dir, tlscert.Options{})
	if err != nil {
		t.Fatalf("Ensure refused to start over bytes that are not certificate material: %v", err)
	}
	if !got.Generated || !got.SelfProvisioned {
		t.Error("Ensure did not provision a certificate of its own")
	}
	// The bytes are untouched, which is the half of the old rule that stays.
	raw, readErr := os.ReadFile(certPath)
	if readErr != nil || !bytes.Equal(raw, planted) {
		t.Errorf("the file was modified (read %q, error %v)", raw, readErr)
	}
	// And it is not reported as a certificate a client could be handed,
	// because it is not one.
	if anchor, ok := got.AnchorFile(); ok {
		t.Errorf("AnchorFile() = %q, but %s does not hold the certificate being served", anchor, certPath)
	}

	// The whole point: this directory works now. A second controller starting
	// here reuses what the first one published.
	again, err := tlscert.Ensure(dir, tlscert.Options{})
	if err != nil {
		t.Fatalf("a second controller could not start in the same directory: %v", err)
	}
	if again.Generated || !again.Leaf.Equal(got.Leaf) {
		t.Error("the second controller did not reuse the published certificate")
	}
}

// TestEnsureServesAStoredCertificateWhenItCannotWriteOne is the
// unwritable-directory case.
//
// A volume remounted read-only, a permission change, a full disk: the
// directory holds a certificate that is perfectly good for another
// fortnight and has just entered its renewal window. Renewal is an
// optimisation and serving is the job, so refusing to start here takes a
// controller that would have worked and stops it.
func TestEnsureServesAStoredCertificateWhenItCannotWriteOne(t *testing.T) {
	if os.Geteuid() == 0 {
		// Root ignores the write bit, so the premise of this test cannot be
		// established. Skipped rather than quietly passing for the wrong
		// reason.
		t.Skip("running as root, which can write to a directory with no write bit")
	}
	if runtime.GOOS == "windows" {
		t.Skip("directory permissions do not work this way on Windows")
	}

	dir := filepath.Join(t.TempDir(), "tls")
	// Two hours of life against the default thirty-day renewal window, so
	// the stored certificate is deep inside its window and Ensure certainly
	// wants to replace it.
	stored, err := tlscert.Generate(dir, tlscert.Options{TTL: 2 * time.Hour})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("making %s read-only: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	got, err := tlscert.Ensure(dir, tlscert.Options{})
	if err != nil {
		t.Fatalf("Ensure refused to start rather than serving the certificate it already had: %v", err)
	}
	if !got.Leaf.Equal(stored.Leaf) {
		t.Error("Ensure returned a certificate other than the one on disk")
	}
	if got.Warning == "" {
		t.Error("a controller serving a certificate it could not renew said nothing about it")
	}
}

// TestEnsureRefusesABlockingFile is the hang.
//
// A named pipe at cert.pem makes open(2) block forever with no writer, so a
// controller pointed at one stays alive, never finishes starting, and logs
// nothing at all. Every signal an operator has says the process is fine.
//
// The test has a real deadline of its own: if the guard is removed, this
// test hangs rather than failing, and `go test` kills the package after its
// timeout. That is the correct outcome to have recorded, because it is
// exactly what the defect does to a controller.
func TestEnsureRefusesABlockingFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("named pipes are not created with mkfifo on Windows")
	}
	if _, err := exec.LookPath("mkfifo"); err != nil {
		t.Skipf("mkfifo is not available: %v", err)
	}

	dir := filepath.Join(t.TempDir(), "tls")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}
	certPath := filepath.Join(dir, tlscert.CertFileName)
	// #nosec G204 -- the only argument is a path this test just built inside
	// its own temporary directory.
	if out, err := exec.Command("mkfifo", certPath).CombinedOutput(); err != nil {
		t.Skipf("mkfifo %s: %v (%s)", certPath, err, out)
	}

	// A channel rather than a bare call, so a regression is a failed test
	// with a clear message for as long as the package timeout allows.
	done := make(chan error, 1)
	go func() {
		_, err := tlscert.Ensure(dir, tlscert.Options{})
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Ensure accepted a named pipe as a certificate")
		}
		for _, want := range []string{certPath, "named pipe"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the error %q does not mention %q", err, want)
			}
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Ensure blocked on a named pipe, which is what makes a controller hang forever with no log line")
	}
}

// TestEnsureReportsWhyItReplacedACertificate is item 5: the reason was
// computed precisely and thrown away by the caller, so the one fact an
// operator needs never reached a log.
func TestEnsureReportsWhyItReplacedACertificate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	ensureOK(t, dir, tlscert.Options{})

	replaced := ensureOK(t, dir, tlscert.Options{ExtraNames: []string{"controller.example.test"}})
	if !replaced.Generated {
		t.Fatal("the stored certificate was reused even though a required name was added")
	}
	if !strings.Contains(replaced.Reason, "controller.example.test") {
		t.Errorf("Reason = %q, and it does not name the missing name that caused the replacement", replaced.Reason)
	}
}

// TestEnsureRejectsANameACertificateCannotCarry is item 7's configuration
// error.
//
// A DNS name on a certificate is an IA5String, which is ASCII and nothing
// else, so a non-ASCII entry fails inside x509.CreateCertificate with a
// message that names neither the value nor the setting it came from.
func TestEnsureRejectsANameACertificateCannotCarry(t *testing.T) {
	tests := []struct {
		name  string
		entry string
		want  string
	}{
		{
			name:  "an international domain in its display form",
			entry: "contrôleur.example.test",
			want:  "punycode",
		},
		{
			name:  "a non-breaking space pasted from a document",
			entry: "controller .example.test",
			want:  "ASCII",
		},
		{
			name:  "a name with a space in it, which is a mistyped separator",
			entry: "controller one",
			want:  "letters, digits, hyphens and underscores",
		},
		{
			name:  "a label that is too long",
			entry: strings.Repeat("a", 64) + ".example.test",
			want:  "64 characters",
		},
		{
			name:  "two dots in a row",
			entry: "controller..example.test",
			want:  "empty part",
		},
		{
			name:  "a misplaced wildcard",
			entry: "example.*.test",
			want:  "wildcard",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "tls")
			_, err := tlscert.Ensure(dir, tlscert.Options{ExtraNames: []string{tc.entry}})
			if err == nil {
				t.Fatalf("Ensure accepted %q as a subject alternative name", tc.entry)
			}
			// The value has to be in the message, because an operator with a
			// list of six names needs to know which one is wrong. Compared
			// against its quoted form, since that is how the message renders
			// it and it is the only form in which an invisible character
			// (the non-breaking space below) is visible at all.
			if !strings.Contains(err.Error(), fmt.Sprintf("%q", tc.entry)) {
				t.Errorf("the error %q does not quote the value that is wrong", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the error %q does not explain the problem (%q)", err, tc.want)
			}
			// And nothing was written, because a configuration error must be
			// caught before a directory is filled in.
			if _, statErr := os.Stat(filepath.Join(dir, tlscert.CertFileName)); !errors.Is(statErr, fs.ErrNotExist) {
				t.Errorf("a certificate was written despite the configuration error (stat error %v)", statErr)
			}
		})
	}
}

// TestValidateNamesAcceptsWhatDeploymentsReallyUse is the control for the
// test above: a checker that rejected everything would pass it.
func TestValidateNamesAcceptsWhatDeploymentsReallyUse(t *testing.T) {
	accepted := []string{
		"",
		"   ",
		"localhost",
		"controller",
		"pleiades.example.test",
		"PLEIADES.EXAMPLE.TEST",
		"*.example.test",
		"host_1.internal",
		"a-b-c.example.test",
		"10.9.8.7",
		"fd00::1",
		"::1",
		"xn--controleur-fcb.example.test",
		// A container id, which is what a machine hostname is inside one.
		"020c7dfd004c",
	}
	if err := tlscert.ValidateNames(accepted); err != nil {
		t.Errorf("ValidateNames rejected a name a real deployment uses: %v", err)
	}
}

// TestLoadReportsAnExpiredCertificateWithoutReplacingIt is the operator
// configured path: TLS_CERT_FILE naming a certificate that has expired.
//
// Generating a replacement would be worse than useless. It would substitute
// a different identity for the one every client of this deployment was told
// to expect. Serving it silently is the actual defect: an expired
// certificate fails every handshake, and "the site stopped working and
// nothing was logged" is a far longer outage than "the site stopped working
// and the controller said the certificate expired on Tuesday".
func TestLoadReportsAnExpiredCertificateWithoutReplacingIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	plantOperatorPair(t, dir, "operator.example.test", time.Now().Add(-90*24*time.Hour), time.Now().Add(-time.Hour))

	cert, err := tlscert.Load(filepath.Join(dir, tlscert.CertFileName), filepath.Join(dir, tlscert.KeyFileName))
	if err != nil {
		t.Fatalf("Load refused an expired certificate instead of returning it: %v", err)
	}
	problem := tlscert.ValidityProblem(cert.Leaf)
	if !strings.Contains(problem, "expired") {
		t.Errorf("ValidityProblem = %q, want it to say the certificate expired", problem)
	}

	// And the control, so this is not passing because ValidityProblem
	// complains about everything.
	good, err := tlscert.Generate(filepath.Join(t.TempDir(), "tls"), tlscert.Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if problem := tlscert.ValidityProblem(good.Leaf); problem != "" {
		t.Errorf("ValidityProblem on a fresh certificate = %q, want none", problem)
	}
}

// TestAnchorsForDirTrustAReplacedCertificate is item 6.
//
// A controller serves the pair it loaded at start-up for as long as it runs.
// If anything replaces the pair on disk underneath it, a probe anchored on
// that file alone starts failing against a process that is serving
// perfectly, and the orchestrator restarts it. The anchor has to be what the
// deployment may legitimately be serving, which is the published certificate
// plus the ones it replaced.
func TestAnchorsForDirTrustAReplacedCertificate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	first := ensureOK(t, dir, tlscert.Options{})

	// A renewal, forced the way a real one happens: a required name the
	// stored certificate does not carry.
	second := ensureOK(t, dir, tlscert.Options{ExtraNames: []string{"controller.example.test"}})
	if !second.Generated || second.Leaf.Equal(first.Leaf) {
		t.Fatal("the second Ensure did not replace the certificate, so this test proves nothing")
	}

	anchors, err := tlscert.AnchorsForDir(dir)
	if err != nil {
		t.Fatalf("AnchorsForDir: %v", err)
	}
	for _, cert := range []*x509.Certificate{first.Leaf, second.Leaf} {
		if _, err := cert.Verify(x509.VerifyOptions{Roots: anchors.Roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
			t.Errorf("a probe would reject a controller serving %s: %v", cert.SerialNumber, err)
		}
	}

	// The control that says this is a real check and not an empty pool: a
	// certificate this deployment never provisioned must NOT verify.
	stranger, err := tlscert.Generate(filepath.Join(t.TempDir(), "other"), tlscert.Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, err := stranger.Leaf.Verify(x509.VerifyOptions{Roots: anchors.Roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err == nil {
		t.Error("the probe's trust pool accepts a certificate this deployment never provisioned")
	}
}

// TestAnchorsForFileTrustsExactlyThatFile is the operator-configured half of
// the probe's trust decision.
//
// There is no history to consider on this path: TLS_CERT_FILE is whatever
// the operator put there. What must hold is that the pool is closed around
// it, and that the name the probe asks for comes off the certificate rather
// than off the address it dials, since a real deployment's certificate names
// a public hostname and the probe always dials loopback.
func TestAnchorsForFileTrustsExactlyThatFile(t *testing.T) {
	configured, err := tlscert.Generate(filepath.Join(t.TempDir(), "operator"), tlscert.Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	anchors, err := tlscert.AnchorsForFile(configured.CertFile)
	if err != nil {
		t.Fatalf("AnchorsForFile: %v", err)
	}
	verify := x509.VerifyOptions{Roots: anchors.Roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if _, err := configured.Leaf.Verify(verify); err != nil {
		t.Errorf("the configured certificate does not verify against its own anchors: %v", err)
	}
	if anchors.ServerName != "localhost" {
		t.Errorf("ServerName = %q, want the certificate's first DNS name", anchors.ServerName)
	}

	stranger, err := tlscert.Generate(filepath.Join(t.TempDir(), "other"), tlscert.Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, err := stranger.Leaf.Verify(verify); err == nil {
		t.Error("the pool accepts a certificate the operator did not configure")
	}

	// A missing file has to stay tellable from a broken one, because the
	// container healthcheck turns that distinction into two different exit
	// codes.
	if _, err := tlscert.AnchorsForFile(filepath.Join(t.TempDir(), "absent.pem")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("AnchorsForFile on a missing file = %v, want an error matching fs.ErrNotExist", err)
	}
	notACertificate := filepath.Join(t.TempDir(), "notes.txt")
	writeFile(t, notACertificate, []byte("this file is not a certificate"))
	if _, err := tlscert.AnchorsForFile(notACertificate); err == nil {
		t.Error("AnchorsForFile accepted a file holding no certificate")
	}
}

// shortTempDir returns a fresh temporary directory, removed when the test
// ends, whose path is short enough to hold a Unix socket.
//
// t.TempDir is the obvious thing to reach for and is the wrong one here:
// it builds its directory name out of the test's own name, and macOS puts
// TMPDIR under /var/folders/<2>/<28>/T/, so a descriptively named test
// pushes the socket path past sockaddr_un's 104-byte sun_path limit and
// net.Listen fails with the famously unhelpful "bind: invalid argument".
// Linux allows 108 bytes and puts TMPDIR at /tmp, which is why this was
// invisible until CI grew a macos-latest leg. A two-character prefix keeps
// the whole path near 70 bytes on either platform.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "px")
	if err != nil {
		t.Fatalf("os.MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// TestRefusesAPathThatIsNotAnOrdinaryFile is the rest of the blocking-file
// guard, on the paths a named pipe test cannot reach.
//
// The message matters as much as the refusal. "cert.pem is a directory"
// tells an operator what to go and look at; the native error for the same
// mistake does not.
func TestRefusesAPathThatIsNotAnOrdinaryFile(t *testing.T) {
	dir := t.TempDir()
	occupied := filepath.Join(dir, "cert.pem")
	if err := os.Mkdir(occupied, 0o700); err != nil {
		t.Fatalf("creating %s: %v", occupied, err)
	}

	_, err := tlscert.Load(occupied, filepath.Join(dir, "key.pem"))
	if err == nil {
		t.Fatal("Load accepted a directory as a certificate")
	}
	for _, want := range []string{occupied, "a directory", "ordinary file"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error %q does not mention %q", err, want)
		}
	}

	if _, err := tlscert.AnchorsForFile(occupied); err == nil {
		t.Error("AnchorsForFile accepted a directory as a certificate")
	}

	// A socket, which is the other thing that turns up in a data directory
	// somebody has been experimenting in, and which open(2) treats
	// differently again.
	if runtime.GOOS == "windows" {
		return
	}
	// shortTempDir rather than t.TempDir: a Unix socket path has a hard
	// length limit t.TempDir's test-name-derived path can overrun, and
	// this case's own t.Skipf below would have turned that into a silent
	// gap rather than a failure.
	socketDir := shortTempDir(t)
	socketPath := filepath.Join(socketDir, "cert.pem")
	socket, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Skipf("this platform cannot create a unix socket: %v", err)
	}
	t.Cleanup(func() { _ = socket.Close() })

	_, err = tlscert.Load(socketPath, filepath.Join(socketDir, "key.pem"))
	if err == nil {
		t.Fatal("Load accepted a socket as a certificate")
	}
	if !strings.Contains(err.Error(), "a socket") {
		t.Errorf("the error %q does not say what it found", err)
	}
}

// TestLoadReportsAMissingKeySeparately covers the half of a configured pair
// that is easiest to get wrong in a deployment: the certificate is mounted
// and the key is not.
//
// It has to stay tellable from every other failure, because the container
// healthcheck turns "the file is not there yet" into a different exit code
// than "the configuration is wrong".
func TestLoadReportsAMissingKeySeparately(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	cert, err := tlscert.Generate(dir, tlscert.Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if err := os.Remove(cert.KeyFile); err != nil {
		t.Fatalf("removing the key: %v", err)
	}

	_, err = tlscert.Load(cert.CertFile, cert.KeyFile)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Load with a missing key = %v, want an error matching fs.ErrNotExist", err)
	}
}

// TestValidityProblemNamesAClockThatIsBehind covers the case an operator
// diagnoses in entirely the wrong place.
//
// A certificate that is not valid YET fails a handshake exactly like an
// expired one, and the usual cause is a machine whose clock is behind rather
// than anything about the certificate. Saying which of the two it is, and
// pointing at the clock, is the whole value of the message.
func TestValidityProblemNamesAClockThatIsBehind(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	plantOperatorPair(t, dir, "operator.example.test", time.Now().Add(48*time.Hour), time.Now().Add(90*24*time.Hour))

	cert, err := tlscert.Load(filepath.Join(dir, tlscert.CertFileName), filepath.Join(dir, tlscert.KeyFileName))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	problem := tlscert.ValidityProblem(cert.Leaf)
	for _, want := range []string{"not valid until", "clock"} {
		if !strings.Contains(problem, want) {
			t.Errorf("ValidityProblem = %q, which does not mention %q", problem, want)
		}
	}
}

// TestAnchorsForDirReportsAColdStart pins the one error a caller has to be
// able to tell apart: the certificate is not written yet, which is the
// startup window rather than a fault.
func TestAnchorsForDirReportsAColdStart(t *testing.T) {
	_, err := tlscert.AnchorsForDir(filepath.Join(t.TempDir(), "tls"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("AnchorsForDir on an empty directory = %v, want an error matching fs.ErrNotExist", err)
	}
}

// plantOperatorPair writes a certificate and key that this package did not
// generate, exactly as an operator copying their own files in would leave
// them: no provenance record beside them.
func plantOperatorPair(t *testing.T, dir, name string, notBefore, notAfter time.Time) *x509.Certificate {
	t.Helper()

	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		// Deliberately none of the loopback names, which is what a real
		// certificate for a real hostname looks like and is also what makes
		// this package's reuse rules want to replace it.
		DNSNames:    []string{name},
		IPAddresses: []net.IP{},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("signing the planted certificate: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshalling the planted key: %v", err)
	}

	writeFile(t, filepath.Join(dir, tlscert.CertFileName), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	writeFile(t, filepath.Join(dir, tlscert.KeyFileName), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))

	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parsing the planted certificate: %v", err)
	}
	return parsed
}

// readPEMBlock reads one certificate out of a PEM file, failing the test
// rather than returning an error.
func readPEMBlock(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		t.Fatalf("%s holds no PEM block", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	return cert
}
