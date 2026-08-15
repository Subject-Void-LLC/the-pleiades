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
// The rule these constants encode, stated precisely enough for a test to
// check it: every tag names at least the major AND minor number of the
// upstream release it pins, never `latest`, never a bare major, and the
// pin lives here rather than at the call site. `latest` is not a version.
// It is "whatever the registry published before CI pulled it," which
// makes a green build unreproducible tomorrow and lets an upstream
// release turn CI red on a commit that changed nothing.
//
// The "major and minor" half of that rule is not pedantry, and it was
// added after `postgres:15-alpine` sat here for a while under a package
// doc that already claimed every image was pinned exactly. That tag is
// not a pin. It names a major only, resolved to 15.19 on the day it was
// caught, and would have moved every test and the compose stack to 15.20
// on the day upstream published it, with no commit and no warning.
// TestPinsNameAnExactVersion enforces the rule now, so the doc and the
// constants cannot disagree again.
//
// Be honest about the ceiling of that rule, because "exact" is doing less
// work here than it sounds like. A tag such as `postgres:15.19-alpine`
// cannot move to a different PostgreSQL release, which is the failure
// worth stopping, but the registry can still republish it over a newer
// Alpine base, so the bytes are not frozen. Only a `@sha256:` digest
// freezes bytes. That was considered and not taken: a digest has to be
// re-resolved by hand for every base rebuild, including security
// rebuilds, and a stale digest is a test suite pinned to an image with
// known CVEs. Version tags plus this guard buy the reproducibility that
// matters at a maintenance cost somebody will actually pay.
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
//
// The `-alpine` suffix is load bearing and is not a style choice. It
// carries the identical nats-server v2.14.4 binary, so nothing about
// JetStream behavior changes, but it is the only published variant of
// that version that contains a program able to probe the server. The
// default image is built on scratch and holds two files, /nats-server
// and /nats-server.conf, so a Docker healthcheck in it can execute
// nothing at all. docker-compose.yml needs that healthcheck, because
// its controller and runner services gate on the broker with
// condition: service_healthy. Dropping the suffix here would either
// break TestComposeImagesMatchPins or, if someone "fixed" that by
// dropping it in both places, deadlock the compose stack. The full
// reasoning, including the two alternatives that lost, is written out
// beside the nats service in docker-compose.yml.
const NATSImage = "nats:2.14.4-alpine"

// NATSCommand returns the argument list docker-compose.yml passes to the
// NATS server, so a developer tool that starts its own broker starts the
// deployment's broker rather than one that merely shares its version.
//
// It returns a fresh slice on every call rather than exporting a package
// level variable, because an exported slice is writable by any caller and
// a pin nobody can rely on is not a pin.
//
// Every flag has to be stated, and none of them is decoration. A
// `command:` in compose (and a trailing argument list in `docker run`)
// REPLACES the image's whole default command, which is
// ["--config", "nats-server.conf"], so a flag the shipped config file used
// to set is simply not set once any command is given.
//
//   - -js turns JetStream on, which is the only reason this repository
//     runs NATS at all.
//   - -m 8222 opens the monitoring port. The shipped config file was the
//     only thing setting monitor_port, so `-js` alone ran a server with
//     JetStream and no monitoring port, and every probe of :8222 was
//     refused.
//   - -sd /data moves the JetStream store off the default, which the
//     server itself warns about: "Temporary storage directory used, data
//     could be lost on system reboot". docker-compose.yml mounts a named
//     volume there. tools/uidev does not, and does not want to: it runs
//     the container with --rm against a throwaway database, so the store
//     is discarded with everything else. The flag still belongs in the
//     shared list, because what it changes is where the server writes,
//     which is a property of the broker rather than of the deployment.
//
// The full reasoning for each, including the routes that lost, is written
// out beside the nats service in docker-compose.yml.
//
// TestComposeCommandMatchesPin asserts this list and that file's
// `command:` agree, for the same reason TestComposeImagesMatchPins exists
// for the image: the two copies are unavoidable, so the link between them
// has to be a test.
//
// The container-backed tests do not call this. They start NATS through
// testcontainers' own NATS module, which supplies its own arguments and
// probes readiness on the client port, so there is nothing for them to
// keep in step here. The caller this exists for is tools/uidev.
func NATSCommand() []string {
	return []string{"-js", "-sd", "/data", "-m", "8222"}
}

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
// run against, and the version docker-compose.yml runs. The two were
// already consistent with each other before this package existed; it is
// centralized here so it stays that way.
//
// The minor number is new and is the point. This read `postgres:15-alpine`
// until the pin rule above was written down as a test, and that tag was
// consistent between the test suite and the deployment while being a pin
// of neither: it selected whatever 15.x the registry had most recently
// built. `docker run --entrypoint postgres postgres:15-alpine --version`
// answered 15.19 the day this was changed, and would have answered 15.20
// the day upstream shipped it, moving the advisory-lock tests, the
// end-to-end suite, the migration generator and the compose stack all at
// once with no commit to point at. Naming 15.19 costs one deliberate edit
// per upgrade and buys a diff that says which release the change was.
//
// The `-alpine` suffix here carries none of the weight it carries on
// NATSImage above. Nothing probes this container from inside; both the
// compose healthcheck and the tests reach it through pg_isready or a real
// connection, so the suffix is only a smaller download.
const PostgresImage = "postgres:15.19-alpine"
