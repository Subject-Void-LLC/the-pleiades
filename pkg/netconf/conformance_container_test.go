package netconf_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.uber.org/goleak"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/datastore"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// notconfImage is pinned by DIGEST rather than by tag, and the reason is
// specific to this image rather than a general preference: notconf
// publishes no semantic version tags at all. Its tag list is "latest",
// "debug", and a long series of opaque CI run identifiers, so a tag pin
// would either float ("latest") or name a build number that says nothing
// about what it contains. A digest is the only pin here that is both
// reproducible and honest.
//
// notconf is BSD-3-Clause and bundles Netopeer2 (CESNET, BSD-3-Clause)
// over sysrepo (BSD-3-Clause), all GPLv3-compatible. It also serves
// RESTCONF through rousette on its own port, which is why this image is
// the right conformance target for the RESTCONF work that follows:
// a write over NETCONF being readable over RESTCONF against ONE
// datastore is a real cross-protocol assertion rather than two
// independent ones.
const notconfImage = "ghcr.io/notconf/notconf@sha256:9ef5677e35d535ca81852d40135236e603526f4380547bc13ffc69ede1b6d03d"

// The image's own fixed test credential, set in its Dockerfile
// ("echo admin:admin | chpasswd", read from the published image's own
// build history rather than guessed). This is a throwaway container
// started fresh for this test run, never a real device, so a literal
// here is not a secret leak -- the same reasoning
// internal/transport/ssh's container tests already record for theirs.
const (
	notconfUser     = "admin"
	notconfPassword = "admin"
)

// One container is shared by every conformance test in this package
// rather than each starting its own, matching
// internal/transport/ssh's own shared-container fixture and for the
// same reason: startup is roughly twelve seconds and nothing here needs
// a pristine server, since each test either only reads or reverts what
// it wrote.
var (
	notconfOnce      sync.Once
	notconfErr       error
	notconfContainer testcontainers.Container
	notconfHost      string
	notconfPort      int

	// notconfGoleak is a goleak.IgnoreCurrent snapshot taken once the
	// container and testcontainers' own supporting goroutines (its
	// reaper) are up, so a test fails only for leaks IT introduced.
	notconfGoleak []goleak.Option
)

// TestMain tears the shared container down once, after every test in
// the package has finished.
func TestMain(m *testing.M) {
	code := m.Run()
	if notconfContainer != nil {
		_ = notconfContainer.Terminate(context.Background())
	}
	os.Exit(code)
}

// startNotconf brings up the shared NETCONF server container, once, and
// returns its endpoint.
//
// This is a real RFC 6241 server implementation, not a fixture written
// against this client's own understanding of the protocol. That is the
// point: pkg/netconf's unit tests drive a scripted fake, which can only
// ever agree with whatever this package believes, while Netopeer2
// disagrees independently. It has already earned that: it rejects an
// unknown module with error-tag "unknown-namespace" and an
// error-message, where a real Cisco IOS XE device rejects an unknown
// element with "unknown-element" and NO error-message at all.
func startNotconf(t *testing.T) (host string, port int) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping the container-backed NETCONF conformance suite in -short mode")
	}

	notconfOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()

		container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image:        notconfImage,
				ExposedPorts: []string{"830/tcp"},
				WaitingFor: wait.ForAll(
					wait.ForLog("Listening on :::830 for SSH connections").
						WithStartupTimeout(2*time.Minute),
					testsupport.SSHGreeting("830/tcp"),
				),
			},
			Started: true,
		})
		if err != nil {
			_ = testcontainers.TerminateContainer(container) // a failed start still returns its container
			notconfErr = fmt.Errorf("starting the NETCONF conformance container: %w", err)
			return
		}
		notconfContainer = container

		h, err := container.Host(ctx)
		if err != nil {
			notconfErr = fmt.Errorf("container host: %w", err)
			return
		}
		mapped, err := container.MappedPort(ctx, "830/tcp")
		if err != nil {
			notconfErr = fmt.Errorf("container port: %w", err)
			return
		}
		notconfHost, notconfPort = h, int(mapped.Num())
		notconfGoleak = []goleak.Option{goleak.IgnoreCurrent()}
	})

	if notconfErr != nil {
		if os.Getenv("CI") == "" {
			t.Skipf("could not start the NETCONF conformance container (is Docker running?): %v", notconfErr)
		}
		t.Fatalf("%v", notconfErr)
	}
	return notconfHost, notconfPort
}

