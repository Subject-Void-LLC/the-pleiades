// Getting a directory back, which is the property the ownership rule used to
// trade away without saying so.
//
// Every case in this file was a permanent block: a controller starting into
// the directory got an error, every other controller got the same error, and
// the only escape was a human deleting a file the message told them not to
// touch. Several of the states are ones this package itself produces, so the
// bricking was reachable without anybody doing anything wrong at all.
//
// The shape of every test here is the same three steps, because a fix that is
// only argued for is not a fix: put the directory into the state that used to
// block, run the real entry point, and then complete a real TLS handshake
// against a real listener holding the material that came back. The last step
// is the one that matters. "Ensure returned no error" would have passed for a
// certificate nothing could serve.
package tlscert_test

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/tlscert"
)

// TestEnsureRecoversFromAnUnservableBundle is the first and worst of them.
//
// serving.pem is the file this package publishes and the file it reads back,
// and any damage to it used to be read as "somebody else's material": zero
// length, bytes that are not PEM, a file cut off part way through the
// certificate. None of those is a secret, none of them can be served, and two
// of them are exactly what a process killed mid-write or a volume that ran
// out of space leaves behind. Every controller in the directory refused to
// start, forever, over a file the directory's own provenance records proved
// this package had written.
func TestEnsureRecoversFromAnUnservableBundle(t *testing.T) {
	tests := []struct {
		name   string
		damage func(t *testing.T, path string, published []byte)
	}{
		{
			name: "zero length",
			damage: func(t *testing.T, path string, _ []byte) {
				writeFile(t, path, nil)
			},
		},
		{
			name: "not PEM at all",
			damage: func(t *testing.T, path string, _ []byte) {
				writeFile(t, path, []byte("\x00\x01garbage that no parser will ever like\n"))
			},
		},
		{
			name: "truncated part way through the certificate",
			damage: func(t *testing.T, path string, published []byte) {
				// Cut inside the certificate block, which is the shape a
				// write that ran out of disk leaves: the preamble and the
				// BEGIN line are there and nothing after them parses.
				writeFile(t, path, published[:len(published)/3])
			},
		},
		{
			name: "a certificate with the key cut off",
			damage: func(t *testing.T, path string, _ []byte) {
				// A whole, valid certificate and no key: servable by nobody,
				// and a secret to nobody either.
				fresh, err := tlscert.Generate(filepath.Join(t.TempDir(), "elsewhere"), tlscert.Options{})
				if err != nil {
					t.Fatalf("minting a certificate to plant: %v", err)
				}
				writeFile(t, path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: fresh.Leaf.Raw}))
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "tls")
			first := ensureOK(t, dir, tlscert.Options{})

			bundlePath := filepath.Join(dir, tlscert.BundleFileName)
			published, err := os.ReadFile(bundlePath)
			if err != nil {
				t.Fatalf("reading the published bundle: %v", err)
			}
			tc.damage(t, bundlePath, published)

			recovered, err := tlscert.Ensure(dir, tlscert.Options{})
			if err != nil {
				t.Fatalf("the directory is permanently blocked: %v", err)
			}
			if !recovered.Generated {
				t.Error("Ensure served the damaged file rather than replacing it")
			}
			if recovered.Leaf.Equal(first.Leaf) {
				t.Error("Ensure reported the certificate that was destroyed")
			}
			assertServes(t, recovered)

			// And the directory is genuinely working again rather than
			// working once: the next controller reuses what this one left.
			again, err := tlscert.Ensure(dir, tlscert.Options{})
			if err != nil {
				t.Fatalf("a second controller could not start in the recovered directory: %v", err)
			}
			if again.Generated || !again.Leaf.Equal(recovered.Leaf) {
				t.Error("the recovered directory did not converge on one certificate")
			}
		})
	}
}

