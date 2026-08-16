//go:build integration

// Phase 20's Release Gate, image half.
//
// Two claims about the artifacts themselves, both asserted against images
// built from the committed Dockerfiles rather than against the Dockerfiles
// themselves. That distinction is the whole reason this file exists:
// reading `USER 65532:65532` out of a Dockerfile proves the line is
// written, and reading it out of a built image proves it survived every
// later stage, every base image change and every build argument.
//
//   - Both images run as an unprivileged numeric UID and hold no shell.
//   - The build context those images are built from is a small fraction of
//     the repository, which is what keeps .git, .SPECIFICATION/, .AGENTS/
//     and .claude/ out of an artifact that ships.
//
// internal/testsupport/dockerignore_test.go guards the same second claim
// from the other side, by reading .dockerignore and checking its rules.
// The two are complementary rather than redundant: that test proves the
// file says the right thing, this one proves docker agreed.
package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// packagingContextCeiling is the largest the build context may be in
// absolute terms, and it is the stable half of this assertion.
//
// 32 MiB against a measured 11.5 MiB. Sized to fail instantly on the
// regression that matters: this repository's `.git` alone is about 61 MiB
// and its agent working directory has held 120 MiB, so either of them
// entering the context blows straight past this, while roughly three times
// the current source tree is left as headroom for legitimate growth.
const packagingContextCeiling = 32 << 20

// packagingContextBudget is the largest share of the working tree the
// build context may be, and it is the WEAKER half of this assertion. The
// comment is longer than the constant because the number is easy to
// misread as tighter evidence than it is.
//
// The denominator is whatever the developer's working tree happens to
// hold, which is not a property of the packaging at all. On the machine
// this was written on the tree is 384 MiB (61 MiB of `.git`, 120 MiB of
// `.claude/`, 31 MiB of `.IGNORE/`, about 17 MiB of source) and the
// context measures 2.99 percent of it. On a freshly cloned checkout with
// none of those working directories the same context is closer to 15
// percent, having changed by nothing. So the ratio is bounded loosely, at
// a level a fresh clone passes with room, and the sharp assertions are the
// absolute ceiling above and the forbidden-path check below.
const packagingContextBudget = 0.33

// packagingForbiddenContextPaths are top-level directories whose presence
// in a build context is the defect this gate exists to prevent, each with
// the reason it matters.
//
// The reasons are the same ones internal/testsupport/dockerignore_test.go
// records, kept short here because that file owns the long form.
var packagingForbiddenContextPaths = map[string]string{
	".SPECIFICATION": "gitignored specification documents that never ship",
	".AGENTS":        "gitignored project rules that never ship",
	".claude":        "the agent working directory, which has held a whole second checkout",
	".git":           "roughly 60 MB of version history that is never a build input",
}

// TestPackagingReleaseGate_ImagesRunUnprivilegedWithNoShell asserts the
// two hardening properties of the shipped images.
func TestPackagingReleaseGate_ImagesRunUnprivilegedWithNoShell(t *testing.T) {
	requireDockerDaemon(t)
	root := ensurePleiadesImages(t)

	for _, image := range []string{packagingControllerImage, packagingRunnerImage} {
		t.Run(image, func(t *testing.T) {
			assertImageRunsAsUnprivilegedUID(t, root, image)
			assertImageHasNoShell(t, image)
		})
	}
}

// assertImageRunsAsUnprivilegedUID reads the user the image will start as
// out of its own configuration.
//
// The value has to be numeric. Kubernetes cannot enforce runAsNonRoot
// against a USER that is a name, because the kubelet cannot resolve a name
// inside an image it has not started yet, and it answers with
// CreateContainerConfigError rather than running the container as root, so
// a named USER turns a security control into an outage.
func assertImageRunsAsUnprivilegedUID(t *testing.T, root, image string) {
	t.Helper()

	out := mustRunPackagingTool(t, root, nil, "",
		"docker", "image", "inspect", "--format", "{{.Config.User}}", image)
	user := strings.TrimSpace(out)
	if user != packagingImageUID {
		t.Errorf("%s runs as %q, want %q", image, user, packagingImageUID)
	}
}

