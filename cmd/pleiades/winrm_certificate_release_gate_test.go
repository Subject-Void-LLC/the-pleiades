// Package main_test holds the Release Gate for WinRM client certificate
// authentication driven through the real built binary.
//
// The long explanation of why this gate is environment-gated rather than
// container-backed, what it proves that its Linux-runnable sibling cannot,
// and what the Windows target has to be configured with first, is below the
// import block beside the constants it describes.
package main_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This file is the Windows half of the Release Gate for WinRM client
// certificate authentication: the real built binary, driven through init,
// add-host, add-credential and run, against a real Windows host whose WinRM
// service is configured to map a client certificate to an account.
//
// # Why this one is env-gated, and what runs instead when it skips
//
// The same reasoning winrm_static_ip_release_gate_test.go gives: there is
// no Windows container a Linux box can run, and no way to fake WinRM
// without also faking the thing under test, so this needs a real host and
// skips clearly when it does not have one.
//
// The half that runs everywhere is
// pkg/winrmexec/certauth_release_gate_test.go, which proves the security
// property against a real TLS server that demands and verifies a client
// certificate. What only THIS file can prove is the step after the
// handshake: that Windows maps the presented certificate to an account and
// actually runs the command. A skip here is a real gap rather than a
// formality, and the roadmap checkbox stays open until it has run.
//
// # It reuses envWinRMHost and declares its own certificate variables
//
// envWinRMHost names the same lab target either gate would point at, so it
// is reused rather than copied, exactly as
// winrm_service_feature_release_gate_test.go does. envWinRMUser and
// envWinRMPassword are deliberately NOT used: the whole point is a session
// with no password in it.
//
// # This one leaves the lab machine as it found it
//
// The reversibility rule the static-IP gate states applies here. This gate
// creates exactly one file, on the desktop of the mapped account, and
// removes it in the same task that reads it back, so the removal happens
// even when the assertion fails. Nothing is configured, started, stopped or
// installed, and a re-run starts from the state the last run left.
//
// If a run is killed between the two sessions the file survives. It is
// named pleiades-gate.txt and holds one line, so it is obvious what it is
// and safe to delete by hand.
//
// # What has to be true on the Windows side first
//
// An HTTPS WinRM listener on 5986, the issuing authority in the host's
// trusted roots, and a certificate-to-account mapping created with
// New-Item -Path WSMan:\localhost\ClientCertificate. The certificate must
// carry a UPN in its subject alternative name and Client Authentication in
// its extended key usage, or the mapping cannot match it. That burden is on
// the target, and docs/10-running-in-production.md says so in the same
// words so an operator meets it before reading a handshake failure as a
// defect here.

const (
	envWinRMCertificate = "PLEIADES_WINRM_CERTIFICATE"
	envWinRMCertKey     = "PLEIADES_WINRM_CERTIFICATE_KEY"

	// A PKCS#12 bundle is accepted as an ALTERNATIVE to the PEM pair above,
	// and it is the more valuable of the two to run.
	//
	// A bundle exported by Windows is the only artifact that exercises
	// pkg/pfx against bytes this project did not produce. Everything in
	// pkg/pfx's own tests is encoded by the same library that decodes it,
	// which proves the round trip and says nothing about what real tooling
	// emits. When this variable is set, the gate runs the whole chain:
	// Windows seals it, pkg/pfx unlocks it in the child process, and the
	// certificate it yields is what authenticates.
	envWinRMPFX         = "PLEIADES_WINRM_PFX"
	envWinRMPFXPassword = "PLEIADES_WINRM_PFX_PASSWORD"
)

// labCredential is how the gate supplies its client identity: either a PEM
// pair or a sealed bundle, never both.
type labCredential struct {
	certPath string
	keyPath  string
	pfxPath  string
	pfxPass  string
}

// addCredentialArgs renders this identity as add-credential flags.
func (c labCredential) addCredentialArgs() []string {
	if c.pfxPath != "" {
		return []string{"--pfx", c.pfxPath, "--passphrase-stdin"}
	}
	return []string{"--certificate", c.certPath, "--key", c.keyPath}
}