// TestEnsureStartsInADirectoryHoldingOnlyACertificate is the second block,
// and it is the one that shows the old rule protecting nothing at all.
//
// A certificate is public by construction. One sitting in a directory with no
// private key beside it cannot be served by anybody, and replacing it could
// not destroy a secret, because there is no secret. Refusing over it cost
// every controller in the directory its start-up and bought nothing.
func TestEnsureStartsInADirectoryHoldingOnlyACertificate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	// A real, parseable certificate that this package certainly did not
	// write: an operator's, for their own hostname.
	planted := plantOperatorPair(t, dir, "operator.example.test", time.Now().Add(-time.Hour), time.Now().Add(90*24*time.Hour))
	if err := os.Remove(filepath.Join(dir, tlscert.KeyFileName)); err != nil {
		t.Fatalf("removing the key: %v", err)
	}

	got, err := tlscert.Ensure(dir, tlscert.Options{})
	if err != nil {
		t.Fatalf("a certificate with no key beside it blocked the start: %v", err)
	}
	if !got.Generated || !got.SelfProvisioned {
		t.Error("Ensure did not provision a certificate of its own")
	}
	assertServes(t, got)

	// The operator's certificate is still exactly where they put it. Not
	// refusing to start and not overwriting a file are different promises,
	// and this package keeps the second one.
	onDisk := readPEMBlock(t, filepath.Join(dir, tlscert.CertFileName))
	if !onDisk.Equal(planted) {
		t.Error("the certificate the operator left in the directory was rewritten")
	}
}

// TestEnsureNamesTheFileItCannotRead is the false-diagnosis case.
//
// Every file this package writes is 0600, so the state that produces this in
// the field is two controllers running as different users over one directory.
// The old rule turned any read failure that was not "no such file" into
// "material this controller did not write", which is a guess dressed as a
// fact: it sent an operator looking for a secret nobody had put there, and
// named TLS_CERT_FILE, which would not have fixed it.
//
// Both halves are asserted: the refusal stays, because bytes this process
// cannot read are unknown rather than proven harmless, and the message now
// says what is really wrong and what to change.
func TestEnsureNamesTheFileItCannotRead(t *testing.T) {
	if os.Geteuid() == 0 {
		// Root reads a 0000 file, so the premise cannot be established here.
		// Skipped rather than quietly passing for the wrong reason; the
		// sibling test below reaches the same code path as any user.
		t.Skip("running as root, which can read a file with no permission bits")
	}
	if runtime.GOOS == "windows" {
		t.Skip("file permissions do not work this way on Windows")
	}

	dir := filepath.Join(t.TempDir(), "tls")
	// Two hours of life published against a one-minute window, then read back
	// against the default thirty-day one, so the second call certainly wants
	// to replace what the first one wrote.
	if _, err := tlscert.Ensure(dir, tlscert.Options{TTL: 2 * time.Hour, RenewBefore: time.Minute}); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	bundlePath := filepath.Join(dir, tlscert.BundleFileName)
	if err := os.Chmod(bundlePath, 0o000); err != nil {
		t.Fatalf("making %s unreadable: %v", bundlePath, err)
	}
	t.Cleanup(func() { _ = os.Chmod(bundlePath, 0o600) })

	_, err := tlscert.Ensure(dir, tlscert.Options{})
	if err == nil {
		t.Fatal("Ensure wrote over a file whose contents it could not see")
	}
	assertRefusalIsActionable(t, err, bundlePath, "could not be read", "uid")
	if strings.Contains(err.Error(), "did not write") || strings.Contains(err.Error(), "not written by this controller") {
		t.Errorf("a file this controller could not read is reported as somebody else's secret: %q", err)
	}

	// And doing what the message says fixes it. That is the difference between
	// a refusal and a brick: this one names an action a human can take, and
	// taking it gets the directory back.
	if err := os.Chmod(bundlePath, 0o600); err != nil {
		t.Fatalf("restoring the permissions the message asked for: %v", err)
	}
	recovered, err := tlscert.Ensure(dir, tlscert.Options{})
	if err != nil {
		t.Fatalf("the directory stayed blocked after the permissions were fixed: %v", err)
	}
	assertServes(t, recovered)
}

// TestEnsureNamesAFileItCannotReadForAnyReason is the same refusal reached by
// a shape root cannot ignore.
//
// A directory where a file should be is what a botched restore or a volume
// mounted one level too high leaves, and readPEMFile refuses it for the same
// reason it refuses a named pipe: stat before open, so the process names the
// file instead of blocking on it forever.
func TestEnsureNamesAFileItCannotReadForAnyReason(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("creating %s: %v", dir, err)
	}
	bundlePath := filepath.Join(dir, tlscert.BundleFileName)
	if err := os.Mkdir(bundlePath, 0o700); err != nil {
		t.Fatalf("putting a directory where the bundle goes: %v", err)
	}

	_, err := tlscert.Ensure(dir, tlscert.Options{})
	if err == nil {
		t.Fatal("Ensure wrote over a path whose contents it could not see")
	}
	assertRefusalIsActionable(t, err, bundlePath, "could not be read", "a directory")
}