// notconfSession opens one NETCONF session against the shared server.
func notconfSession(t *testing.T, opts netconf.Options) *netconf.Session {
	t.Helper()
	host, port := startNotconf(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	runner := remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true})
	conn, err := runner.Connect(ctx, nil,
		remoteexec.Target{Host: host, Port: port},
		remoteexec.PasswordAuth(notconfUser, notconfPassword))
	if err != nil {
		t.Fatalf("connecting to the conformance container: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	sub, err := conn.Subsystem(ctx, "netconf")
	if err != nil {
		t.Fatalf("opening the netconf subsystem: %v", err)
	}
	t.Cleanup(func() { _ = sub.Close() })

	session, err := netconf.Open(ctx, sub, opts)
	if err != nil {
		t.Fatalf("opening the NETCONF session: %v (stderr: %q)", err, sub.Stderr())
	}
	return session
}

// TestConformance_NegotiatesAgainstARealServer is the baseline: an
// independent RFC 6241 implementation accepted this client's hello and
// this client read its capabilities.
func TestConformance_NegotiatesAgainstARealServer(t *testing.T) {
	s := notconfSession(t, netconf.Options{})

	if s.Framing() != netconf.FramingChunked {
		t.Errorf("Framing() = %v, want chunked: Netopeer2 advertises base:1.1", s.Framing())
	}
	if s.SessionID() == "" {
		t.Error("SessionID() is empty, but RFC 6241 requires the server to assign one")
	}
	if !s.HasCapability(netconf.CapabilityBase11) {
		t.Error("HasCapability(base:1.1) = false against a server that negotiated chunked framing")
	}
	t.Logf("session %s, framing %v, %d capabilities advertised", s.SessionID(), s.Framing(), len(s.Capabilities()))
}

// TestConformance_GetConfigReadsTheRunningDatastore proves this client
// parses a reply an independent server produced, over chunked framing.
func TestConformance_GetConfigReadsTheRunningDatastore(t *testing.T) {
	s := notconfSession(t, netconf.Options{})

	payload, err := s.GetConfig(context.Background(), datastore.Path{})
	if err != nil {
		t.Fatalf("GetConfig() error = %v, want nil", err)
	}
	if payload.Encoding != datastore.EncodingXML {
		t.Errorf("Encoding = %v, want xml", payload.Encoding)
	}
	if len(payload.Bytes) == 0 {
		t.Fatal("GetConfig() returned an empty document; this server's running datastore holds the NETCONF server's own configuration and is never empty")
	}
	t.Logf("running datastore: %d bytes", len(payload.Bytes))
}

// TestConformance_ReportsARealServersRpcError is the other half of the
// error work: pkg/netconf's unit tests decode error shapes captured from
// a Cisco device, and this proves the same decoding holds against a
// completely different vendor's implementation. An error type modelled
// on one vendor's output is a vendor-specific error type.
func TestConformance_ReportsARealServersRpcError(t *testing.T) {
	s := notconfSession(t, netconf.Options{})

	// An element in no schema the server knows.
	err := s.EditConfig(context.Background(), netconf.EditConfigRequest{
		Config: `<pleiades-not-a-real-module xmlns="urn:pleiades:conformance:nonexistent"><x/></pleiades-not-a-real-module>`,
	})
	if err == nil {
		t.Fatal("EditConfig() error = nil, want the server to reject an unknown module")
	}

	var decoded netconf.RPCError
	if !errors.As(err, &decoded) {
		t.Fatalf("EditConfig() error = %v (%T), want a decoded RPCError: an error that did not decode is one an operator cannot act on", err, err)
	}
	if decoded.Tag == "" {
		t.Error("the decoded error carries no error-tag, which is the field a caller branches on")
	}
	t.Logf("server rejected it with tag=%q type=%q message=%q", decoded.Tag, decoded.Type, decoded.Message)

	// Whatever the tag, the rendering must carry something actionable.
	// This is the assertion that would have failed against a device
	// omitting <error-message>, which is exactly what Cisco IOS XE does.
	if len(err.Error()) < len("netconf: rpc-error: ")+3 {
		t.Errorf("error rendered as %q, which tells an operator nothing", err)
	}
}

// nacmPath addresses the one leaf these round-trip tests write:
// ietf-netconf-acm's enable-external-groups, a plain boolean that
// defaults to true. It is chosen because it is harmless to toggle on a
// throwaway container, unlike enable-nacm, which would switch access
// control off for the rest of the session.
var nacmPath = datastore.Path{Elem: []datastore.PathElem{
	{Name: "nacm", Namespace: "urn:ietf:params:xml:ns:yang:ietf-netconf-acm"},
}}

const nacmDisableExternalGroups = `<nacm xmlns="urn:ietf:params:xml:ns:yang:ietf-netconf-acm">` +
	`<enable-external-groups>false</enable-external-groups></nacm>`

const nacmEnableExternalGroups = `<nacm xmlns="urn:ietf:params:xml:ns:yang:ietf-netconf-acm">` +
	`<enable-external-groups>true</enable-external-groups></nacm>`

// TestConformance_EditConfigRoundTripsOnRunning is the write half: a
// change this client sent is a change this client can read back, over
// chunked framing, against an independent server.
func TestConformance_EditConfigRoundTripsOnRunning(t *testing.T) {
	s := notconfSession(t, netconf.Options{})
	ctx := context.Background()

	if !s.HasCapability(netconf.CapabilityWritableRunning) {
		t.Skipf("this server does not advertise %s", netconf.CapabilityWritableRunning)
	}
	// Reverted whatever the test does, so a shared container is left as
	// it was found for whichever conformance test runs next.
	t.Cleanup(func() {
		_ = s.EditConfig(context.Background(), netconf.EditConfigRequest{Config: nacmEnableExternalGroups})
	})

	if err := s.EditConfig(ctx, netconf.EditConfigRequest{Config: nacmDisableExternalGroups}); err != nil {
		t.Fatalf("EditConfig() error = %v, want nil", err)
	}

	got, err := s.GetConfig(ctx, nacmPath)
	if err != nil {
		t.Fatalf("GetConfig() error = %v, want nil", err)
	}
	if !strings.Contains(string(got.Bytes), "<enable-external-groups>false</enable-external-groups>") {
		t.Fatalf("after writing false, the datastore reads %q", got.Bytes)
	}

	if err := s.EditConfig(ctx, netconf.EditConfigRequest{Config: nacmEnableExternalGroups}); err != nil {
		t.Fatalf("EditConfig() reverting: error = %v, want nil", err)
	}
	got, err = s.GetConfig(ctx, nacmPath)
	if err != nil {
		t.Fatalf("GetConfig() error = %v, want nil", err)
	}
	if strings.Contains(string(got.Bytes), "<enable-external-groups>false</enable-external-groups>") {
		t.Fatalf("the revert did not land; the datastore still reads %q", got.Bytes)
	}
}

// TestConformance_SubtreeFilterNarrowsTheReply proves the filter this
// client builds from a datastore.Path is one a real server honors,
// rather than one it ignores while returning everything.
func TestConformance_SubtreeFilterNarrowsTheReply(t *testing.T) {
	s := notconfSession(t, netconf.Options{})
	ctx := context.Background()

	whole, err := s.GetConfig(ctx, datastore.Path{})
	if err != nil {
		t.Fatalf("GetConfig(root) error = %v", err)
	}
	filtered, err := s.GetConfig(ctx, nacmPath)
	if err != nil {
		t.Fatalf("GetConfig(nacm) error = %v", err)
	}

	if len(filtered.Bytes) >= len(whole.Bytes) {
		t.Fatalf("the filtered read returned %d bytes and the whole datastore %d: the server did not narrow the reply, so this client's subtree filter is not being honored",
			len(filtered.Bytes), len(whole.Bytes))
	}
	if !strings.Contains(string(filtered.Bytes), "nacm") {
		t.Errorf("the filtered read returned %q, want the nacm subtree", filtered.Bytes)
	}
}

// TestConformance_TheDatastoreFamilyWorksWhereItIsSupported exercises
// the operations the shared pkg/datastore.Store port deliberately does
// NOT carry: lock, unlock, candidate, commit and discard-changes. They
// cannot be proven against the Cisco sandbox at all, because it offers
// only :writable-running, which is exactly why a second, independent
// conformance target earns its cost.
func TestConformance_TheDatastoreFamilyWorksWhereItIsSupported(t *testing.T) {
	probe := notconfSession(t, netconf.Options{})
	if !probe.HasCapability(netconf.CapabilityCandidate) {
		t.Skipf("this server does not advertise %s", netconf.CapabilityCandidate)
	}

	s := notconfSession(t, netconf.Options{Target: netconf.Candidate})
	ctx := context.Background()

	if err := s.Lock(ctx); err != nil {
		t.Fatalf("Lock() error = %v, want nil", err)
	}
	if err := s.EditConfig(ctx, netconf.EditConfigRequest{Config: nacmDisableExternalGroups}); err != nil {
		t.Fatalf("EditConfig() on candidate: error = %v, want nil", err)
	}

	// The edit is in the candidate datastore and NOT in running: that
	// distinction is the entire reason a candidate datastore exists, and
	// it is what net.netconf.config's commit parameter turns on.
	staged, err := s.GetConfig(ctx, nacmPath)
	if err != nil {
		t.Fatalf("GetConfig(candidate) error = %v", err)
	}
	if !strings.Contains(string(staged.Bytes), "<enable-external-groups>false</enable-external-groups>") {
		t.Fatalf("the candidate datastore does not hold the staged edit: %q", staged.Bytes)
	}
	live, err := probe.GetConfig(ctx, nacmPath)
	if err != nil {
		t.Fatalf("GetConfig(running) error = %v", err)
	}
	if strings.Contains(string(live.Bytes), "<enable-external-groups>false</enable-external-groups>") {
		t.Fatal("the running datastore already holds an uncommitted candidate edit")
	}

	// DiscardChanges must undo it, leaving running untouched.
	if err := s.DiscardChanges(ctx); err != nil {
		t.Fatalf("DiscardChanges() error = %v, want nil", err)
	}
	staged, err = s.GetConfig(ctx, nacmPath)
	if err != nil {
		t.Fatalf("GetConfig(candidate) after discard: error = %v", err)
	}
	if strings.Contains(string(staged.Bytes), "<enable-external-groups>false</enable-external-groups>") {
		t.Fatalf("DiscardChanges left the edit in the candidate datastore: %q", staged.Bytes)
	}

	// And a committed edit must reach running.
	t.Cleanup(func() {
		_ = s.EditConfig(context.Background(), netconf.EditConfigRequest{Config: nacmEnableExternalGroups})
		_ = s.Commit(context.Background())
		_ = s.Unlock(context.Background())
	})
	if err := s.EditConfig(ctx, netconf.EditConfigRequest{Config: nacmDisableExternalGroups}); err != nil {
		t.Fatalf("EditConfig() on candidate: error = %v", err)
	}
	if err := s.Commit(ctx); err != nil {
		t.Fatalf("Commit() error = %v, want nil", err)
	}
	live, err = probe.GetConfig(ctx, nacmPath)
	if err != nil {
		t.Fatalf("GetConfig(running) after commit: error = %v", err)
	}
	if !strings.Contains(string(live.Bytes), "<enable-external-groups>false</enable-external-groups>") {
		t.Fatalf("the committed edit did not reach the running datastore: %q", live.Bytes)
	}
}

// TestConformance_LeavesNoGoroutinesBehind covers the goroutines a
// session and its subsystem channel start. A leak here would be
// per-session rather than per-process, so a Controller opening one
// NETCONF session per task would accumulate them for as long as it ran.
func TestConformance_LeavesNoGoroutinesBehind(t *testing.T) {
	host, port := startNotconf(t)
	ctx := context.Background()

	runner := remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true})
	conn, err := runner.Connect(ctx, nil,
		remoteexec.Target{Host: host, Port: port},
		remoteexec.PasswordAuth(notconfUser, notconfPassword))
	if err != nil {
		t.Fatalf("connecting: %v", err)
	}
	sub, err := conn.Subsystem(ctx, "netconf")
	if err != nil {
		conn.Close()
		t.Fatalf("opening the netconf subsystem: %v", err)
	}
	s, err := netconf.Open(ctx, sub, netconf.Options{})
	if err != nil {
		sub.Close()
		conn.Close()
		t.Fatalf("opening the session: %v", err)
	}
	if _, err := s.GetConfig(ctx, nacmPath); err != nil {
		t.Fatalf("GetConfig(): %v", err)
	}

	// The polite close: <close-session/> and then the stream.
	if err := s.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v, want nil", err)
	}
	_ = sub.Close()
	_ = conn.Close()

	goleak.VerifyNone(t, notconfGoleak...)
}
