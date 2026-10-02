// Independent checks of package iso9660's output through real ISO 9660
// readers running in a container: xorriso (libisofs), bsdtar (libarchive)
// and the Linux kernel's own iso9660 driver, which is what a cloud-init
// guest mounts its NoCloud seed with. None of them shares code with this
// package, so each one agreeing is evidence the image is right rather
// than merely self-consistent.
package iso9660_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/iso9660"
)

// readerImage is the container the ISO readers run in. It is pinned to an
// exact Alpine release under the rule internal/testsupport's package
// documentation states, and declared here, as pkg/netconf declares its
// NETCONF server image, because nothing else in the repository runs it.
// The readers themselves come from that release's package repository.
const readerImage = "alpine:3.20.10"

// readerPackages pins each reader to the exact version readerImage's
// release serves. The kernel check installs one in a privileged
// container, so what runs there is chosen here rather than by whatever the
// repository serves on the day; a release that drops a version fails the
// install, and the pin is then moved on purpose.
var readerPackages = map[string]string{
	"xorriso":          "xorriso=1.5.6-r0",
	"libarchive-tools": "libarchive-tools=3.8.3-r0",
}

// seedPath is where the image under test is copied inside a container.
const seedPath = "/work/seed.iso"

// startReader starts readerImage with img at seedPath, installs packages
// from Alpine's repositories, and returns the running container. It skips
// the test when Docker is unavailable outside CI, as the repository's
// other container tests do, and fails it when installing packages fails,
// since the readers are the whole point of the test.
func startReader(t *testing.T, img []byte, privileged bool, packages ...string) testcontainers.Container {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping the container-backed ISO reader checks in -short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), testsupport.ContainerStartupTimeout)
	defer cancel()
	req := testcontainers.ContainerRequest{
		Image: readerImage,
		Cmd:   []string{"sleep", "600"},
		Files: []testcontainers.ContainerFile{{Reader: bytes.NewReader(img), ContainerFilePath: seedPath, FileMode: 0o644}},
	}
	if privileged {
		// Only the kernel mount check asks for this: a loop mount needs
		// CAP_SYS_ADMIN and access to /dev/loop*.
		req.HostConfigModifier = func(hc *container.HostConfig) { hc.Privileged = true }
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: req, Started: true})
	if err != nil {
		_ = testcontainers.TerminateContainer(c) // a failed start still returns its container
		if os.Getenv("CI") == "" {
			t.Skipf("could not start the %s container (is Docker running?): %v", readerImage, err)
		}
		t.Fatalf("starting the %s container: %v", readerImage, err)
	}
	// sleep as PID 1 ignores SIGTERM, so a graceful stop would only wait
	// out Docker's ten-second grace period. Nothing inside needs one.
	t.Cleanup(func() { _ = c.Terminate(context.Background(), testcontainers.StopTimeout(0)) })

	install := []string{"apk", "add", "--no-cache"}
	for _, p := range packages {
		pinned, ok := readerPackages[p]
		if !ok {
			t.Fatalf("%s has no pinned version in readerPackages", p)
		}
		install = append(install, pinned)
	}
	if code, out := runCode(t, c, install...); code != 0 {
		t.Fatalf("installing %v in %s failed (exit %d); this step needs network access to Alpine's package repositories:\n%s",
			packages, readerImage, code, out)
	}
	return c
}

// runCode runs cmd in c and returns its exit code and combined output.
func runCode(t *testing.T, c testcontainers.Container, cmd ...string) (int, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	code, r, err := c.Exec(ctx, cmd, tcexec.Multiplexed())
	if err != nil {
		t.Fatalf("running %q: %v", cmd, err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading the output of %q: %v", cmd, err)
	}
	return code, string(out)
}

// run runs cmd in c, fails the test on a non-zero exit, and returns the
// combined output.
func run(t *testing.T, c testcontainers.Container, cmd ...string) string {
	t.Helper()
	code, out := runCode(t, c, cmd...)
	if code != 0 {
		t.Fatalf("%q exited %d:\n%s", cmd, code, out)
	}
	return out
}

// expectFiles checks that dir in c holds exactly the files in want, by
// name, and that each one's bytes match.
func expectFiles(t *testing.T, c testcontainers.Container, dir string, want map[string][]byte) {
	t.Helper()
	got := strings.Fields(run(t, c, "ls", "-A1", dir))
	names := make([]string, 0, len(want))
	for name := range want {
		names = append(names, name)
	}
	slices.Sort(got)
	slices.Sort(names)
	if !slices.Equal(got, names) {
		t.Fatalf("%s holds %q, want %q", dir, got, names)
	}
	for name, data := range want {
		rc, err := c.CopyFileFromContainer(context.Background(), dir+"/"+name)
		if err != nil {
			t.Fatalf("copying %s/%s out of the container: %v", dir, name, err)
		}
		body, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("reading %s/%s: %v", dir, name, err)
		}
		if !bytes.Equal(body, data) {
			t.Errorf("%s/%s: %d bytes that differ from the %d written", dir, name, len(body), len(data))
		}
	}
}

