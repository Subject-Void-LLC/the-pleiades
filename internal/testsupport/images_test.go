// This file is the enforcement half of internal/testsupport. The
// constants next door are only a single source of truth if something
// checks the copies that cannot import them, and checks that each one
// says what the package doc claims it says.
//
// Three separate claims are tested here, because they fail in three
// different ways:
//
//   - TestComposeImagesMatchPins: docker-compose.yml runs the images the
//     tests run. It walks every service in the file, not a list of
//     services this test already knows, so a service added there is a
//     test failure until somebody accounts for it.
//   - TestComposeCommandMatchesPin: the broker is started with the same
//     flags. The image agreeing while the flags differ is a real state
//     this repository was in, and it is invisible to an image check.
//   - TestComposeNeverPullsWhatItBuilds and
//     TestBackupImageMatchesTheServer: a locally built image is never
//     fetched from a registry under its local name, and the backup image's
//     PostgreSQL release is the server's.
//   - TestPinsNameAnExactVersion and TestExactVersionRule: every pin
//     names a version that cannot move on its own. The second of those
//     tests the rule itself against tags known to be good and bad, so
//     the guard is proven to fail before it is trusted to pass.
package testsupport_test

import (
	"regexp"
	"strings"
	"testing"

	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// composeFile is the deployment descriptor this package's constants must
// agree with, relative to this package's own directory.
const composeFile = "../../docker-compose.yml"

// compose is the sliver of docker-compose.yml these tests read. Ports,
// healthchecks, environment and depends_on are deliberately not modeled,
// so these tests keep passing when unrelated deployment details change.
type compose struct {
	Services map[string]composeService `yaml:"services"`
}

// composeService is one service's build inputs.
//
// Build and Command are yaml.Node rather than concrete types on purpose.
// Compose accepts more than one shape for each (build as a path string or
// as a mapping, command as a shell string or an exec list), and decoding
// straight into a Go type would turn a legal-but-different shape into an
// error about the whole file. Holding the raw node lets each test decode
// only what it needs and say something useful when the shape is not the
// one it can check.
type composeService struct {
	Image      string    `yaml:"image"`
	Build      yaml.Node `yaml:"build"`
	Command    yaml.Node `yaml:"command"`
	PullPolicy string    `yaml:"pull_policy"`
}

// hasBuild reports whether the service builds from a local Dockerfile
// instead of pulling a published image. A yaml.Node that was never
// decoded has Kind 0, which is how an absent key is told from a present
// one here.
func (s composeService) hasBuild() bool { return s.Build.Kind != 0 }

// pinnedImages maps a compose service name to the constant that must
// match it. It is the whole set of services this repository expects to
// PULL a published image; anything else in the file has to build from a
// Dockerfile or be added here deliberately.
//
// The services that build are deliberately absent even though they name an
// image. Theirs is a local build tag (pleiades/controller:dev), which
// compose applies to the image it builds from the Dockerfile, and those are
// pinned by their Dockerfile's own FROM line, at a digest, which is a
// stronger pin than anything expressible here. That compose builds rather
// than pulls them is not something a build: key promises, though: see
// TestComposeNeverPullsWhatItBuilds.
func pinnedImages() map[string]string {
	return map[string]string{
		"nats":     testsupport.NATSImage,
		"postgres": testsupport.PostgresImage,
	}
}

// readCompose parses docker-compose.yml or fails the test.
func readCompose(tb testing.TB) compose {
	tb.Helper()

	raw, err := os.ReadFile(filepath.Clean(composeFile))
	if err != nil {
		tb.Fatalf("reading %s: %v", composeFile, err)
	}

	var c compose
	if err := yaml.Unmarshal(raw, &c); err != nil {
		tb.Fatalf("parsing %s: %v", composeFile, err)
	}
	if len(c.Services) == 0 {
		tb.Fatalf("%s declares no services; either the file moved or its shape changed and this guard is now checking nothing", composeFile)
	}
	return c
}

// TestComposeImagesMatchPins is the guard that makes internal/testsupport
// a single source of truth rather than merely a tidier place to keep one
// of two copies.
//
// docker-compose.yml cannot import a Go constant, so the image strings
// necessarily exist twice. Without this test, nothing connects them: the
// NATS image had already drifted to three versions across the test suite
// while the deployment ran a fourth (`latest`), which meant every
// container-backed test in this repository was proving something about a
// NATS release no user would run. Asserting the two agree costs one test
// and converts that entire failure mode into a build failure.
//
// It checks in both directions, and the second direction is the one that
// was missing. An earlier version walked its own two-entry table and
// never looked at the parsed file, so it could only ever notice a change
// to a service it already knew about. A service ADDED to compose with an
// image, which is exactly how a new dependency arrives, was unguarded and
// silently so. Now every service in the file has to be either pinned here
// or built from a local Dockerfile, and a new one fails until someone
// decides which it is.
func TestComposeImagesMatchPins(t *testing.T) {
	c := readCompose(t)
	want := pinnedImages()

	// Direction one: every service the file declares is accounted for.
	for service, svc := range c.Services {
		if svc.hasBuild() {
			// Built here, not pulled. Its `image:` is the tag compose
			// applies to what it builds, so there is no upstream version
			// to keep in step; the Dockerfile's FROM line is the pin, and
			// both of this repository's pin their base at a digest.
			continue
		}
		if svc.Image == "" {
			// Neither pulled nor built is not a service compose can start
			// at all, so it is far more likely a typo or a bad merge than
			// a deliberate entry.
			t.Errorf("docker-compose.yml service %q names neither an `image:` nor a `build:`; compose cannot start it, so this is almost certainly a typo or a bad merge", service)
			continue
		}

		wantImage, ok := want[service]
		if !ok {
			t.Errorf("docker-compose.yml service %q pulls %s, which internal/testsupport does not pin.\n"+
				"Every published image this stack pulls is pinned in one place so a test and the deployment cannot drift apart.\n"+
				"Add a constant to internal/testsupport/images.go and an entry to pinnedImages() in this file, or make the service build from a Dockerfile.",
				service, svc.Image)
			continue
		}
		if svc.Image != wantImage {
			t.Errorf("docker-compose.yml runs %s for service %q, but the tests run %s.\n"+
				"These must be the same version: a test that proves something about a version the deployment does not run proves nothing about the deployment.\n"+
				"Change both, in internal/testsupport/images.go and docker-compose.yml.",
				svc.Image, service, wantImage)
		}
	}

	// Direction two: every pin still has a service to pin, and that
	// service still pulls rather than builds. A renamed or deleted
	// service would otherwise leave a constant nothing checks, and a
	// `build:` added to one would silently exempt it from direction one.
	for service := range want {
		svc, ok := c.Services[service]
		if !ok {
			t.Errorf("docker-compose.yml has no %q service; if it was renamed, update internal/testsupport and pinnedImages() to match", service)
			continue
		}
		if svc.hasBuild() {
			t.Errorf("docker-compose.yml service %q now builds from a Dockerfile, so internal/testsupport's pin for it is checking nothing.\n"+
				"Remove it from pinnedImages() in this file, and from internal/testsupport/images.go if no test starts that image either.", service)
		}
	}
}

// TestComposeCommandMatchesPin asserts the NATS server is started with
// the same flags in the deployment and in the one tool that starts its
// own broker.
//
// The image agreeing while the flags differ is not hypothetical. This
// stack passed `-js` alone for a long time, which replaced the image's
// default command and therefore switched off the monitoring port the
// healthcheck needs, and tools/uidev kept passing `-js` alone after the
// stack was fixed. An image-only check cannot see either. See
// internal/testsupport.NATSCommand for what the flags do and why both are
// required.
func TestComposeCommandMatchesPin(t *testing.T) {
	c := readCompose(t)

	svc, ok := c.Services["nats"]
	if !ok {
		t.Fatalf("docker-compose.yml has no %q service; if it was renamed, update internal/testsupport and this test to match", "nats")
	}
	if svc.Command.Kind == 0 {
		t.Fatalf("docker-compose.yml service %q states no `command:`.\n"+
			"That is not the same as agreeing with internal/testsupport.NATSCommand: with no command the image runs its own default, which is the config file, and internal/testsupport.NATSCommand documents why this stack cannot use it.", "nats")
	}

	var got []string
	if err := svc.Command.Decode(&got); err != nil {
		t.Fatalf("docker-compose.yml service %q states a `command:` this guard cannot read: %v\n"+
			"Write it in exec form, as a YAML list of strings. Compose's string form is shell form and means something different (it runs through /bin/sh), which this stack's scratch-derived images cannot do anyway.", "nats", err)
	}

	want := testsupport.NATSCommand()
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("docker-compose.yml starts nats with %q, but internal/testsupport.NATSCommand says %q.\n"+
			"These must be the same: `make ui-dev` starts its own broker from that function, so a difference here means the UI is developed against a broker configured unlike the deployed one.\n"+
			"Change both, in internal/testsupport/images.go and docker-compose.yml.",
			got, want)
	}
}

