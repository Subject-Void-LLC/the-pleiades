// Package main tests the release command: its names, archives, checksums and a real
// stamped build of the runner.
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport/need"
	"github.com/Subject-Void-LLC/the-pleiades/tools/internal/headroom"
)

// releaseBuildMB is what building and linking the real runner costs a
// machine whose build cache is cold, as a lab VM's is on every run: 2786 MB
// on the FreeBSD lab VM on 2026-10-29, from its testgate record, run alone.
// The link alone peaks near 2 GB on any machine, with symbols stripped.
const releaseBuildMB = 2800

// TestParsePlatforms checks the accepted and refused spellings.
func TestParsePlatforms(t *testing.T) {
	got, err := parsePlatforms("linux/amd64, windows/arm64")
	if err != nil || len(got) != 2 || got[1].String() != "windows/arm64" {
		t.Fatalf("got %v, %v", got, err)
	}
	for _, bad := range []string{"", "linux", "linux/", "/amd64", "linux/amd64,linux/amd64", "a/b/c"} {
		if _, err := parsePlatforms(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

// TestParseBinariesRefusesAPath checks that a binary name cannot leave ./cmd.
func TestParseBinariesRefusesAPath(t *testing.T) {
	for _, bad := range []string{"", "../x", "a/b", "a.b", `a\b`} {
		if _, err := parseBinaries(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	if got, err := parseBinaries("pleiades,runner"); err != nil || len(got) != 2 {
		t.Fatalf("got %v, %v", got, err)
	}
}

// TestNames checks the archive, checksum, and binary names per platform and FIPS mode.
func TestNames(t *testing.T) {
	if got := archiveName("0.3.0", Platform{"linux", "arm64"}, false); got != "the-pleiades_0.3.0_linux_arm64.tar.gz" {
		t.Errorf("linux archive %q", got)
	}
	if got := archiveName("0.3.0", Platform{"windows", "amd64"}, false); got != "the-pleiades_0.3.0_windows_amd64.zip" {
		t.Errorf("windows archive %q", got)
	}
	if got := archiveName("0.3.0", Platform{"linux", "amd64"}, true); got != "the-pleiades_0.3.0_linux_amd64_fips.tar.gz" {
		t.Errorf("linux fips archive %q", got)
	}
	if got := archiveName("0.3.0", Platform{"windows", "amd64"}, true); got != "the-pleiades_0.3.0_windows_amd64_fips.zip" {
		t.Errorf("windows fips archive %q", got)
	}
	if checksumFileName(false) != ChecksumFile || checksumFileName(true) != FIPSChecksumFile {
		t.Errorf("checksum filenames: %q, %q", checksumFileName(false), checksumFileName(true))
	}
	if binaryName("runner", Platform{"windows", "arm64"}) != "runner.exe" || binaryName("runner", Platform{"darwin", "arm64"}) != "runner" {
		t.Error("binary names wrong")
	}
}

// TestBuildEnvPinsGOFIPS140 checks that buildEnv overrides any ambient GOFIPS140
// setting and never mutates its base slice.
func TestBuildEnvPinsGOFIPS140(t *testing.T) {
	fipsBase := []string{"GOFIPS140=off"}
	gotFIPS := buildEnv(fipsBase, Platform{"linux", "amd64"}, true)
	if len(fipsBase) != 1 || fipsBase[0] != "GOFIPS140=off" {
		t.Fatalf("buildEnv mutated fipsBase: %v", fipsBase)
	}
	wantFIPS := []string{"GOFIPS140=off", "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0", "GOFIPS140=" + FIPSModuleVersion}
	if strings.Join(gotFIPS, "\n") != strings.Join(wantFIPS, "\n") {
		t.Fatalf("buildEnv(fips=true) = %v, want %v", gotFIPS, wantFIPS)
	}

	stdBase := []string{"GOFIPS140=" + FIPSModuleVersion}
	gotStd := buildEnv(stdBase, Platform{"linux", "amd64"}, false)
	if len(stdBase) != 1 || stdBase[0] != "GOFIPS140="+FIPSModuleVersion {
		t.Fatalf("buildEnv mutated stdBase: %v", stdBase)
	}
	wantStd := []string{"GOFIPS140=" + FIPSModuleVersion, "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0", "GOFIPS140=off"}
	if strings.Join(gotStd, "\n") != strings.Join(wantStd, "\n") {
		t.Fatalf("buildEnv(fips=false) = %v, want %v", gotStd, wantStd)
	}
}

// TestLdflagsStampsBothVariables checks the link flags name the real
// buildinfo variables.
func TestLdflagsStampsBothVariables(t *testing.T) {
	got := ldflags("0.3.0", "abcdef1234567")
	for _, want := range []string{"buildinfo.version=0.3.0", "buildinfo.revision=abcdef1234567", "-s -w"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q lacks %q", got, want)
		}
	}
}

// TestArchivesHoldTheirMembers writes both archive forms and reads them back.
func TestArchivesHoldTheirMembers(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "bin")
	if err := os.WriteFile(src, []byte("payload"), 0o755); err != nil {
		t.Fatal(err)
	}
	members := []member{{name: "runner", path: src, mode: 0o755}}

	tgz := filepath.Join(dir, "a.tar.gz")
	if err := writeArchive(tgz, members); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(tgz)
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	h, err := tr.Next()
	if err != nil || h.Name != "runner" || h.Mode != 0o755 {
		t.Fatalf("tar header %+v, %v", h, err)
	}
	if body, _ := io.ReadAll(tr); string(body) != "payload" {
		t.Errorf("tar body %q", body)
	}

	zp := filepath.Join(dir, "a.zip")
	if err := writeArchive(zp, members); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(zp)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	if len(zr.File) != 1 || zr.File[0].Name != "runner" {
		t.Fatalf("zip members %v", zr.File)
	}
}

// TestChecksumsMatchSha256sumFormat checks the list is sorted and that
// sha256sum itself accepts it, which is how a user verifies a download.
func TestChecksumsMatchSha256sumFormat(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"b.zip", "a.tar.gz"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeChecksums(dir, ChecksumFile, []string{"b.zip", "a.tar.gz"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, ChecksumFile))
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 || !strings.HasSuffix(lines[0], "  a.tar.gz") {
		t.Fatalf("list %q", raw)
	}
	if p, err := exec.LookPath("sha256sum"); err == nil {
		cmd := exec.Command(p, "-c", ChecksumFile)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("sha256sum -c: %v\n%s", err, out)
		}
	}
}

// TestRunRefusesBadUsage checks the usage errors exit 2 and build nothing.
func TestRunRefusesBadUsage(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"-version", "dev"},
		{"-version", "0.3.0", "-platforms", "linux"},
		{"-version", "0.3.0", "-binaries", "../x"},
		{"-version", "0.3.0", "-fips", "-notes", "docs/releases/v0.3.0.md", "-repo", "o/r"},
		{"-version", "0.3.0", "extra"},
	} {
		var out, errb bytes.Buffer
		if code := run(args, &out, &errb); code != 2 {
			t.Errorf("run(%v) = %d, want 2; stderr %q", args, code, errb.String())
		}
	}
}