// seedContents maps each seed file's Joliet name to its contents.
func seedContents(files []iso9660.File) map[string][]byte {
	m := make(map[string][]byte, len(files))
	for _, f := range files {
		m[f.Name] = f.Data
	}
	return m
}

// quotedName matches the quoted file name at the end of an xorriso -lsl
// line for a regular file.
var quotedName = regexp.MustCompile(`(?m)^-\S+\s.*'([^']+)'$`)

// xorrisoComplaint matches any xorriso message more severe than NOTE,
// UPDATE or HINT: a structural problem it noticed while loading the tree.
var xorrisoComplaint = regexp.MustCompile(`xorriso : (WARNING|SORRY|MISHAP|FAILURE|FATAL|ABORT) :`)

// xorrisoNames returns the regular file names an xorriso -lsl listing shows.
func xorrisoNames(out string) []string {
	var names []string
	for _, m := range quotedName.FindAllStringSubmatch(out, -1) {
		names = append(names, m[1])
	}
	slices.Sort(names)
	return names
}

// TestIndependentReadersExtractTheSeed lists and extracts the seed image
// with xorriso and bsdtar, and checks each shows the Joliet names exactly
// and returns every file byte for byte. xorriso also lists the primary
// tree, checking the derived ISO 9660 identifiers, and must load the
// image without a single warning.
func TestIndependentReadersExtractTheSeed(t *testing.T) {
	files := seedFiles(t)
	want := seedContents(files)
	c := startReader(t, writeImage(t, seedLabel, files), false, "xorriso", "libarchive-tools")
	t.Logf("readers: %s", strings.TrimSpace(run(t, c, "sh", "-c", "xorriso -version 2>&1 | head -1; bsdtar --version")))

	jolietNames := make([]string, 0, len(want))
	for name := range want {
		jolietNames = append(jolietNames, name)
	}
	slices.Sort(jolietNames)

	t.Run("xorriso", func(t *testing.T) {
		out := run(t, c, "xorriso", "-read_fs", "norock", "-indev", seedPath, "-pvd_info", "-lsl", "/")
		t.Logf("xorriso, Joliet tree:\n%s", out)
		if xorrisoComplaint.MatchString(out) {
			t.Errorf("xorriso complained while loading the image")
		}
		if got := xorrisoNames(out); !slices.Equal(got, jolietNames) {
			t.Errorf("Joliet tree lists %q, want %q", got, jolietNames)
		}
		if !regexp.MustCompile(`Volume Id\s*:\s*CIDATA\b`).MatchString(out) {
			t.Errorf("xorriso -pvd_info does not show the primary volume identifier CIDATA")
		}

		out = run(t, c, "xorriso", "-read_fs", "ecma119", "-ecma119_map", "unmapped", "-indev", seedPath, "-lsl", "/")
		t.Logf("xorriso, primary tree:\n%s", out)
		var primary []string
		for _, id := range seedPrimaryIDs {
			primary = append(primary, id)
		}
		slices.Sort(primary)
		if got := xorrisoNames(out); !slices.Equal(got, primary) {
			t.Errorf("primary tree lists %q, want %q", got, primary)
		}

		run(t, c, "xorriso", "-osirrox", "on", "-read_fs", "norock", "-indev", seedPath, "-extract", "/", "/out/xorriso")
		expectFiles(t, c, "/out/xorriso", want)
	})

	t.Run("bsdtar", func(t *testing.T) {
		out := run(t, c, "bsdtar", "-tf", seedPath)
		t.Logf("bsdtar -tf:\n%s", out)
		got := slices.DeleteFunc(strings.Fields(out), func(s string) bool { return s == "." })
		slices.Sort(got)
		if !slices.Equal(got, jolietNames) {
			t.Errorf("bsdtar lists %q, want %q", got, jolietNames)
		}
		run(t, c, "mkdir", "-p", "/out/bsdtar")
		run(t, c, "bsdtar", "-xf", seedPath, "-C", "/out/bsdtar")
		expectFiles(t, c, "/out/bsdtar", want)
	})
}