// assertImageHasNoShell proves there is nothing in the image to run but
// the one binary it was built for.
//
// Two assertions, and the pair is stronger than either alone. The first
// says no file anywhere is named like a shell, which catches a shell
// dropped somewhere unusual. The second says the four directories a shell
// or a utility would normally live in hold no ordinary file at all, which
// catches a package manager, a busybox applet set under another name, or a
// debugging tool somebody added "just for now".
//
// If a legitimate change ever puts a file in one of those directories,
// this test is the right place to argue for it: the reason it fails is the
// reason the base image is distroless.
func assertImageHasNoShell(t *testing.T, image string) {
	t.Helper()

	entries := exportImageEntries(t, image)
	if len(entries) == 0 {
		t.Fatalf("%s exported an empty filesystem, so nothing below proves anything", image)
	}

	for _, entry := range entries {
		base := filepath.Base(entry.Name)
		if packagingShellNames[base] {
			t.Errorf("%s contains %q, which is a shell; this image holds the master encryption key "+
				"and the JWT signing secret, so an interactive shell in it is worth more to an "+
				"attacker than one anywhere else in the mesh", image, entry.Name)
		}
		if !entry.Regular {
			continue
		}
		for _, dir := range packagingBinDirs {
			if strings.HasPrefix(entry.Name, dir) {
				t.Errorf("%s holds an executable at %q; the distroless base leaves %s empty, "+
					"so something added it", image, entry.Name, dir)
			}
		}
	}
	// Guarded, because an unconditional summary line saying "no shell"
	// directly under a list of shells it found is the kind of log that gets
	// believed instead of read.
	if !t.Failed() {
		t.Logf("%s: %d paths, no shell, %v all empty", image, len(entries), packagingBinDirs)
	}
}

// TestPackagingReleaseGate_BuildContextIsASmallFractionOfTheTree measures
// what docker is actually given when it builds these images.
//
// The measurement is a real build. A scratch image whose only instruction
// copies the whole context is exactly the filtered context and nothing
// else, so exporting it yields the byte-for-byte answer. Two weaker routes
// were rejected: parsing "transferring context: N MB" out of BuildKit's
// progress output reports the DELTA against its local context cache, which
// on a second build is a few kilobytes and means nothing; and
// reimplementing .dockerignore's matching rules in Go would prove this
// test agrees with itself.
func TestPackagingReleaseGate_BuildContextIsASmallFractionOfTheTree(t *testing.T) {
	requireDockerDaemon(t)
	// Not ensurePleiadesImages: building the shipped images is not a
	// precondition of measuring the context they would be built from, and
	// making it one would couple two independent tests.
	root := testsupport.RepoRoot(t)

	const probeImage = "pleiades/build-context-probe:release-gate"
	// CMD is set so `docker create` accepts the image without an
	// --entrypoint override. The path does not exist and is never run;
	// nothing here starts the container.
	probeDockerfile := "FROM scratch\nCOPY . /\nCMD [\"/nonexistent-by-design\"]\n"

	build := packagingCommand(t, root, nil, "docker", "build", "-f-", "-t", probeImage, ".")
	build.Stdin = strings.NewReader(probeDockerfile)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the build-context probe image: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		if out, err := runPackagingTool(t, root, nil, "", "docker", "image", "rm", probeImage); err != nil {
			t.Logf("removing the probe image: %v\n%s", err, out)
		}
	})

	entries := exportImageEntries(t, probeImage)

	var contextBytes int64
	var files int
	seen := map[string]bool{}
	for _, entry := range entries {
		// .dockerenv is created by docker inside every container it makes,
		// so it is part of the export and not part of the context.
		if entry.Name == ".dockerenv" {
			continue
		}
		seen[topLevel(entry.Name)] = true
		if entry.Regular {
			contextBytes += entry.Size
			files++
		}
	}

	for forbidden, why := range packagingForbiddenContextPaths {
		if seen[forbidden] {
			t.Errorf("the build context contains %s, which must never enter an image: %s", forbidden, why)
		}
	}

	// Everything the two Go builds reach has to still be there, because
	// "deny more" is only correct while the build still works. A context
	// that excluded internal/ would pass every check above and fail every
	// build.
	for _, required := range []string{"cmd", "internal", "pkg", "go.mod", "go.sum"} {
		if !seen[required] {
			t.Errorf("the build context is missing %s, which `go build ./cmd/...` needs", required)
		}
	}

	tree := treeBytes(t, root)
	if tree == 0 {
		t.Fatal("the repository measured as zero bytes, so the ratio below is meaningless")
	}
	share := float64(contextBytes) / float64(tree)
	t.Logf("build context: %s across %d files, against %s of working tree (%.2f%%)",
		humanBytes(contextBytes), files, humanBytes(tree), share*100)

	if contextBytes > packagingContextCeiling {
		t.Errorf("the build context is %s, over the %s ceiling; check .dockerignore for a rule "+
			"that was removed or narrowed", humanBytes(contextBytes), humanBytes(packagingContextCeiling))
	}
	if share > packagingContextBudget {
		t.Errorf("the build context is %.2f%% of the working tree, over the %.0f%% budget; "+
			"check .dockerignore for a rule that was removed or narrowed",
			share*100, packagingContextBudget*100)
	}
}