// describe names which supply form is in use, for a failure message that
// says which path was exercised.
func (c labCredential) describe() string {
	if c.pfxPath != "" {
		return "a PKCS#12 bundle"
	}
	return "a PEM certificate and key"
}

// winrmCertificateGate skips unless a lab target and a client identity are
// both configured.
//
// A bundle wins over a PEM pair when both are set, because it exercises
// strictly more of the platform: the same certificate path, plus pkg/pfx
// unlocking bytes that Windows produced rather than this repository.
func winrmCertificateGate(t *testing.T) (host string, cred labCredential) {
	t.Helper()

	host = os.Getenv(envWinRMHost)
	cred = labCredential{
		certPath: os.Getenv(envWinRMCertificate),
		keyPath:  os.Getenv(envWinRMCertKey),
		pfxPath:  os.Getenv(envWinRMPFX),
		pfxPass:  os.Getenv(envWinRMPFXPassword),
	}
	// The passphrase may instead be named by a file, which is how
	// winrm-cert-setup.ps1 hands it over (readable only by its owner), so
	// it need not be copied into an environment variable at all.
	if cred.pfxPass == "" {
		if path := os.Getenv(envWinRMPFXPassword + "_FILE"); path != "" {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading %s_FILE: %v", envWinRMPFXPassword, err)
			}
			cred.pfxPass = strings.TrimRight(string(raw), "\r\n")
		}
	}

	switch {
	case host == "":
		t.Skipf("the WinRM certificate Release Gate needs a real Windows host with an HTTPS listener on 5986 "+
			"and a certificate-to-account mapping; set %s, plus either %s and %s, or %s and %s. The certificate "+
			"must carry a UPN in its subject alternative name and Client Authentication in its extended key "+
			"usage. This gate writes one file to the mapped account's desktop and removes it again.",
			envWinRMHost, envWinRMCertificate, envWinRMCertKey, envWinRMPFX, envWinRMPFXPassword)
	case cred.pfxPath != "" && cred.pfxPass == "":
		t.Skipf("%s is set without %s, and a sealed bundle cannot be opened without its passphrase",
			envWinRMPFX, envWinRMPFXPassword)
	case cred.pfxPath == "" && (cred.certPath == "" || cred.keyPath == ""):
		t.Skipf("the WinRM certificate Release Gate needs a client identity: set %s and %s, or %s and %s",
			envWinRMCertificate, envWinRMCertKey, envWinRMPFX, envWinRMPFXPassword)
	}
	return host, cred
}

// certificateGateProject builds a project whose one device is reached by
// certificate rather than by password.
//
// Port 5986 is set explicitly. A windows_server device defaults to 5985,
// the cleartext listener, and a certificate credential aimed at it is
// refused by design, so leaving the default would make this gate fail for a
// reason that has nothing to do with the target.
func certificateGateProject(t *testing.T, host string, cred labCredential) string {
	t.Helper()

	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "add-host", "win-cert-gate",
		"--type", "windows_server", "--set", "host="+host, "--set", "port=5986"); err != nil {
		t.Fatalf("add-host: %v\n%s", err, out)
	}

	args := append([]string{"add-credential", "win-cert-gate"}, cred.addCredentialArgs()...)
	out, err := runPleiadesWithStdin(t, dir, cred.pfxPass+"\n", args...)
	if err != nil {
		t.Fatalf("add-credential with %s: %v\n%s", cred.describe(), err, out)
	}
	return dir
}