// gettingStartedDoc is the one user-facing document that tells a reader
// to start a broker by hand, relative to this package's own directory.
const gettingStartedDoc = "../../docs/02-get-started.md"

// TestGettingStartedRunsThePinnedBroker asserts the quickstart starts the
// broker this repository pins, with the flags it pins.
//
// A document is a third copy of the image string, and it drifted exactly
// like the other two did. While docker-compose.yml moved to
// nats:2.14.4-alpine with three flags, this page still said nats:2.14.4
// with one, so a reader following it end to end was standing up a
// genuinely different image from the one the same repository deploys, and
// then reading output produced against it. Prose cannot import a
// constant, so this is the same shape of guard TestComposeImagesMatchPins
// is, aimed at a Markdown file.
//
// What it does not check: that the surrounding prose is accurate. It
// checks the one line a reader copies.
func TestGettingStartedRunsThePinnedBroker(t *testing.T) {
	raw, err := os.ReadFile(filepath.Clean(gettingStartedDoc))
	if err != nil {
		t.Fatalf("reading %s: %v", gettingStartedDoc, err)
	}

	wantCommand := strings.Join(testsupport.NATSCommand(), " ")
	found := 0

	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		// Only the copy-and-paste line matters, and it is the one that
		// runs a container from an image whose name starts with `nats`.
		if !strings.HasPrefix(line, "docker run ") || !strings.Contains(line, "nats") {
			continue
		}
		found++

		fields := strings.Fields(line)
		image := -1
		for i, f := range fields {
			if f == testsupport.NATSImage {
				image = i
				break
			}
		}
		if image < 0 {
			t.Errorf("%s tells the reader to run a broker that is not the pinned one:\n"+
				"    %s\n"+
				"internal/testsupport pins %s. A quickstart on a different image documents a stack nobody deploys.",
				gettingStartedDoc, line, testsupport.NATSImage)
			continue
		}

		// Everything after the image reference is the container's own
		// command, which is what the flags are.
		got := strings.Join(fields[image+1:], " ")
		if got != wantCommand {
			t.Errorf("%s starts the pinned broker with %q, but internal/testsupport.NATSCommand says %q.\n"+
				"Line:\n    %s\n"+
				"The image agreeing while the flags differ still gives the reader a differently configured server; see internal/testsupport.NATSCommand for what each flag does.",
				gettingStartedDoc, got, wantCommand, line)
		}
	}

	if found == 0 {
		t.Fatalf("found no `docker run` line naming a nats image in %s.\n"+
			"Either the quickstart stopped starting a broker by hand, in which case delete this test, or its shape changed and this guard is now asserting nothing.", gettingStartedDoc)
	}
}

