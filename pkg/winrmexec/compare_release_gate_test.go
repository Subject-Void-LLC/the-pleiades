//go:build linux

// The industry comparison AGENTS.md asks of every Fuzz/Stress gate:
// pkg/winrmexec beside pywinrm, the WinRM client Ansible's winrm connection
// plugin is built on, measured here and now against the same host rather
// than taken from anybody's published figure.
package winrmexec

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// pywinrmResult is what testdata/pywinrm_bench.py reports for one
// workload.
type pywinrmResult struct {
	Durations []float64 `json:"durations"`
	Errors    []string  `json:"errors"`
}

// TestModesComparedToPywinrm runs the stress gate's four workloads through
// Execute and then through pywinrm, on the same host, as the same
// certificate-authenticated account, and logs the two side by side.
//
// Both sides open one shell per command and run it through the service's
// cmd.exe (pywinrm asks for that; Execute gets it whatever it asks,
// FAILURE_PATTERNS 356), so the unit of work is the same. The one design
// difference is left in on purpose, because it is the thing worth
// knowing: a pywinrm Session keeps its HTTPS connection between calls,
// while Execute builds a client per call so that no session can ever pass
// from one credential to another.
//
// It skips without a certificate credential (the lab account is
// certificate-only, and comparing two different logins would compare the
// logins), and when python3 or its winrm module is missing, saying which.
func TestModesComparedToPywinrm(t *testing.T) {
	target, auth, opts := gateHost(t)
	if len(auth.CertificatePEM) == 0 {
		t.Skip("the comparison runs on certificate authentication, so both clients present the same identity: set PLEIADES_WINRM_PFX, or _CERTIFICATE and _CERTIFICATE_KEY")
	}
	if len(opts.CACert) == 0 {
		t.Skip("the comparison verifies the host on both sides: set PLEIADES_WINRM_CA")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed, so there is no pywinrm to compare against")
	}
	if out, err := exec.Command(python, "-c", "import winrm").CombinedOutput(); err != nil {
		t.Skipf("python3 cannot import winrm (python3 -m pip install --user pywinrm): %s", bytes.TrimSpace(out))
	}

	whoami, err := CommandLine(`C:\Windows\System32\whoami.exe`)
	if err != nil {
		t.Fatal(err)
	}
	workloads := stressWorkloads(whoami)

	// Ours first, then theirs, each against a host the other has just
	// warmed, so neither pays for the host's first connection alone.
	ours := make(map[string]latencies, len(workloads))
	for _, w := range workloads {
		ours[w.label] = measureExecute(t, target, auth, opts, w.n, w.workers, w.cmd)
	}
	theirs := runPywinrm(t, python, target, auth, opts, workloads)

	for _, w := range workloads {
		t.Logf("%-24s pkg/winrmexec %v", w.label, ours[w.label])
		t.Logf("%-24s pywinrm       %v", "", theirs[w.label])
	}
}

// runPywinrm runs testdata/pywinrm_bench.py over workloads and returns each
// one's latencies. The certificate, key and authority reach Python as
// memory-only files, so the private key never touches disk; any call
// pywinrm reports as failed fails the test, as it would for Execute.
func runPywinrm(t *testing.T, python string, target Target, auth Auth, opts Options, workloads []stressWorkload) map[string]latencies {
	t.Helper()
	type spec struct {
		Label   string `json:"label"`
		N       int    `json:"n"`
		Workers int    `json:"workers"`
		Kind    string `json:"kind"`
		Command string `json:"command"`
	}
	var specs []spec
	for _, w := range workloads {
		kind, command := "cmd", w.cmd.Script
		switch w.cmd.Shell {
		case ShellPowerShell:
			kind = "ps"
		case ShellNone:
			// The program itself, which pywinrm hands to cmd.exe as its
			// command line.
			command = `C:\Windows\System32\whoami.exe`
		}
		specs = append(specs, spec{w.label, w.n, w.workers, kind, command})
	}
	input, err := json.Marshal(map[string]any{
		"endpoint":  "https://" + Addr(target, auth, opts) + "/wsman",
		"cert":      "/dev/fd/3",
		"key":       "/dev/fd/4",
		"ca":        "/dev/fd/5",
		"workloads": specs,
	})
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(python, filepath.Join("testdata", "pywinrm_bench.py"))
	cmd.Stdin = bytes.NewReader(input)
	cmd.ExtraFiles = []*os.File{
		memoryFile(t, "winrm-client-certificate", auth.CertificatePEM),
		memoryFile(t, "winrm-client-key", auth.PrivateKeyPEM),
		memoryFile(t, "winrm-authority", opts.CACert),
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("pywinrm_bench.py: %v\n%s", err, stderr.String())
	}

	var raw map[string]pywinrmResult
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		t.Fatalf("pywinrm_bench.py printed %q: %v", stdout.String(), err)
	}
	results := make(map[string]latencies, len(raw))
	for label, r := range raw {
		for _, e := range r.Errors {
			t.Errorf("pywinrm, %s: %s", label, strings.TrimSpace(e))
		}
		var d []time.Duration
		for _, seconds := range r.Durations {
			d = append(d, time.Duration(seconds*float64(time.Second)))
		}
		results[label] = newLatencies(d)
	}
	return results
}

// memoryFile returns a memory-only file holding content, closed when the
// test ends. A child that inherits it opens it by its /dev/fd path as
// often as it likes; nothing is ever written to disk.
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
