// Package main_test: the Release Gate for the build version (IMPLEMENTATION.md
// Phase 42's engine version build item).
package main_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/loader"
)

// buildinfoPackage is the package whose variables a build is stamped
// through.
const buildinfoPackage = "github.com/Subject-Void-LLC/the-pleiades/internal/buildinfo"

// buildStamped builds the Runner and the CLI into one directory with
// ldflags, as a release build would, and returns their paths.
func buildStamped(t *testing.T, ldflags string) (runner, cli string) {
	t.Helper()
	dir := t.TempDir()
	runner, cli = filepath.Join(dir, "runner"), filepath.Join(dir, "pleiades")
	for bin, pkg := range map[string]string{runner: ".", cli: "../pleiades"} {
		build := exec.Command("go", "build", "-ldflags", ldflags, "-o", bin, pkg)
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("building %s: %v\n%s", pkg, err, out)
		}
	}
	return runner, cli
}

// run runs bin with args and env added to this process's environment,
// with a deadline, and returns what it printed and its error.
func run(t *testing.T, env []string, bin string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("%s %v did not finish: %s", filepath.Base(bin), args, out)
	}
	return string(out), err
}

// TestVersionReleaseGate_OneVersionAcrossBothBinaries proves the one build
// version end to end, through the real binaries. A release build of the
// Runner and of the CLI both print the stamped version, and both hand it
// to the loader: each refuses a program that needs a newer engine, the
// Runner before it touches the network, naming both versions. A
// development build prints 0.0.0-dev+<commit> from both, and loads the
// same program with one warning.
func TestVersionReleaseGate_OneVersionAcrossBothBinaries(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the version Release Gate, which builds both binaries twice, in short mode")
	}
	collections := t.TempDir()
	if err := os.Chmod(collections, 0o700); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", filepath.Join(collections, "future"), "./testdata/futureengine")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the fixture program: %v\n%s", err, out)
	}
	digest, _, err := loader.Inspect(context.Background(), collections, "future", loader.Options{})
	if err != nil {
		t.Fatalf("inspecting the fixture: %v", err)
	}
	if err := loader.Approve(collections, loader.Approval{Program: "future", Digest: digest, ApprovedBy: "release-gate", ApprovedAt: time.Now().UTC().Format(time.RFC3339)}); err != nil {
		t.Fatalf("approving the fixture: %v", err)
	}
	env := []string{"PLEIADES_COLLECTIONS_DIR=" + collections, "NATS_URL=nats://127.0.0.1:1"}

	// A release build.
	runner, cli := buildStamped(t, "-X "+buildinfoPackage+".version=9.8.7 -X "+buildinfoPackage+".revision=abcdef0123456")
	for bin, want := range map[string]string{runner: "runner 9.8.7", cli: "pleiades 9.8.7"} {
		if out, err := run(t, nil, bin, "version"); err != nil || strings.TrimSpace(out) != want {
			t.Errorf("%s version = %q, %v; want %q", filepath.Base(bin), out, err, want)
		}
	}
	for name, args := range map[string][]string{"runner": nil, "pleiades": {"doc", "--list"}} {
		bin := runner
		if name == "pleiades" {
			bin = cli
		}
		out, err := run(t, env, bin, args...)
		if err == nil || !strings.Contains(out, "requires engine >=99.0.0, and this build is 9.8.7") {
			t.Errorf("a release %s loaded a program needing a newer engine: %v\n%s", name, err, out)
		}
	}

	// A development build: the default the image build arguments leave.
	runner, cli = buildStamped(t, "-X "+buildinfoPackage+".version=unknown -X "+buildinfoPackage+".revision=abcdef0123456")
	for bin, want := range map[string]string{runner: "runner 0.0.0-dev+abcdef012345", cli: "pleiades 0.0.0-dev+abcdef012345"} {
		if out, err := run(t, nil, bin, "version"); err != nil || strings.TrimSpace(out) != want {
			t.Errorf("%s version = %q, %v; want %q", filepath.Base(bin), out, err, want)
		}
	}
	out, err := run(t, env, cli, "doc", "--list")
	if err != nil {
		t.Fatalf("a development CLI refused the program: %v\n%s", err, out)
	}
	if n := strings.Count(out, "were not checked: gatefixture.future.run (>=99.0.0)"); n != 1 {
		t.Errorf("a development CLI warned %d time(s) about the program, want once:\n%s", n, out)
	}
	if !strings.Contains(out, "gatefixture.future.run") {
		t.Errorf("a development CLI did not list the program's method:\n%s", out)
	}
	out, err = run(t, env, cli, "doc", "gatefixture.future.run")
	if err != nil || !strings.Contains(out, ">=99.0.0 (not checked: 0.0.0-dev+abcdef012345 is a development build)") {
		t.Errorf("doc on a development build does not say the constraint went unchecked: %v\n%s", err, out)
	}
}