// versionNumber matches a dotted release number: two or more numeric
// components separated by dots, such as 15.19, 2.14.4 or 10.3.
//
// Two components is the floor rather than three because that is what the
// upstreams actually publish. PostgreSQL has no third component (15.19 is
// a complete release identity), and LinuxServer's openssh tags carry the
// upstream's own 10.3_p1-r0 shape. Demanding three would have forced a
// tag that does not exist.
var versionNumber = regexp.MustCompile(`[0-9]+(\.[0-9]+)+`)

// rejectPin returns the reason an image reference is not an acceptable
// pin, or the empty string when it is fine.
//
// The rule, stated once here so the constants' package doc and this test
// cannot drift: the reference must carry a tag, the tag must not be
// `latest`, and the tag must contain a release number with at least a
// major AND a minor component. That last clause is the one that matters
// in practice. `latest` is obvious and nobody types it twice, but
// `postgres:15-alpine` looks pinned, reads as pinned in review, and moves
// to 15.20 on its own.
//
// Its known limit, written down rather than hidden: the rule looks for a
// version number anywhere in the tag, so a tag naming some OTHER
// component's version, such as a hypothetical `postgres:alpine3.22`,
// would satisfy it while leaving the PostgreSQL version floating.
// Anchoring the number to the front of the tag was tried and rejected: it
// rejects `version-10.3_p1-r0`, a genuinely immutable tag this repository
// depends on. The rule catches the mistake people make and does not
// pretend to catch every mistake possible.
func rejectPin(ref string) string {
	idx := lastColonAfterSlash(ref)
	if idx < 0 {
		return "has no tag at all, which Docker resolves as :latest"
	}

	tag := ref[idx+1:]
	switch {
	case tag == "":
		return "has an empty tag, which Docker resolves as :latest"
	case tag == "latest":
		return "uses the `latest` tag, which is whatever the registry published most recently"
	case !versionNumber.MatchString(tag):
		return "has a tag naming no release number at all, so nothing stops the registry moving it"
	}
	return ""
}

