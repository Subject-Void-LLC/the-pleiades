//go:build integration

// Phase 20's Release Gate, shared half.
//
// The gate proves a real install of this repository's shipped artifacts,
// twice: once with `docker compose up -d --wait` on a Docker host, and
// once with `helm install` into a real Kubernetes cluster. This file holds
// what both halves need and neither owns: finding the external tools,
// skipping cleanly when they are absent, running them with their output
// attached to the failure, and reading the contents of a built image.
//
// Everything here shells out to the real `docker`, `kind`, `kubectl` and
// `helm` binaries an operator types, rather than to a Go client library.
// That is deliberate and it is the whole point of the gate: a Go client
// speaking to the Docker API would prove the API works, and the claim
// under test is that the documented commands work. The commands in
// docker-compose.yml's header comment, in NOTES.txt and in
// docs/10-running-in-production.md are the specification these tests
// execute.
//
// SKIPPING IS NOT FAILING. A machine with no Docker, no kind, or no Helm
// cannot run this gate, and a test that fails there teaches a developer to
// ignore the failure. Every entry point below skips with a message naming
// the missing tool and what it was needed for.
package e2e

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// The two locally built images this repository ships, named exactly as
// docker-compose.yml names them and as helm/the-pleiades defaults to.
//
// Written out rather than parsed from docker-compose.yml because the
// values are also the chart's defaults, so a change to either has to be a
// deliberate edit in three places that internal/testsupport's own
// TestComposeImagesMatchPins-style checks would not see. If these ever
// drift, the gate fails on a missing image, which is loud.
const (
	packagingControllerImage = "pleiades/controller:dev"
	packagingRunnerImage     = "pleiades/runner:dev"
)

// packagingImageUID is the numeric user both images must run as.
//
// A number, not a name: Kubernetes cannot enforce runAsNonRoot against a
// USER that is a name, because the kubelet has no way to resolve a name
// inside an image it has not started, and it answers with
// CreateContainerConfigError instead. Dockerfile.controller carries the
// same reasoning at its own USER line.
const packagingImageUID = "65532:65532"

// packagingShellNames are the basenames that make a container image a
// place an attacker can work rather than a place a single binary runs.
//
// The controller image holds the master encryption key and the JWT
// signing secret, so a shell there is worth far more than a shell in the
// broker. Both images are built on distroless for that reason, and this
// list is what proves the base was not quietly swapped for one with a
// package manager in it.
var packagingShellNames = map[string]bool{
	"sh": true, "bash": true, "ash": true, "dash": true, "zsh": true,
	"ksh": true, "csh": true, "tcsh": true, "fish": true, "busybox": true,
}

// packagingBinDirs are the directories a shell or a utility would land in.
// Distroless leaves all four present and empty, which is a stronger claim
// than "no file is named sh": it says there is nothing to run at all.
var packagingBinDirs = []string{"bin/", "sbin/", "usr/bin/", "usr/sbin/"}

// requirePackagingTool skips the calling test unless the named binary is
// on PATH, and returns its resolved path.
//
// why is printed in the skip message, because "kind not found" without it
// leaves a reader guessing whether the test needed a cluster or merely a
// version string.
func requirePackagingTool(t *testing.T, name, why string) string {
	t.Helper()
	resolved, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s not found on PATH; this gate needs it to %s", name, why)
	}
	return resolved
}

// requireDockerDaemon skips unless docker is on PATH AND the daemon
// answers.
//
// Both halves are needed. `docker` on PATH with no daemon behind it is a
// common shape (a client installed by a package manager, a stopped Docker
// Desktop), and every command this gate runs would then fail several
// minutes in with a connection error rather than skipping in a second.
func requireDockerDaemon(t *testing.T) {
	t.Helper()
	requirePackagingTool(t, "docker", "build and run the shipped images")

	// `docker version` talks to the daemon; `docker --version` does not,
	// which is exactly the distinction that makes this check worth making.
	out, err := exec.Command("docker", "version", "--format", "{{.Server.Version}}").CombinedOutput()
	if err != nil {
		t.Skipf("the docker daemon did not answer, so there is nothing to install into: %v\n%s", err, out)
	}
	t.Logf("docker daemon version %s", strings.TrimSpace(string(out)))
}

