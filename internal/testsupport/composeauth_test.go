// The guard on the mesh authentication overlay.
//
// docker-compose.mesh-auth.yml turns the compose stack's broker into an
// authenticating one. It is a separate file rather than a flag in the
// base compose file because that file's `command:` is pinned byte for
// byte by TestComposeCommandMatchesPin, compose cannot vary a list on a
// condition, and a fresh clone has to be able to `docker compose up` with
// no generated file present.
//
// That split has a cost this file exists to pay: there are now two
// descriptions of how to start the broker, and the overlay's has to stay
// the base one plus a configuration file. Getting it wrong is silent in
// the worst direction. An overlay that REPLACED the flags rather than
// adding to them would start a broker with no JetStream, which is
// precisely the fixture defect that let five permission bugs ship behind
// a green Release Gate (FAILURE_PATTERNS.md #207).
package testsupport_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"go.yaml.in/yaml/v3"
)

// meshAuthOverlayFile is the overlay, relative to this package.
const meshAuthOverlayFile = "../../docker-compose.mesh-auth.yml"

// TestComposeMeshAuthOverlayIsAdditive asserts the overlay starts the
// broker with the deployment's own flags plus a configuration file, in
// that order.
func TestComposeMeshAuthOverlayIsAdditive(t *testing.T) {
	raw, err := os.ReadFile(filepath.Clean(meshAuthOverlayFile))
	if err != nil {
		t.Fatalf("reading %s: %v", meshAuthOverlayFile, err)
	}

	var c compose
	if err := yaml.Unmarshal(raw, &c); err != nil {
		t.Fatalf("parsing %s: %v", meshAuthOverlayFile, err)
	}

	svc, ok := c.Services["nats"]
	if !ok {
		t.Fatalf("%s has no nats service, so it configures nothing", meshAuthOverlayFile)
	}
	var got []string
	if err := svc.Command.Decode(&got); err != nil {
		t.Fatalf("%s states a command this guard cannot read: %v", meshAuthOverlayFile, err)
	}

	want := append([]string{"-c", testsupport.NATSConfigPath}, testsupport.NATSCommand()...)
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("%s starts nats with %q, want %q.\n"+
			"The overlay must ADD a configuration file to the pinned flag list, never replace it: replacing it starts a broker without JetStream, which is the whole data path, and every test against such a broker passes while proving nothing.",
			meshAuthOverlayFile, got, want)
	}
}

// TestComposeMeshAuthOverlayPinsNoImage keeps the base file the single
// place the broker's version is chosen.
//
// An image key here would be a second pin, invisible to
// TestComposeImagesMatchPins, and the two would drift the first time one
// of them was bumped.
func TestComposeMeshAuthOverlayPinsNoImage(t *testing.T) {
	raw, err := os.ReadFile(filepath.Clean(meshAuthOverlayFile))
	if err != nil {
		t.Fatalf("reading %s: %v", meshAuthOverlayFile, err)
	}
	var c compose
	if err := yaml.Unmarshal(raw, &c); err != nil {
		t.Fatalf("parsing %s: %v", meshAuthOverlayFile, err)
	}
	for name, svc := range c.Services {
		if svc.Image != "" {
			t.Errorf("%s pins an image for service %q (%q); the base compose file is the only place a version is chosen, and a second pin drifts the first time either is bumped",
				meshAuthOverlayFile, name, svc.Image)
		}
	}
}

// TestComposeMeshAuthOverlayAssertsEnforcement is the check on the check.
//
// Under authentication the base file's PING/PONG probe cannot pass: a
// probe would have to sign the server's nonce with an ed25519 key, and
// this image has busybox and nats-server and nothing that can. Measured
// against nats-server 2.14.4: in operator mode the INFO line carries
// "auth_required":true and a bare PING is answered with
// -ERR 'Authorization Violation'; with no authentication the field is
// absent and PING is answered with PONG.
//
// So the overlay's probe has to assert the OPPOSITE of the base one. A
// probe that merely dropped the PONG check would pass against a broker
// that is not enforcing at all, which is the one misconfiguration this
// overlay exists to make impossible.
func TestComposeMeshAuthOverlayAssertsEnforcement(t *testing.T) {
	raw, err := os.ReadFile(filepath.Clean(meshAuthOverlayFile))
	if err != nil {
		t.Fatalf("reading %s: %v", meshAuthOverlayFile, err)
	}
	body := string(raw)

	if !strings.Contains(body, `"auth_required":true`) {
		t.Error("the overlay's healthcheck does not assert the broker is requiring authentication, so it would report a broker that accepts anyone as healthy")
	}
	if strings.Contains(body, "grep -q '^PONG'") {
		t.Error("the overlay's healthcheck still expects PONG, which an authenticating broker never sends to an unauthenticated probe; the stack would never come up")
	}
}
