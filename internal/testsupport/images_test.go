package testsupport_test

import (
	"os"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// composeFile is the deployment descriptor this package's constants must
// agree with, relative to this package's own directory.
const composeFile = "../../docker-compose.yml"

// compose is the sliver of docker-compose.yml this test reads: service
// names to image references. Everything else in the file (ports,
// healthchecks, build stanzas) is deliberately not modeled, so this test
// keeps passing when unrelated deployment details change.
type compose struct {
	Services map[string]struct {
		Image string `yaml:"image"`
	} `yaml:"services"`
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
func TestComposeImagesMatchPins(t *testing.T) {
	raw, err := os.ReadFile(filepath.Clean(composeFile))
	if err != nil {
		t.Fatalf("reading %s: %v", composeFile, err)
	}

	var c compose
	if err := yaml.Unmarshal(raw, &c); err != nil {
		t.Fatalf("parsing %s: %v", composeFile, err)
	}

	want := map[string]string{
		"nats":     testsupport.NATSImage,
		"postgres": testsupport.PostgresImage,
	}

	for service, wantImage := range want {
		svc, ok := c.Services[service]
		if !ok {
			t.Errorf("docker-compose.yml has no %q service; if it was renamed, update internal/testsupport to match", service)
			continue
		}
		if svc.Image != wantImage {
			t.Errorf("docker-compose.yml runs %s for service %q, but the tests run %s.\n"+
				"These must be the same version: a test that proves something about a version the deployment does not run proves nothing about the deployment.\n"+
				"Change both, in internal/testsupport/images.go and docker-compose.yml.",
				svc.Image, service, wantImage)
		}
	}
}

// TestPinsAreNotFloatingTags fails on any constant reintroducing a
// mutable tag. `latest` (and a bare, tagless reference, which Docker
// resolves to `latest`) makes a green build unreproducible the next day
// and lets an upstream release turn CI red with no code change, which is
// the specific failure this package was created to end.
func TestPinsAreNotFloatingTags(t *testing.T) {
	pins := map[string]string{
		"NATSImage":     testsupport.NATSImage,
		"SSHDImage":     testsupport.SSHDImage,
		"PostgresImage": testsupport.PostgresImage,
	}

	for name, ref := range pins {
		// Split on the final colon: an image reference may carry a
		// registry host with its own port (lscr.io/... does not, but a
		// private registry would), so only the last segment is the tag.
		idx := lastColonAfterSlash(ref)
		if idx < 0 {
			t.Errorf("%s = %q has no tag at all, which Docker resolves as :latest", name, ref)
			continue
		}
		if tag := ref[idx+1:]; tag == "latest" || tag == "" {
			t.Errorf("%s = %q uses a floating tag; pin an exact version", name, ref)
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