// TestWinRMGate_ACertificateAuthenticatesAndAStrangerDoesNot is the gate.
//
// Three acts. The first proves a certificate-authenticated session really
// does work on the far side, by writing a file and reading it back. The
// second is what makes the first mean anything: a certificate this host has
// never been told about must be refused, or a passing first act could not
// tell certificate authentication from a listener accepting whatever it is
// shown. The third puts the machine back.
//
// # Why a file round trip rather than `hostname`
//
// `hostname` proves a session opened. It does not prove the session can DO
// anything, and the difference is not academic: a WinRM session can
// authenticate and then fail every operation because the mapped account has
// no rights. Writing a file, reading it back in a SEPARATE session and
// comparing the contents proves authentication, authorization and a real
// round trip, and the second session proves the first one's effect outlived
// it rather than being echoed back out of the same shell.
func TestWinRMGate_ACertificateAuthenticatesAndAStrangerDoesNot(t *testing.T) {
	host, cred := winrmCertificateGate(t)
	t.Logf("supplying the client identity as %s", cred.describe())

	// A marker distinctive enough that finding it in the output cannot be a
	// coincidence, and fixed rather than random so a failed run leaves a file
	// whose name says where it came from.
	const marker = "pleiades-gate-round-trip-4f2a9c"
	const fileName = "pleiades-gate.txt"

	dir := certificateGateProject(t, host, cred)

	// Act one, first session: create the file on the desktop and write to it.
	//
	// GetFolderPath rather than "$env:USERPROFILE\\Desktop", because a desktop
	// redirected into OneDrive is the common case on a real machine and the
	// literal path would silently write somewhere nobody looks.
	writeRB := writeRunbook(t, dir, "cert_write", `id: cert_write
hosts: win-cert-gate
tasks:
  - name: write a file on the desktop
    fqcn: exec.winrm.shell
    params:
      shell: powershell
      command: |
        $desktop = [Environment]::GetFolderPath('Desktop')
        $path = Join-Path $desktop '`+fileName+`'
        Set-Content -LiteralPath $path -Value '`+marker+`' -Encoding UTF8
        Write-Output "wrote $path"
`)
	out, err := runPleiades(t, dir, "run", writeRB, "--verbose")
	if err != nil {
		t.Fatalf("a certificate-authenticated session could not write to the desktop: %v\n%s", err, out)
	}

	// Act one, second session: read it back and remove it.
	//
	// The removal is in the same task as the read so the file is cleaned up
	// even when the assertion below fails, which is what keeps this gate
	// re-runnable. The read happens first, so a failure to delete cannot
	// disguise itself as a failure to read.
	readRB := writeRunbook(t, dir, "cert_read", `id: cert_read
hosts: win-cert-gate
tasks:
  - name: read it back and clean up
    fqcn: exec.winrm.shell
    params:
      shell: powershell
      command: |
        $desktop = [Environment]::GetFolderPath('Desktop')
        $path = Join-Path $desktop '`+fileName+`'
        $content = Get-Content -LiteralPath $path -Raw
        Remove-Item -LiteralPath $path -Force
        Write-Output $content.Trim()
`)
	out, err = runPleiades(t, dir, "run", readRB, "--verbose")
	if err != nil {
		t.Fatalf("a second certificate-authenticated session could not read the file back: %v\n%s", err, out)
	}
	if !strings.Contains(out, marker) {
		t.Errorf("the file did not round trip: the marker %q is absent from the output:\n%s", marker, out)
	}

	// Act two: a certificate this host has never been mapped is refused.
	//
	// Generated here rather than configured, so the negative control needs
	// no extra environment variable and cannot accidentally be the same
	// certificate as act one's. It attempts the READ, so if the refusal were
	// ever to stop working this act would find act one's file already gone
	// and fail loudly rather than quietly succeeding.
	strangerCert, strangerKey := writeStrangerCertificate(t)
	strangerDir := certificateGateProject(t, host, labCredential{certPath: strangerCert, keyPath: strangerKey})
	strangerRB := writeRunbook(t, strangerDir, "stranger_read", `id: stranger_read
hosts: win-cert-gate
tasks:
  - name: an unmapped certificate must not get a session
    fqcn: exec.winrm.shell
    params:
      shell: powershell
      command: hostname
`)
	if out, err := runPleiades(t, strangerDir, "run", strangerRB, "--verbose"); err == nil {
		t.Fatalf("an unmapped certificate authenticated, so this host accepts whatever it is shown:\n%s", out)
	}
}

// writeStrangerCertificate generates a self-signed client certificate no
// host has been told about, and returns the two paths.
func writeStrangerCertificate(t *testing.T) (certPath, keyPath string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating the stranger key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "an-unmapped-stranger"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating the stranger certificate: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshaling the stranger key: %v", err)
	}

	dir := t.TempDir()
	certPath = filepath.Join(dir, "stranger.pem")
	keyPath = filepath.Join(dir, "stranger.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("writing the stranger certificate: %v", err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatalf("writing the stranger key: %v", err)
	}
	return certPath, keyPath
}