// packagingCommand builds one external command rooted at the repository,
// with the environment this gate needs and the caller's additions on top.
//
// dir is the working directory. Every compose invocation needs the
// repository root, because `docker compose` finds docker-compose.yml
// relative to the working directory and derives the project name from that
// directory's own name.
func packagingCommand(t *testing.T, dir string, env []string, name string, args ...string) *exec.Cmd {
	t.Helper()
	// #nosec G204 -- name and args are fixed literals and repository paths
	// built by this test, never user input. internal/testsupport's own
	// docker build helper carries the same shape and the same reasoning.
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	return cmd
}

// runPackagingTool runs one command and returns its combined output plus
// whatever error it exited with, leaving the decision to the caller.
//
// Combined rather than separated because every one of these tools writes
// the sentence explaining a failure to stderr and the answer to stdout,
// and a failure report holding only one of the two has always been the
// half that does not explain anything.
func runPackagingTool(t *testing.T, dir string, env []string, stdin, name string, args ...string) (string, error) {
	t.Helper()
	cmd := packagingCommand(t, dir, env, name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	started := time.Now()
	out, err := cmd.CombinedOutput()
	t.Logf("%s %s (%s)", name, strings.Join(maskSecretArgs(args), " "), time.Since(started).Round(time.Millisecond))
	return string(out), err
}

// secretArgKeys are the substrings that make an argument's value worth
// keeping out of a log.
//
// Everything this gate passes is a throwaway constant declared a few
// screens above the call, so nothing real is at stake here. It is masked
// anyway, because "it is only a test secret" is how the habit of printing
// credentials into build output gets established, and a test log is
// archived by CI exactly like any other.
var secretArgKeys = []string{"password", "secret", "key", "token"}

// maskSecretArgs replaces the value half of any `key=value` argument whose
// key names a credential, leaving everything else readable so a failing
// command can still be copied and rerun.
func maskSecretArgs(args []string) []string {
	masked := make([]string, len(args))
	for i, arg := range args {
		masked[i] = arg
		split := strings.Index(arg, "=")
		if split < 0 {
			continue
		}
		lowered := strings.ToLower(arg[:split])
		for _, marker := range secretArgKeys {
			if strings.Contains(lowered, marker) {
				masked[i] = arg[:split+1] + "REDACTED"
				break
			}
		}
	}
	return masked
}

// mustRunPackagingTool is runPackagingTool with the failure already
// decided: the command is one the gate cannot continue without.
func mustRunPackagingTool(t *testing.T, dir string, env []string, stdin, name string, args ...string) string {
	t.Helper()
	out, err := runPackagingTool(t, dir, env, stdin, name, args...)
	if err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return out
}

// packagingBuildInputs are the paths a change to which makes a built image
// out of date, relative to the repository root.
//
// It is the build context .dockerignore actually allows (go.mod, go.sum,
// cmd/, internal/, pkg/), plus the two Dockerfiles and the compose file
// that decides how they are built. Anything outside this list cannot
// change what lands in either image, so including it would mean rebuilding
// for a documentation edit.
var packagingBuildInputs = []string{
	"go.mod", "go.sum",
	"cmd", "internal", "pkg",
	"Dockerfile.controller", "Dockerfile.runner",
	"docker-compose.yml",
}

// ensurePleiadesImages builds the controller and runner images if they are
// missing OR older than the sources they are built from, and returns the
// repository root.
//
// It exists so that every test here can be run on its own with `-run`,
// which is how this package has to be run at all: tests/e2e is listed in
// flaky-packages.json, so a developer verifying one gate runs that gate by
// name rather than the whole suite. A test that silently depended on an
// earlier test having built the images would pass in a full run and fail
// alone, which is the worst ordering to debug.
//
// STALENESS, NOT MERE ABSENCE, IS THE TRIGGER, and the difference is the
// whole point of this gate. Rebuilding only when no image carries the name
// means a machine that has ever built `pleiades/controller:dev` keeps
// validating the shipped artifacts against whatever source produced that
// image, which on a developer's machine is routinely the source from
// before the change under test. The gate would then report that a fix
// works while never having run it. Comparing the image's creation time
// against the newest build input makes the answer about content rather
// than about the name existing.
//
// The comparison is deliberately conservative: any input newer than the
// image rebuilds, even when the content is identical (a `git checkout`
// touches mtimes without changing bytes). That costs a cached build of a
// few seconds and cannot produce a false pass, which is the direction an
// error here should point.
//
// `docker compose build` rather than two `docker build` invocations,
// because the build arguments that stamp the OCI provenance labels live in
// docker-compose.yml and a hand-written `docker build` would omit them.
func ensurePleiadesImages(t *testing.T) string {
	t.Helper()
	root := testsupport.RepoRoot(t)
	newestInput := newestBuildInput(t, root)

	rebuild := ""
	for _, image := range []string{packagingControllerImage, packagingRunnerImage} {
		built, ok := imageCreatedAt(t, image)
		switch {
		case !ok:
			rebuild = fmt.Sprintf("%s is not present", image)
		case built.Before(newestInput):
			rebuild = fmt.Sprintf("%s was built %s, before the newest source change at %s",
				image, built.Format(time.RFC3339), newestInput.Format(time.RFC3339))
		}
		if rebuild != "" {
			break
		}
	}
	if rebuild == "" {
		t.Logf("both shipped images are newer than every build input (newest input %s)",
			newestInput.Format(time.RFC3339))
		return root
	}

	t.Logf("building the shipped images: %s", rebuild)
	mustRunPackagingTool(t, root, nil, "", "docker", "compose", "build")

	// Stated rather than asserted. A build whose layers were all cached
	// leaves the image ID, and therefore its creation time, unchanged, so a
	// failure here would fail the harmless case (mtimes moved, bytes did
	// not) while proving nothing about the one that matters.
	for _, image := range []string{packagingControllerImage, packagingRunnerImage} {
		if built, ok := imageCreatedAt(t, image); ok {
			t.Logf("%s now reports created %s", image, built.Format(time.RFC3339))
		}
	}
	return root
}

// imageCreatedAt reports when a local image was built, and whether it
// exists at all.
//
// The value comes from `docker image inspect`, which is the daemon's own
// record of when that image ID was created, so it moves only when a build
// really produces a new image.
func imageCreatedAt(t *testing.T, image string) (time.Time, bool) {
	t.Helper()
	out, err := exec.Command("docker", "image", "inspect", "--format", "{{.Created}}", image).Output()
	if err != nil {
		return time.Time{}, false
	}
	created, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(out)))
	if err != nil {
		// An image that exists but whose timestamp cannot be read is
		// treated as absent, which rebuilds. The alternative would be to
		// treat an unreadable answer as "fresh enough", which is the
		// overclaim this whole helper exists to remove.
		t.Logf("could not read the creation time of %s (%v), so it is treated as stale", image, err)
		return time.Time{}, false
	}
	return created, true
}