// TestPinsNameAnExactVersion fails on any constant that does not name a
// version specific enough to stay still.
//
// This replaces a check that only rejected the literal tag `latest` and
// the empty tag. That check passed `postgres:15-alpine` for as long as it
// existed, and would equally have passed `nats:2` or `golang:1`, while
// the package doc beside those constants claimed every image was pinned
// to an exact version. A guard that cannot catch the thing its own
// documentation promises is worse than none, because it is read as
// evidence.
func TestPinsNameAnExactVersion(t *testing.T) {
	pins := map[string]string{
		"NATSImage":       testsupport.NATSImage,
		"SSHDImage":       testsupport.SSHDImage,
		"PostgresImage":   testsupport.PostgresImage,
		"LocalStackImage": testsupport.LocalStackImage,
		"ToxiproxyImage":  testsupport.ToxiproxyImage,
		"NginxImage":      testsupport.NginxImage,
	}

	for name, ref := range pins {
		if reason := rejectPin(ref); reason != "" {
			t.Errorf("%s = %q %s.\n"+
				"Pin a tag naming at least the major and minor version of the release, so an upstream release cannot move this repository without a commit.\n"+
				"`docker run --rm --entrypoint <program> %s --version` tells you which release the current tag resolves to today.",
				name, ref, reason, ref)
		}
	}
}