// TestReleaseBuildReportsItsVersion builds the real runner for this machine
// through the command and runs it, proving the stamp reaches the binary a
// user downloads and that an archive and its checksum list are written.
func TestReleaseBuildReportsItsVersion(t *testing.T) {
	if testing.Short() {
		t.Skip("links a real binary")
	}
	// A machine that cannot hold the link above the gate's memory floor
	// skips, named in the skip ledger, rather than have the gate stop the
	// whole run for it (FAILURE_PATTERNS 732). A machine whose memory cannot
	// be read runs it.
	if m, err := headroom.Read(); err == nil {
		need.Require(t, "memory", m.AvailableMB-releaseBuildMB >= headroom.FloorMB,
			fmt.Sprintf("building and linking the runner costs about %d MB, and %.0f MB free would leave this machine under the gate's %d MB memory floor",
				releaseBuildMB, m.AvailableMB, headroom.FloorMB))
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	out := t.TempDir()
	plat := runtime.GOOS + "/" + runtime.GOARCH
	var stdout, stderr bytes.Buffer
	code := run([]string{"-version", "9.8.7", "-platforms", plat, "-binaries", "runner", "-out", out, "-revision", "0123456789abcdef"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	name := archiveName("9.8.7", Platform{runtime.GOOS, runtime.GOARCH}, false)
	if runtime.GOOS == "windows" {
		t.Skip("the check below reads a tar")
	}
	raw, err := os.ReadFile(filepath.Join(out, name))
	if err != nil {
		t.Fatal(err)
	}
	gz, _ := gzip.NewReader(bytes.NewReader(raw))
	tr := tar.NewReader(gz)
	bin := filepath.Join(t.TempDir(), "runner")
	for {
		h, err := tr.Next()
		if err != nil {
			t.Fatal("no runner in the archive")
		}
		if h.Name != "runner" {
			continue
		}
		f, _ := os.OpenFile(bin, os.O_CREATE|os.O_WRONLY, 0o755)
		if _, err := io.Copy(f, tr); err != nil { // #nosec G110 -- an archive this test just built
			t.Fatal(err)
		}
		f.Close()
		break
	}
	meta, err := exec.Command("go", "version", "-m", bin).CombinedOutput()
	if err != nil {
		t.Fatalf("go version -m: %v\n%s", err, meta)
	}
	if strings.Contains(string(meta), "GOFIPS140="+FIPSModuleVersion) {
		t.Fatalf("standard release build unexpectedly carries GOFIPS140=%s:\n%s", FIPSModuleVersion, meta)
	}
	got, err := exec.Command(bin, "version").CombinedOutput()
	if err != nil || !strings.Contains(string(got), "9.8.7") {
		t.Fatalf("runner version = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(out, ChecksumFile)); err != nil {
		t.Error(err)
	}
	// 9.8.7 has no codename, so its title is the tag alone; the runner's
	// version command prints no codename line for it: the version, then the
	// external Collection protocols it speaks (Phase 135b), nothing between.
	if title, _ := os.ReadFile(filepath.Join(out, TitleFile)); string(title) != "v9.8.7\n" {
		t.Errorf("release title = %q", title)
	}
	if lines := strings.Split(strings.TrimSpace(string(got)), "\n"); len(lines) != 2 || !strings.HasPrefix(lines[1], "external collection protocols: ") {
		t.Errorf("an unnamed release printed %q, want its version and then its protocols", got)
	}
}

// TestReleaseBuildFIPSStampsModuleVersion builds the real runner with -fips
// and verifies the archive name, SHA256SUMS.fips, embedded GOFIPS140 build
// setting, default fips140=on execution, and strict GODEBUG=fips140=only boot.
func TestReleaseBuildFIPSStampsModuleVersion(t *testing.T) {
	if testing.Short() {
		t.Skip("links a real binary")
	}
	if m, err := headroom.Read(); err == nil {
		need.Require(t, "memory", m.AvailableMB-releaseBuildMB >= headroom.FloorMB,
			fmt.Sprintf("building and linking the runner costs about %d MB, and %.0f MB free would leave this machine under the gate's %d MB memory floor",
				releaseBuildMB, m.AvailableMB, headroom.FloorMB))
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	out := t.TempDir()
	plat := runtime.GOOS + "/" + runtime.GOARCH
	var stdout, stderr bytes.Buffer
	code := run([]string{"-version", "9.8.7", "-fips", "-platforms", plat, "-binaries", "runner", "-out", out, "-revision", "0123456789abcdef"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(out, FIPSChecksumFile)); err != nil {
		t.Fatalf("missing %s: %v", FIPSChecksumFile, err)
	}
	if _, err := os.Stat(filepath.Join(out, ChecksumFile)); !os.IsNotExist(err) {
		t.Fatalf("standard %s should not be written in -fips mode (stat err = %v)", ChecksumFile, err)
	}
	if p, err := exec.LookPath("sha256sum"); err == nil {
		cmd := exec.Command(p, "-c", FIPSChecksumFile)
		cmd.Dir = out
		if sumOut, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("sha256sum -c %s: %v\n%s", FIPSChecksumFile, err, sumOut)
		}
	}
	name := archiveName("9.8.7", Platform{runtime.GOOS, runtime.GOARCH}, true)
	if !strings.Contains(name, "_fips.") {
		t.Fatalf("expected _fips suffix in %q", name)
	}
	if runtime.GOOS == "windows" {
		t.Skip("the check below reads a tar")
	}
	raw, err := os.ReadFile(filepath.Join(out, name))
	if err != nil {
		t.Fatal(err)
	}
	gz, _ := gzip.NewReader(bytes.NewReader(raw))
	tr := tar.NewReader(gz)
	bin := filepath.Join(t.TempDir(), "runner")
	for {
		h, err := tr.Next()
		if err != nil {
			t.Fatal("no runner in the archive")
		}
		if h.Name != "runner" {
			continue
		}
		f, _ := os.OpenFile(bin, os.O_CREATE|os.O_WRONLY, 0o755)
		if _, err := io.Copy(f, tr); err != nil { // #nosec G110 -- an archive this test just built
			t.Fatal(err)
		}
		f.Close()
		break
	}
	meta, err := exec.Command("go", "version", "-m", bin).CombinedOutput()
	if err != nil {
		t.Fatalf("go version -m: %v\n%s", err, meta)
	}
	fipsSetting := regexp.MustCompile(`(?m)^\s*build\s+GOFIPS140=` + regexp.QuoteMeta(FIPSModuleVersion) + `(-[0-9a-f]+)?$`)
	if !fipsSetting.Match(meta) {
		t.Fatalf("FIPS release build missing GOFIPS140=%s in go version -m:\n%s", FIPSModuleVersion, meta)
	}
	got, err := exec.Command(bin, "version").CombinedOutput()
	if err != nil || !strings.Contains(string(got), "9.8.7") {
		t.Fatalf("runner version (default fips140=on) = %q, %v", got, err)
	}
	strictCmd := exec.Command(bin, "version")
	strictCmd.Env = append(os.Environ(), "GODEBUG=fips140=only")
	strictGot, err := strictCmd.CombinedOutput()
	if err != nil || !strings.Contains(string(strictGot), "9.8.7") {
		t.Fatalf("runner version (GODEBUG=fips140=only) = %q, %v", strictGot, err)
	}
}