// TestAnchorsSurviveALegacyRecordThatCannotBeRead is the healthcheck case,
// and it is the one where the cost was furthest from the cause.
//
// provisioned.pem is the single-file record an earlier build wrote. It is
// read and never written, and nothing in a modern directory depends on it.
// An unreadable one made the trust anchor builder return an error instead of
// the anchors it had already gathered, so a controller that provisioned
// perfectly, served perfectly and had a complete set of per-certificate
// records failed its own container healthcheck on every probe, forever, and
// an orchestrator answers that by killing it.
func TestAnchorsSurviveALegacyRecordThatCannotBeRead(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	serving := ensureOK(t, dir, tlscert.Options{})

	// A directory where the legacy record goes: unreadable as PEM by any
	// user, so this test proves the same thing as root and as anybody else.
	if err := os.Mkdir(filepath.Join(dir, tlscert.ProvisionedFileName), 0o700); err != nil {
		t.Fatalf("putting a directory where the legacy record goes: %v", err)
	}

	anchors, err := tlscert.AnchorsForDir(dir)
	if err != nil {
		t.Fatalf("the probe cannot build its trust anchors: %v", err)
	}
	if _, err := serving.Leaf.Verify(x509.VerifyOptions{
		Roots:     anchors.Roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		t.Errorf("a probe would reject the certificate this controller is serving: %v", err)
	}
}

// TestAReplicaKeepsItsAnchorWhileASiblingRenews is the running-replica case.
//
// A controller serves the material it loaded at start-up, from memory, for as
// long as it runs, while the directory underneath it can move on. The anchor
// set has to admit what such a replica may legitimately still be presenting,
// and it did not: a replica that REUSED what it found wrote no record of its
// own, so its anchor was somebody else's file, and one sibling publishing one
// new certificate into a directory whose records had been lost was enough to
// make that replica fail its own healthcheck while serving perfectly.
func TestAReplicaKeepsItsAnchorWhileASiblingRenews(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	if _, err := tlscert.Ensure(dir, tlscert.Options{}); err != nil {
		t.Fatalf("the first controller: %v", err)
	}
	// The records are gone: an operator tidying the directory, a restore that
	// missed a subdirectory, a volume copied file by file. The preamble in
	// each record warns against exactly this, which is the evidence that it
	// happens.
	if err := os.RemoveAll(filepath.Join(dir, tlscert.ProvisionedDirName)); err != nil {
		t.Fatalf("removing the records: %v", err)
	}

	// The replica under test: it starts, reuses what is published, and keeps
	// serving that for the rest of the test.
	running, err := tlscert.Ensure(dir, tlscert.Options{})
	if err != nil {
		t.Fatalf("the running replica: %v", err)
	}
	if running.Generated {
		t.Fatal("the replica minted a certificate instead of reusing the published one")
	}
	// The mechanism, asserted directly rather than only through its outcome:
	// this replica put its own answer into the set, in a file named after its
	// own fingerprint that no other writer will ever open. Reusing used to
	// write nothing at all, which left the replica's anchor depending on
	// somebody else's file staying exactly where it was.
	if !hasRecordFor(t, dir, running.Leaf) {
		t.Error("a replica that reused the published certificate recorded nothing, so its anchor is another writer's file")
	}

	// A sibling renews, twice, which is what moves the directory past it.
	for i, name := range []string{"first.example.test", "second.example.test"} {
		sibling, err := tlscert.Ensure(dir, tlscert.Options{ExtraNames: []string{name}})
		if err != nil {
			t.Fatalf("sibling %d: %v", i, err)
		}
		if sibling.Leaf.Equal(running.Leaf) {
			t.Fatalf("sibling %d did not actually renew", i)
		}
	}

	anchors, err := tlscert.AnchorsForDir(dir)
	if err != nil {
		t.Fatalf("AnchorsForDir: %v", err)
	}
	if _, err := running.Leaf.Verify(x509.VerifyOptions{
		Roots:     anchors.Roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		t.Errorf("a probe would reject a replica that is serving perfectly, which an orchestrator answers by killing it: %v", err)
	}

	// The same thing one step further: the published files themselves go away
	// underneath the running replica. It is still holding its material in
	// memory and still answering every request, so its records alone have to
	// be enough to verify it.
	for _, name := range []string{tlscert.BundleFileName, tlscert.CertFileName} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			t.Fatalf("removing %s: %v", name, err)
		}
	}
	anchors, err = tlscert.AnchorsForDir(dir)
	if err != nil {
		t.Fatalf("AnchorsForDir with only the records left: %v", err)
	}
	if _, err := running.Leaf.Verify(x509.VerifyOptions{
		Roots:     anchors.Roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}); err != nil {
		t.Errorf("a probe would reject a replica whose published files were removed while it ran: %v", err)
	}
}

