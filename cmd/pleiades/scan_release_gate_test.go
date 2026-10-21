// Release gate for the scan methods: through the real binary, a device
// that is a real SSH server (in this process, its commands run by this
// machine's /bin/sh) onboarded with the engine it reaches (this machine's
// Docker, through the SSH session), on which the real pinned scanners
// run. A check of a runbook that scans and writes an SBOM writes nothing;
// the run writes the SBOM, which sha256sum verifies and a rollback
// removes; a vulnerability and a planted credential each fail their task,
// the credential's value in no output.
package main_test

import (
	"archive/zip"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/scan"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// The scanner images, named here as well as in the scan package so CI's
// image list (tools/testimages, which reads literals) caches them for this
// package's shard; the gate refuses to run when the two disagree.
const (
	gateOSVScannerImage = "ghcr.io/google/osv-scanner:v2.6.0@sha256:afd838850ac1a0fcc15ff4a041dc9ba11123c3f0d2666217a5f0fcf9222b55fa"
	gateSyftImage       = "anchore/syft:v1.52.0@sha256:500e2d872ac019436926e8322b4fc1f39441d94d21f6f4046c6ff29b30e8cb02"
	gateGitleaksImage   = "zricethezav/gitleaks:v8.30.0@sha256:691af3c7c5a48b16f187ce3446d5f194838f91238f27270ed36eef6359a574d9"
)

// TestCLI_ScanCheckWritesNothingAndTheRunDoesTheWork is the release gate.
func TestCLI_ScanCheckWritesNothingAndTheRunDoesTheWork(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the real scanners on this machine's Docker engine")
	}
	info, err := exec.Command("docker", "info", "--format", "{{.ID}}").Output()
	testsupport.Require(t, "docker", err == nil && len(strings.TrimSpace(string(info))) > 0, "the scanners run on this machine's Docker engine")
	// A check pulls nothing, so the check below covers its tasks only with
	// the scanners already on the engine: that is this gate's precondition,
	// made here rather than left to whatever this machine last pulled.
	gate := []string{gateOSVScannerImage, gateSyftImage, gateGitleaksImage}
	if !slices.Equal(gate, scan.ScannerImages()) {
		t.Fatalf("the gate names %v and the scan methods run %v; update the gate's constants", gate, scan.ScannerImages())
	}
	for _, image := range gate {
		if exec.Command("docker", "image", "inspect", image).Run() == nil { // #nosec G204 -- a pinned image constant
			continue
		}
		if out, err := exec.Command("docker", "pull", "-q", image).CombinedOutput(); err != nil { // #nosec G204 -- same
			t.Fatalf("pulling %s: %v\n%s", image, err, out)
		}
	}
	need.POSIXShell(t)
	srv, err := remoteexectest.Start(remoteexectest.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	knownHosts := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(knownHosts, []byte(knownhosts.Line([]string{net.JoinHostPort(srv.Host, strconv.Itoa(srv.Port))}, srv.HostKey)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{remoteexec.KnownHostsEnv: knownHosts}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"add-host", "scan1", "--type", "linux_server", "--set", "host=" + srv.Host, "--set", "port=" + strconv.Itoa(srv.Port)},
		{"add-credential", "scan1", "--username", srv.Username, "--password", srv.Password},
	} {
		if out, err := runPleiadesWithEnv(t, dir, env, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	onboarded, err := runPleiadesWithEnv(t, dir, env, "onboard", "scan1", "--json")
	if err != nil || !strings.Contains(onboarded, "ContainerEngineCapable") {
		t.Fatalf("onboarding found no engine: %v\n%s", err, onboarded)
	}
	// The shipped example validates against a device onboarding proved
	// runs an engine; as shipped, release-box has not been onboarded, so
	// examples_test.go leaves it to this gate.
	example, err := os.ReadFile(filepath.Join("..", "..", "examples", "scan_lab", "pleiades", "runbooks", "release.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(example), "hosts: release-box\n") {
		t.Fatalf("examples/scan_lab's release.yaml no longer targets release-box:\n%s", example)
	}
	if err := os.WriteFile(filepath.Join(dir, "runbooks", "example-release.yaml"), []byte(strings.Replace(string(example), "hosts: release-box\n", "hosts: scan1\n", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := runPleiadesWithEnv(t, dir, env, "validate", "runbooks/example-release.yaml"); err != nil || !strings.Contains(out, "no issues found") || strings.Contains(out, "[warning]") {
		t.Fatalf("validate refuses examples/scan_lab's release.yaml on an onboarded engine host: %v\n%s", err, out)
	}

	// The project, an offline vulnerability database, a tree with a
	// planted credential, and an ignore file, all on the device.
	files := t.TempDir()
	write := func(rel, content string) string {
		p := filepath.Join(files, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	_ = os.Chmod(files, 0o755)
	project := filepath.Dir(write("app/package.json", `{"name":"demo","version":"1.0.0","dependencies":{"left-pad":"^1.3.0"}}`))
	write("app/package-lock.json", `{"name":"demo","version":"1.0.0","lockfileVersion":3,"packages":{"":{"name":"demo","version":"1.0.0","dependencies":{"left-pad":"^1.3.0"}},"node_modules/left-pad":{"version":"1.3.0","resolved":"https://registry.npmjs.org/left-pad/-/left-pad-1.3.0.tgz","integrity":"sha512-XI5MPzVNApjAyhQzphX8BkmKsKUxD4LdyK24iZeQEBAhFFX6+AGJCqp7KvP5+WmIp5Ud71wOdvFE/tDHg6P2A=="}}}`)
	const planted = "AKIAZ7QXPLMNB2VKD4RT"
	leaky := filepath.Dir(filepath.Dir(write("leaky/conf/app.ini", "aws_access_key_id = "+planted+"\n")))
	db := filepath.Join(files, "osvdb")
	if err := os.MkdirAll(filepath.Join(db, "osv-scalibr", "npm"), 0o755); err != nil {
		t.Fatal(err)
	}
	zf, err := os.Create(filepath.Join(db, "osv-scalibr", "npm", "all.zip")) // #nosec G304 -- this test's own directory
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(zf)
	w, _ := z.Create("GHSA-gate.json")
	_, _ = w.Write([]byte(`{"schema_version":"1.6.0","id":"GHSA-gate","modified":"2026-01-01T00:00:00Z","summary":"test","affected":[{"package":{"ecosystem":"npm","name":"left-pad"},"ranges":[{"type":"SEMVER","events":[{"introduced":"0"},{"fixed":"1.3.1"}]}]}],"severity":[{"type":"CVSS_V3","score":"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"}]}`))
	_ = z.Close()
	_ = zf.Chmod(0o644)
	_ = zf.Close()
	for _, d := range []string{db, filepath.Join(db, "osv-scalibr"), filepath.Join(db, "osv-scalibr", "npm")} {
		_ = os.Chmod(d, 0o755)
	}
	ignore := write("scan-ignore.yaml", "- id: GHSA-gate\n  reason: left-pad runs only in a build script\n  until: "+time.Now().AddDate(0, 1, 0).Format("2006-01-02")+"\n")
	dest := filepath.Join(files, "sbom-1.0.0")

	report := func(args ...string) (jsonReport, string) {
		t.Helper()
		out, _ := runPleiadesWithEnv(t, dir, env, append(args, "--json")...)
		var r jsonReport
		if err := json.NewDecoder(strings.NewReader(out)).Decode(&r); err != nil {
			t.Fatalf("pleiades %v printed %q: %v", args, out, err)
		}
		return r, out
	}
	release := writeRunbook(t, dir, "release", "id: release\nhosts: scan1\ntasks:\n"+
		"  - name: vulnerabilities\n    scan.vuln:\n      engine: docker\n      dir: "+project+"\n      offline_db: "+db+"\n      ignore: "+ignore+"\n"+
		"  - name: sbom\n    scan.sbom:\n      engine: docker\n      dir: "+project+"\n      dest: "+dest+"\n      name: demo\n      version: 1.0.0\n      supplier: \"Organization: Example Co\"\n"+
		"  - name: credentials\n    scan.secrets:\n      engine: docker\n      dir: "+project+"\n")
	if out, err := runPleiadesWithEnv(t, dir, env, "validate", release); err != nil {
		t.Fatalf("validate: %v\n%s", err, out)
	}

	checked, out := report("run", release, "--mode", "check")
	if checked.Outcome.ExitCode != 0 || len(checked.Tasks) != 3 {
		t.Fatalf("the check: %+v %+v\n%s", checked.Outcome, checked.Tasks, out)
	}
	for i, want := range []string{"ok", "would_change", "ok"} {
		if checked.Tasks[i].Status != want {
			t.Errorf("the check of %s is %s, want %s", checked.Tasks[i].Name, checked.Tasks[i].Status, want)
		}
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("a check wrote %s", dest)
	}

	ran, out := report("run", release)
	if ran.Outcome.ExitCode != 0 {
		t.Fatalf("the run: %+v\n%s", ran.Tasks, out)
	}
	cmd := exec.Command("sha256sum", "-c", "SHA256SUMS")
	cmd.Dir = dest
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sha256sum -c: %v\n%s", err, out)
	}
	if rolled, out := report("rollback", ran.RunID); rolled.Outcome.ExitCode != 0 {
		t.Fatalf("the rollback: %+v\n%s", rolled.Outcome, out)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("the rollback left %s", dest)
	}

	// A vulnerability and a planted credential each fail their own task.
	vulnGate := writeRunbook(t, dir, "vulngate", "id: vulngate\nhosts: scan1\ntasks:\n  - name: vulnerabilities\n    scan.vuln:\n      engine: docker\n      dir: "+project+"\n      offline_db: "+db+"\n")
	if r, out := report("run", vulnGate); len(r.Tasks) != 1 || !strings.Contains(r.Tasks[0].Error, "GHSA-gate (critical); fixed in 1.3.1") {
		t.Errorf("a critical vulnerability: %+v\n%s", r.Tasks, out)
	}
	leakGate := writeRunbook(t, dir, "leakgate", "id: leakgate\nhosts: scan1\ntasks:\n  - name: credentials\n    scan.secrets:\n      engine: docker\n      dir: "+leaky+"\n")
	r, out := report("run", leakGate)
	if len(r.Tasks) != 1 || !strings.Contains(r.Tasks[0].Error, "conf/app.ini:1: aws-access-token") {
		t.Errorf("a planted credential: %+v\n%s", r.Tasks, out)
	}
	if strings.Contains(out, planted) {
		t.Fatal("the planted credential's value appeared in the run's output")
	}
	if doc, err := runPleiades(t, dir, "doc", "scan.vuln"); err != nil || !strings.Contains(doc, "Lists the known vulnerabilities") {
		t.Errorf("pleiades doc scan.vuln: %v\n%s", err, doc)
	}
}