// newestBuildInput is the most recent modification time across every path
// in packagingBuildInputs.
//
// Directories are walked; a missing path is skipped rather than fatal, so
// that adding or removing a Dockerfile does not break this helper before
// anybody notices the list is out of date. A walk error is logged for the
// same reason: the honest fallback is an OLDER answer, which rebuilds more
// often rather than less.
func newestBuildInput(t *testing.T, root string) time.Time {
	t.Helper()
	var newest time.Time
	consider := func(info fs.FileInfo) {
		if info.IsDir() {
			return
		}
		if modified := info.ModTime(); modified.After(newest) {
			newest = modified
		}
	}

	for _, input := range packagingBuildInputs {
		path := filepath.Join(root, input)
		info, err := os.Stat(path)
		if err != nil {
			t.Logf("build input %s could not be read (%v), so it is not counted", input, err)
			continue
		}
		if !info.IsDir() {
			consider(info)
			continue
		}
		err = filepath.WalkDir(path, func(_ string, entry fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			entryInfo, err := entry.Info()
			if err != nil {
				return nil
			}
			consider(entryInfo)
			return nil
		})
		if err != nil {
			t.Logf("walking build input %s: %v", input, err)
		}
	}
	if newest.IsZero() {
		t.Fatal("no build input could be read, so image staleness cannot be decided and a pass " +
			"here would mean nothing")
	}
	return newest
}

// imageEntry is one path inside a built container image, with enough of
// its tar header to tell a directory from a program.
type imageEntry struct {
	// Name is the path relative to the image root, with no leading slash,
	// exactly as `docker export` writes it.
	Name string
	// Mode carries the permission bits, so an executable can be told from
	// a data file.
	Mode int64
	// Size is the file's length in bytes.
	Size int64
	// Regular is true for an ordinary file, false for a directory, symlink
	// or device node.
	Regular bool
}