// TestEnsureNamesACertificateFileWithNoKeyInIt is the leak shape.
//
// CertFile used to be the serving bundle on every reuse, and the serving
// bundle holds the private key. The controller printed it as cert_file, and
// an operator following that path into a `curl --cacert`, a ConfigMap or a
// copy to a colleague would have handed this server's private key to
// something that only ever wanted the certificate.
func TestEnsureNamesACertificateFileWithNoKeyInIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	generated := ensureOK(t, dir, tlscert.Options{})
	reused := ensureOK(t, dir, tlscert.Options{})
	if reused.Generated {
		t.Fatal("the second call did not reuse, so the reuse path is not being tested")
	}

	for _, cert := range []tlscert.ServingCert{generated, reused} {
		anchor, ok := cert.AnchorFile()
		if !ok {
			t.Fatal("no key-free copy of the certificate was reported at all")
		}
		if anchor != filepath.Join(dir, tlscert.CertFileName) {
			t.Errorf("AnchorFile() = %q, want the certificate-only copy", anchor)
		}
		raw, err := os.ReadFile(anchor)
		if err != nil {
			t.Fatalf("reading the file offered as a trust anchor: %v", err)
		}
		// The assertion that matters: whatever is offered under that name has
		// no private key in it, checked by parsing rather than by trusting
		// the file name.
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
		if !containsCertificate(parseCertificatesForTest(t, raw), cert.Leaf) {
			t.Errorf("%s does not hold the certificate being served", anchor)
		}
	}
}

// TestEnsureReportsAFirstStartAsASentence covers the most common thing this
// package ever does.
//
// The reason a certificate was written is printed to operators as
// replaced_because. On a first start into an empty directory it used to be
// "stat /data/tls/cert.pem: no such file or directory", which is a syscall
// rather than an explanation and made an ordinary cold start read like a
// fault.
func TestEnsureReportsAFirstStartAsASentence(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	first := ensureOK(t, dir, tlscert.Options{})

	if !strings.Contains(first.Reason, "no certificate is stored in") {
		t.Errorf("Reason = %q, which is not the sentence a first start should print", first.Reason)
	}
	for _, unwanted := range []string{"stat ", "no such file or directory"} {
		if strings.Contains(first.Reason, unwanted) {
			t.Errorf("Reason = %q, which is a raw syscall error rather than an explanation", first.Reason)
		}
	}
	if !strings.Contains(first.Reason, dir) {
		t.Errorf("Reason = %q, which does not name the directory it is about", first.Reason)
	}
}

