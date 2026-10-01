//go:build linux

// The per-run half of Phase 75's industry comparison: one WinRM command
// through the real pleiades binary beside the same command through a real
// ansible-playbook, against the same host, as the same certificate
// account. pkg/winrmexec's TestModesComparedToPywinrm compares the two
// clients call for call; this compares the two tools as an operator runs
// them, process start and all.
package main_test

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// compareRuns is how many times each tool runs the command.
const compareRuns = 10

// TestWinRMGate_RunComparedToAnsible times `pleiades adhoc` running
// whoami.exe with no shell, then ansible-playbook running the same program
// through ansible.windows.win_command, ten times each, and logs both.
//
// What each number includes is the point, so it is stated here: the
// pleiades figure is a binary start, reading the project's inventory,
// unlocking the stored credential and one WinRM command; the Ansible figure
// is a Python start, loading the playbook and its collection, forking a
// worker and one WinRM command through pywinrm. Neither is the other's
// best case, and both are what running one command costs from a shell.
//
// It skips unless the certificate gate is configured with PLEIADES_WINRM_CA
// set (both tools verify the host), and when ansible-playbook, the
// ansible.windows collection or pywinrm is missing, saying which.
func TestWinRMGate_RunComparedToAnsible(t *testing.T) {
	host, cred := winrmCertificateGate(t)
	caPath := os.Getenv(envWinRMCA)
	if caPath == "" {
		t.Skipf("the comparison verifies the host on both sides: set %s", envWinRMCA)
	}
	ansible, err := exec.LookPath("ansible-playbook")
	if err != nil {
		t.Skip("ansible-playbook is not installed, so there is nothing to compare against")
	}
	if out, err := exec.Command("ansible-doc", "ansible.windows.win_command").CombinedOutput(); err != nil {
		t.Skipf("the ansible.windows collection is not installed (ansible-galaxy collection install ansible.windows): %s", firstLine(out))
	}
	if out, err := exec.Command("python3", "-c", "import winrm").CombinedOutput(); err != nil {
		t.Skipf("Ansible's winrm connection needs pywinrm (python3 -m pip install --user pywinrm): %s", firstLine(out))
	}

	dir := certificateGateProject(t, host, cred)
	pleiades := make([]time.Duration, 0, compareRuns)
	for i := 0; i < compareRuns; i++ {
		start := time.Now()
		out, err := runPleiades(t, dir, "adhoc", "win-cert-gate", "exec.winrm.shell", `command=C:\Windows\System32\whoami.exe`, "shell=none")
		pleiades = append(pleiades, time.Since(start))
		if err != nil {
			t.Fatalf("pleiades adhoc, run %d: %v\n%s", i, err, out)
		}
	}

	certPEM, keyPEM := labCredentialPEM(t, cred)
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatalf("reading %s: %v", envWinRMCA, err)
	}
	work := t.TempDir()
	inventory := filepath.Join(work, "inventory.ini")
	// The certificate, key and authority are memory-only files inherited
	// as descriptors 3, 4 and 5, so the private key is never written to
	// disk for Ansible either.
	if err := os.WriteFile(inventory, []byte(fmt.Sprintf("win ansible_host=%s ansible_port=5986 ansible_connection=winrm "+
		"ansible_winrm_transport=certificate ansible_winrm_scheme=https ansible_winrm_server_cert_validation=validate "+
		"ansible_winrm_cert_pem=/dev/fd/3 ansible_winrm_cert_key_pem=/dev/fd/4 ansible_winrm_ca_trust_path=/dev/fd/5\n", host)), 0o600); err != nil {
		t.Fatal(err)
	}
	playbook := filepath.Join(work, "whoami.yml")
	if err := os.WriteFile(playbook, []byte("- hosts: win\n  gather_facts: false\n  tasks:\n    - name: who am i\n      ansible.windows.win_command: C:\\Windows\\System32\\whoami.exe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	secrets := []*os.File{
		memoryFile(t, "winrm-client-certificate", certPEM),
		memoryFile(t, "winrm-client-key", keyPEM),
		memoryFile(t, "winrm-authority", caPEM),
	}
	ansibleRuns := make([]time.Duration, 0, compareRuns)
	for i := 0; i < compareRuns; i++ {
		cmd := exec.Command(ansible, "-i", inventory, playbook)
		cmd.ExtraFiles = secrets
		cmd.Env = append(os.Environ(), "ANSIBLE_NOCOLOR=1", "ANSIBLE_RETRY_FILES_ENABLED=0")
		start := time.Now()
		out, err := cmd.CombinedOutput()
		ansibleRuns = append(ansibleRuns, time.Since(start))
		if err != nil {
			t.Fatalf("ansible-playbook, run %d: %v\n%s", i, err, out)
		}
	}

	t.Logf("one command, whoami.exe with no shell, %d runs each:", compareRuns)
	t.Logf("  pleiades adhoc exec.winrm.shell      %s", summarize(pleiades))
	t.Logf("  ansible-playbook win_command         %s", summarize(ansibleRuns))
}

// labCredentialPEM returns the lab credential's certificate and key as PEM,
// opening a bundle the way a stored credential is opened.
func labCredentialPEM(t *testing.T, cred labCredential) (certPEM, keyPEM []byte) {
	t.Helper()
	if cred.pfxPath == "" {
		cert, err := os.ReadFile(cred.certPath)
		if err != nil {
			t.Fatal(err)
		}
		key, err := os.ReadFile(cred.keyPath)
		if err != nil {
			t.Fatal(err)
		}
		return cert, key
	}
	bundle, err := os.ReadFile(cred.pfxPath)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := winrmexec.AuthFromSecrets(map[string]string{
		wire.SecretPFXBase64:  base64.StdEncoding.EncodeToString(bundle),
		wire.SecretPassphrase: cred.pfxPass,
	})
	if err != nil {
		t.Fatalf("opening the bundle: %v", err)
	}
	return auth.CertificatePEM, auth.PrivateKeyPEM
}

// memoryFile returns a memory-only file holding content, closed when the
// test ends. A child inherits it and opens it by its /dev/fd path as often
// as it likes, and nothing is ever written to disk.
func memoryFile(t *testing.T, name string, content []byte) *os.File {
	t.Helper()
	fd, err := unix.MemfdCreate(name, unix.MFD_CLOEXEC)
	if err != nil {
		t.Fatalf("memfd_create: %v", err)
	}
	f := os.NewFile(uintptr(fd), name)
	t.Cleanup(func() { _ = f.Close() })
	if _, err := f.Write(content); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	return f
}

// summarize reports d's count, median, 95th percentile and maximum, each
// rounded to the millisecond, the statistics pkg/winrmexec's gates report.
func summarize(d []time.Duration) string {
	sorted := append([]time.Duration(nil), d...)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a] < sorted[b] })
	return fmt.Sprintf("n=%d  p50=%v  p95=%v  max=%v", len(sorted),
		sorted[len(sorted)/2].Round(time.Millisecond), sorted[len(sorted)*95/100].Round(time.Millisecond), sorted[len(sorted)-1].Round(time.Millisecond))
}

// firstLine returns out's first line, for a skip message.
func firstLine(out []byte) string {
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return line
}