// exportImageEntries lists every path inside an image by streaming
// `docker export` through a tar reader.
//
// This is the honest way to ask "what is in this image". The alternatives
// were both weaker: running `/bin/sh` and checking the exit code proves
// only that one path is missing (and cannot distinguish "no shell" from
// "no permission"), and `docker history` reports the instructions that
// built the layers rather than the files that survived them.
//
// The export is streamed rather than written to a file. The controller
// image exports to about 64 MB of tar, and nothing here needs it twice.
func exportImageEntries(t *testing.T, image string) []imageEntry {
	t.Helper()

	// A container has to exist before its filesystem can be exported, and
	// it is never started. --entrypoint pointing at a path that does not
	// exist is what makes `docker create` accept an image whose own
	// configuration supplies no command, which the build-context probe
	// image below deliberately does not.
	created, err := exec.Command("docker", "create", "--entrypoint=/nonexistent-by-design", image).Output()
	if err != nil {
		t.Fatalf("docker create %s: %v", image, err)
	}
	container := strings.TrimSpace(string(created))
	t.Cleanup(func() {
		if err := exec.Command("docker", "rm", "-f", container).Run(); err != nil {
			t.Logf("removing the export container %s: %v", container, err)
		}
	})

	export := exec.Command("docker", "export", container)
	stream, err := export.StdoutPipe()
	if err != nil {
		t.Fatalf("opening the export stream for %s: %v", image, err)
	}
	if err := export.Start(); err != nil {
		t.Fatalf("docker export %s: %v", image, err)
	}

	var entries []imageEntry
	reader := tar.NewReader(stream)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("reading the exported filesystem of %s: %v", image, err)
		}
		entries = append(entries, imageEntry{
			Name:    strings.TrimPrefix(header.Name, "./"),
			Mode:    header.Mode,
			Size:    header.Size,
			Regular: header.Typeflag == tar.TypeReg,
		})
	}
	if err := export.Wait(); err != nil {
		t.Fatalf("docker export %s did not finish cleanly: %v", image, err)
	}
	return entries
}

// composeService is the sliver of `docker compose ps --format json` this
// gate reads. Everything else that command prints (labels, mounts,
// publishers) is deliberately not modeled, so an unrelated change to the
// compose file does not break the parse.
type composeService struct {
	Service string `json:"Service"`
	State   string `json:"State"`
	Health  string `json:"Health"`
}

// composeServices returns what compose says is running right now.
//
// Running only, with no --all. A service whose container exited is then
// absent from the map rather than present with a stopped state, which is
// what the caller's "all four are here" assertion is built on. That is the
// exact defect docker-compose.yml's controller healthcheck comment
// records: a dead controller was invisible to plain `ps` while `up --wait`
// still exited 0.
//
// The output is newline-delimited JSON, one object per container, not a
// JSON array. json.Decoder reads exactly that shape by decoding values
// until the stream ends, which is why this is not an Unmarshal into a
// slice.
func composeServices(t *testing.T, root string) map[string]composeService {
	t.Helper()
	out := mustRunPackagingTool(t, root, nil, "", "docker", "compose", "ps", "--format", "json")

	services := map[string]composeService{}
	decoder := json.NewDecoder(strings.NewReader(out))
	for {
		var one composeService
		if err := decoder.Decode(&one); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("decoding `docker compose ps --format json`: %v\n%s", err, out)
		}
		services[one.Service] = one
	}
	return services
}

// treeBytes sums the size of every ordinary file under root.
//
// Directories are excluded, because their reported size is filesystem
// bookkeeping rather than content, and counting them would inflate the
// denominator this gate compares the build context against.
func treeBytes(t *testing.T, root string) int64 {
	t.Helper()
	var total int64
	err := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			// A file that vanished mid-walk (a temporary file from a
			// concurrent build) must not fail a size measurement.
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
	return total
}

// humanBytes formats a byte count for a log line a person reads.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n/div >= unit && exp < 3 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}

// topLevel returns the first path element of an image or archive entry,
// which is what identifies a repository directory that leaked into a build
// context.
func topLevel(name string) string {
	cleaned := path.Clean(strings.TrimPrefix(name, "./"))
	if i := strings.Index(cleaned, "/"); i >= 0 {
		return cleaned[:i]
	}
	return cleaned
}