// TestEnsureTellsAKeyFromBytesThatLookLikeOne is the line the whole rule
// stands on, checked from both sides.
//
// Everything this package refuses to touch, it refuses because a private key
// might be destroyed. So the question "is this a private key?" has to be
// answered by PARSING, not by reading the label above the base64: a block
// headed PRIVATE KEY holding a truncated DER is bytes, and refusing to start
// over bytes is the defect. An encrypted key goes the other way and the
// exception is load bearing, because Go cannot decrypt one, so it will never
// parse, and treating it as "not a key" would let this package write over the
// only copy of a real secret.
func TestEnsureTellsAKeyFromBytesThatLookLikeOne(t *testing.T) {
	tests := []struct {
		name    string
		planted []byte
		blocks  bool
	}{
		{
			name:    "a real key in the encoding openssl writes by default",
			planted: operatorKeyPEM(t),
			blocks:  true,
		},
		{
			name: "an encrypted key, which nothing here can parse and must never be replaced",
			planted: pem.EncodeToMemory(&pem.Block{
				Type:    "RSA PRIVATE KEY",
				Headers: map[string]string{"Proc-Type": "4,ENCRYPTED", "DEK-Info": "AES-256-CBC,0123456789ABCDEF"},
				Bytes:   []byte("ciphertext that no parser will ever read"),
			}),
			blocks: true,
		},
		{
			name: "a PKCS#8 encrypted key, recognised by its block type alone",
			planted: pem.EncodeToMemory(&pem.Block{
				Type:  "ENCRYPTED PRIVATE KEY",
				Bytes: []byte("ciphertext that no parser will ever read"),
			}),
			blocks: true,
		},
		{
			name: "a block labelled as a key holding bytes that are not one",
			planted: pem.EncodeToMemory(&pem.Block{
				Type:  "EC PRIVATE KEY",
				Bytes: []byte("truncated, and therefore nobody's secret"),
			}),
			blocks: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "tls")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatalf("creating %s: %v", dir, err)
			}
			keyPath := filepath.Join(dir, tlscert.KeyFileName)
			writeFile(t, keyPath, tc.planted)

			got, err := tlscert.Ensure(dir, tlscert.Options{})
			if tc.blocks {
				if err == nil {
					t.Fatal("Ensure provisioned into a directory holding a private key it cannot account for")
				}
				assertRefusalIsActionable(t, err, keyPath, "private key")
			} else if err != nil {
				t.Fatalf("Ensure refused to start over bytes that are nobody's secret: %v", err)
			} else {
				assertServes(t, got)
			}

			// Either way the planted bytes are still exactly where they were.
			// This package writes over a key.pem only when it can prove it
			// wrote it, and it never proved anything about these.
			after, readErr := os.ReadFile(keyPath)
			if readErr != nil {
				t.Fatalf("the planted file is gone: %v", readErr)
			}
			if string(after) != string(tc.planted) {
				t.Error("the planted file was modified")
			}
		})
	}
}

// assertServes completes a real TLS handshake against a real listener holding
// the material that came back.
//
// It is the only assertion in this file that proves a recovery is a recovery.
// A ServingCert with no error in it is not evidence: the question is whether
// a client can reach a server holding it.
func assertServes(t *testing.T, cert tlscert.ServingCert) {
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
		// The material in hand, not the paths, because what a caller serves
		// is the pair this package handed it.
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert.Pair}},
	}
	go func() { _ = srv.ServeTLS(listener, "", "") }()
	t.Cleanup(func() { _ = srv.Close() })

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: cert.TLSClientConfig()}}
	resp, err := client.Get("https://" + listener.Addr().String() + "/")
	if err != nil {
		t.Fatalf("a client could not reach a server holding the recovered material: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("the server answered %s", resp.Status)
	}
}

// assertRefusalIsActionable checks that a refusal names the file it is about
// and tells a human what to do next.
//
// Every refusal in this package has to pass this, because a refusal that
// names neither is the difference between a five-minute fix and a directory
// nobody can work out how to unblock.
func assertRefusalIsActionable(t *testing.T, err error, path string, wants ...string) {
	t.Helper()

	for _, want := range append([]string{path, "move it aside"}, wants...) {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not mention %q", err, want)
		}
	}
}

// hasRecordFor reports whether dir holds a provenance record for leaf.
//
// It rebuilds the file name from the certificate rather than reading the
// records, because the name IS the mechanism: one file per fingerprint is
// what makes a record impossible for a concurrent writer to lose, and a test
// that only asked "is this certificate trusted" would pass on a stale copy of
// cert.pem sitting beside them.
func hasRecordFor(t *testing.T, dir string, leaf *x509.Certificate) bool {
	t.Helper()

	fingerprint := sha256.Sum256(leaf.Raw)
	path := filepath.Join(dir, tlscert.ProvisionedDirName, hex.EncodeToString(fingerprint[:])+".pem")
	if _, err := os.Stat(path); err == nil {
		return true
	}
	return false
}

// parseCertificatesForTest decodes every certificate in a PEM file, from the
// test's own side of the package boundary.
func parseCertificatesForTest(t *testing.T, raw []byte) []*x509.Certificate {
	t.Helper()

	var certs []*x509.Certificate
	for {
		var block *pem.Block
		block, raw = pem.Decode(raw)
		if block == nil {
			return certs
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatalf("parsing a certificate out of the file: %v", err)
		}
		certs = append(certs, cert)
	}
}