// TestExactVersionRule is the negative control for TestPinsNameAnExactVersion,
// and it is here because a guard nobody has watched fail is not a guard.
//
// The rows below include the exact tag that slipped through the previous
// version of this check (`postgres:15-alpine`) and the two shapes named
// alongside it (`nats:2`, `golang:1`), so this test would have caught
// that regression on the day it landed. The registry-with-a-port rows
// exist because the tag is found by scanning backwards for a colon, and a
// private registry's port colon is the one thing that can be mistaken for
// a tag separator.
func TestExactVersionRule(t *testing.T) {
	tests := []struct {
		ref    string
		reject bool
	}{
		// The real pins, which must stay acceptable. This list said
		// "the three real pins" while listing three of four, and is now
		// four of five; the map above is the authority, and this table
		// exists to prove rejectPin's judgement rather than to enumerate.
		{"nats:2.14.4-alpine", false},
		{"postgres:15.19-alpine", false},
		{"lscr.io/linuxserver/openssh-server:version-10.3_p1-r0", false},
		{"ghcr.io/shopify/toxiproxy:2.12.0", false},

		// A bare major is the failure this rule was added for.
		{"postgres:15-alpine", true},
		{"postgres:15", true},
		{"nats:2", true},
		{"golang:1", true},

		// The failures the previous rule already caught, kept so a
		// rewrite cannot lose them.
		{"nats:latest", true},
		{"nats:", true},
		{"nats", true},

		// A registry host's port is not a tag.
		{"registry.example.com:5000/nats", true},
		{"registry.example.com:5000/nats:2.14.4", false},

		// A three-component tag with no suffix, the most ordinary
		// acceptable shape there is.
		{"nats:2.14.4", false},
	}

	for _, tc := range tests {
		reason := rejectPin(tc.ref)
		switch {
		case tc.reject && reason == "":
			t.Errorf("rejectPin(%q) accepted a floating reference; this rule exists to reject it", tc.ref)
		case !tc.reject && reason != "":
			t.Errorf("rejectPin(%q) rejected a real pin (%s); the rule is now stricter than the tags upstream actually publishes", tc.ref, reason)
		}
	}
}

// lastColonAfterSlash returns the index of the tag separator in an image
// reference, or -1 when the reference carries no tag. A colon before the
// final slash belongs to a registry host's port, not to a tag.
func lastColonAfterSlash(ref string) int {
	lastSlash := -1
	for i := 0; i < len(ref); i++ {
		if ref[i] == '/' {
			lastSlash = i
		}
	}
	for i := len(ref) - 1; i > lastSlash; i-- {
		if ref[i] == ':' {
			return i
		}
	}
	return -1
}

// TestComposeNeverPullsWhatItBuilds requires `pull_policy: never` on every
// service that builds its image.
//
// A build: key does not stop compose pulling. Measured on Compose v5.5.1:
// for a service with both image: and build:, `docker compose up` or `run`
// with no local image first PULLS the image: name from its registry, and
// builds only when the pull fails. pleiades/controller:dev therefore means
// docker.io/pleiades/controller:dev on a fresh clone, and the "pleiades"
// namespace on Docker Hub belongs to somebody else. An image published there
// under that name would be run as the controller, with the master key and
// the database. `never` makes compose build a missing image instead, and
// use a present one without rebuilding.
func TestComposeNeverPullsWhatItBuilds(t *testing.T) {
	c := readCompose(t)
	built := 0
	for service, svc := range c.Services {
		if !svc.hasBuild() {
			continue
		}
		built++
		if svc.PullPolicy != "never" {
			t.Errorf("docker-compose.yml service %q builds %s but sets pull_policy %q.\n"+
				"Without `pull_policy: never`, compose pulls that name from a registry before building it, and this project does not own the Docker Hub namespace it is in.",
				service, svc.Image, svc.PullPolicy)
		}
	}
	if built < 4 {
		t.Fatalf("found %d services that build; the controller, runner, setup and backup services all do, so the file was not read the way this test assumes", built)
	}
}

// TestBackupImageMatchesTheServer holds the backup image's PostgreSQL
// release to the server's. pg_dump refuses a server newer than itself, and a
// backup written by the release that will read it back is the one nobody has
// to reason about.
func TestBackupImageMatchesTheServer(t *testing.T) {
	raw, err := os.ReadFile(filepath.Clean("../../Dockerfile.controller"))
	if err != nil {
		t.Fatalf("reading Dockerfile.controller: %v", err)
	}
	m := regexp.MustCompile(`(?m)^FROM postgres:([0-9.]+)-bookworm@sha256:[0-9a-f]{64} AS backup$`).FindSubmatch(raw)
	if m == nil {
		t.Fatal("Dockerfile.controller has no `FROM postgres:<version>-bookworm@sha256:<digest> AS backup` stage")
	}
	server := strings.TrimSuffix(strings.TrimPrefix(testsupport.PostgresImage, "postgres:"), "-alpine")
	if string(m[1]) != server {
		t.Fatalf("the backup image is PostgreSQL %s and the server is %s; change both together", m[1], server)
	}
}
