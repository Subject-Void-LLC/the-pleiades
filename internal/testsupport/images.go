// Package testsupport holds the container image references this
// repository's container-backed tests start, in one place, so that a
// test and the deployment it is supposed to represent cannot silently
// run different versions of the same dependency.
//
// It exists because they already had. Before this package, the NATS
// image was named independently at seventeen call sites across eleven
// packages and had drifted into three different versions at once:
// `nats:2.10` in the Phase 16 SSH mesh Release Gate, `internal/event`,
// `internal/runner` and `internal/topology`; `nats:2.11` in
// `internal/election`, `internal/lock` and both `cmd/controller` gates;
// and `nats:latest` in `tests/e2e` and in `docker-compose.yml`. Since
// `latest` resolves to whatever was published most recently, the
// deployment and the end-to-end test were on a fourth, moving version
// that no Release Gate had ever exercised, and the tests that most
// directly claim "the mesh really works" were validating against a NATS
// two minor versions behind the one a user would actually run.
//
// The rule these constants encode: every image is pinned to an exact
// version, never `latest`, and the pin lives here rather than at the
// call site. `latest` is not a version. It is "whatever the registry
// published before CI pulled it," which makes a green build
// unreproducible tomorrow and lets an upstream release turn CI red on a
// commit that changed nothing.
//
// docker-compose.yml cannot read a Go constant, so it necessarily holds
// a second copy of these strings. TestComposeImagesMatchPins asserts the
// two agree, which turns the drift this package was written to end into
// a build failure rather than a discovery months later.
package testsupport

import "time"

// ContainerStartupTimeout and SSHDStartupTimeout bound how long a test
// waits for a container to report itself ready before giving up.
//
// These are harness patience, not production semantics, and the
// distinction matters: nothing in production boots a fresh broker per
// operation, so there is no production value to mirror here. What they
// guard is a cold image pull on a loaded CI runner. Contrast the values
// that *are* production semantics -- lease TTLs, renewal intervals, ack
// and retry windows -- which tests must run at their real production
// values and must never shrink for speed.
//
// Consistency here means every container names its timeout explicitly
// from a constant, not that every container uses the same number. Before
// this, the sshd containers set three minutes deliberately while every
// NATS container silently inherited testcontainers' 60-second default by
// saying nothing, so the value that applied was invisible at the call
// site and nobody had chosen it.
//
// The two differ because the images differ. NATS is a small image that
// starts in about a second, so two minutes is already generous cover for
// a cold pull under load. The LinuxServer sshd image is much larger and
// performs first-boot user creation before it prints "done.", which is
// why three minutes was picked for it originally and is kept.
//
// Both are also bounded from above by `go test`'s own per-package
// timeout, which the Makefile sets (GO_TEST_TIMEOUT) rather than leaving
// at its 10-minute default. The two are coupled: internal/lock starts
// seven containers in one package, so a package where every start timed
// out would need 7 x ContainerStartupTimeout to report the real error,
// and if the package timeout fired first the clear "container failed to
// start" would be replaced by a goroutine-dump panic that says nothing
// about Docker. Raising either means checking the other.
const (
	ContainerStartupTimeout = 2 * time.Minute
	SSHDStartupTimeout      = 3 * time.Minute
)

// NATSImage is the NATS server every container-backed test starts, and
// the version docker-compose.yml runs. JetStream behavior (per-key TTL,
// consumer and stream config, KV bucket validation) differs between
// minor versions in ways this repository's lock, event, election and
// topology packages depend on directly, which is exactly why a test must
// not silently run a different one from the deployment.
//
// One deliberate exception exists and must stay: internal/lock's own
// TestNatsLockManager bucket-config case pins nats:2.10 inline, because
// that specific version rejects a bucket config the test asserts is
// rejected. That is a version-specific regression test, not drift, and
// it names its version at the call site with a comment saying why.
const NATSImage = "nats:2.14.4"

// SSHDImage is the OpenSSH server the real-transport tests dial: a
// genuinely independent SSH implementation, never a Go in-process fake,
// so a handshake, host-key check or auth failure is the real one.
//
// LinuxServer.io publishes immutable `version-<upstream>` tags alongside
// the mutable `latest` this used to name. The pinned form matters more
// here than for most images, because an sshd release can change host key
// algorithms or default auth methods, which would surface as an
// unexplained transport test failure with no corresponding code change.
const SSHDImage = "lscr.io/linuxserver/openssh-server:version-10.3_p1-r0"

// PostgresImage is the database the advisory-lock and end-to-end tests
// run against, and the version docker-compose.yml runs. This one was
// already consistent between the two before this package existed; it is
// centralized here so it stays that way.
const PostgresImage = "postgres:15-alpine"
