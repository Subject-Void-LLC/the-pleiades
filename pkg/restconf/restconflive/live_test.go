// Package restconflive: the RESTCONF client against rousette, and the same
// server's NETCONF listener, so what one protocol writes or reports is held
// to what the other reads.
package restconflive

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
)

// notconfImage is the image pkg/netconf's conformance suite and the CLI's
// NETCONF gate pin, by digest. Its run.sh starts rousette behind nghttpx on
// port 80 (plain HTTP; /restconf/, /yang/, /streams/ and /.well-known/ are
// proxied) beside netopeer2-server on 830, both over one sysrepo datastore.
const notconfImage = "ghcr.io/notconf/notconf@sha256:9ef5677e35d535ca81852d40135236e603526f4380547bc13ffc69ede1b6d03d"

// The image's own fixed test login (its Dockerfile's "echo admin:admin |
// chpasswd"), which rousette checks through PAM and netopeer2 over SSH: a
// throwaway container's, never a device's.
const (
	notconfUser     = "admin"
	notconfPassword = "admin"
)

// server is one started notconf container's RESTCONF origin and NETCONF
// address.
type server struct {
	origin      string
	netconfHost string
	netconfPort int
}

// One container serves every test here, as pkg/netconf's conformance suite
// shares its own: startup takes seconds, and each test only reads or reverts
// what it wrote.
var (
	shared     server
	sharedOnce sync.Once
	sharedErr  error
	sharedC    testcontainers.Container
)

// TestMain stops the shared container once every test has finished.
func TestMain(m *testing.M) {
	code := m.Run()
	if sharedC != nil {
		_ = testcontainers.TerminateContainer(sharedC)
	}
	os.Exit(code)
}

// start runs the image once and waits for both listeners.
func start(t *testing.T) server {
	t.Helper()
	if testing.Short() {
		t.Skip("starts a container")
	}
	sharedOnce.Do(func() { shared, sharedC, sharedErr = run() })
	if sharedErr != nil {
		t.Fatal(sharedErr)
	}
	return shared
}

// run starts the container and reads its endpoints.
func run() (server, testcontainers.Container, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        notconfImage,
			ExposedPorts: []string{"80/tcp", "830/tcp"},
			WaitingFor: wait.ForAll(
				wait.ForLog("Listening on :::830 for SSH connections").WithStartupTimeout(2*time.Minute),
				testsupport.SSHGreeting("830/tcp"),
				wait.ForHTTP("/.well-known/host-meta").WithPort("80/tcp").WithStartupTimeout(2*time.Minute),
			),
		},
		Started: true,
	})
	if err != nil {
		_ = testcontainers.TerminateContainer(c)
		return server{}, nil, fmt.Errorf("starting %s: %w", notconfImage, err)
	}
	host, err := c.Host(ctx)
	if err != nil {
		return server{}, c, err
	}
	web, err := c.MappedPort(ctx, "80/tcp")
	if err != nil {
		return server{}, c, err
	}
	nc, err := c.MappedPort(ctx, "830/tcp")
	if err != nil {
		return server{}, c, err
	}
	return server{origin: "http://" + net.JoinHostPort(host, strconv.Itoa(int(web.Num()))), netconfHost: host, netconfPort: int(nc.Num())}, c, nil
}

// plainDevice is a RESTCONF device at an http:// origin that accepts sending
// its credential there, as rousette behind the image's plain proxy needs.
type plainDevice struct{ origin string }

// RestconfBaseURL implements capability.RestconfCapable.
func (d plainDevice) RestconfBaseURL() string { return d.origin }

// RestconfAuth implements capability.RestconfCapable.
func (plainDevice) RestconfAuth() string { return httpapi.AuthBasic }

// RestconfAllowPlaintextCredentials implements capability.RestconfCapable.
func (plainDevice) RestconfAllowPlaintextCredentials() bool { return true }

// client is a RESTCONF client of s, its root found.
func client(t *testing.T, s server) *restconf.Client {
	t.Helper()
	c, _, err := restconf.ClientFor(plainDevice{origin: s.origin}, "notconf", map[string]string{wire.SecretUsername: notconfUser, wire.SecretPassword: notconfPassword})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// session is a NETCONF session to s.
func session(t *testing.T, s server) *netconf.Session {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	conn, err := remoteexec.New(remoteexec.Options{InsecureSkipHostKeyVerify: true}).Connect(ctx, nil,
		remoteexec.Target{Host: s.netconfHost, Port: s.netconfPort}, remoteexec.PasswordAuth(notconfUser, notconfPassword))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	sub, err := conn.Subsystem(ctx, "netconf")
	if err != nil {
		t.Fatal(err)
	}
	ns, err := netconf.Open(ctx, sub, netconf.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ns.Close(context.Background()) })
	return ns
}

// TestLive_DiscoversARealServerAndAgreesWithItsNETCONF reads rousette's root,
// capabilities and YANG library, and holds the module set to the one the same
// server's NETCONF session discovers: one library, one digest, whichever
// protocol read it.
func TestLive_DiscoversARealServerAndAgreesWithItsNETCONF(t *testing.T) {
	s := start(t)
	c := client(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	d, err := c.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if d.Root != "/restconf" {
		t.Errorf("Root = %q, want the /restconf/ rousette names, without its slash", d.Root)
	}
	if d.CapabilitiesError != "" || !containsString(d.Protocol, "urn:ietf:params:restconf:capability:defaults:1.0?basic-mode=explicit") {
		t.Errorf("Protocol = %v (%q), want rousette's capabilities, read from inside restconf-state", d.Protocol, d.CapabilitiesError)
	}
	if d.Completeness != yanglib.Complete || d.Source != yanglib.SourceLibrary11 || len(d.Modules) == 0 || d.Dropped != 0 {
		t.Fatalf("Discovery = %s from %s, %d modules, %d dropped (%v); want a complete RFC 8525 library", d.Completeness, d.Source, len(d.Modules), d.Dropped, d.DroppedSamples)
	}
	if got := strings.Join(d.Datastores, ","); got != "candidate,operational,running,startup" {
		t.Errorf("Datastores = %q, want sysrepo's NMDA set", got)
	}
	if d.ContentID == "" {
		t.Error("ContentID is empty, want the library's content-id")
	}
	if c.Verified() {
		t.Error("Verified = true over the image's plain HTTP proxy")
	}
	over := session(t, s).Discovery()
	if over.Completeness != yanglib.Complete || over.ModulesDigest() != d.ModulesDigest() {
		t.Errorf("NETCONF read %d modules (%s, digest %s), RESTCONF %d (digest %s); want one module set", len(over.Modules), over.Completeness, over.ModulesDigest(), len(d.Modules), d.ModulesDigest())
	}
}

// TestLive_AKeyHoldingASlashAndACommaAddressesItsOwnEntry writes a NACM group
// named "ops/team,1" over NETCONF and reads it back over RESTCONF through the
// path builder: the entry comes back, where the same name pasted into a path
// raw is read by this server as a node named "team,1" under it.
func TestLive_AKeyHoldingASlashAndACommaAddressesItsOwnEntry(t *testing.T) {
	s := start(t)
	ns := session(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	const name = "ops/team,1"
	group := `<nacm xmlns="urn:ietf:params:xml:ns:yang:ietf-netconf-acm"><groups><group><name>` + name + `</name><user-name>alice</user-name></group></groups></nacm>`
	if err := ns.EditConfig(ctx, netconf.EditConfigRequest{Config: group}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Bounded on its own context: a cleanup must not hang the run on
		// a server that stopped answering.
		cleanupCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		remove := `<nacm xmlns="urn:ietf:params:xml:ns:yang:ietf-netconf-acm" xmlns:nc="urn:ietf:params:xml:ns:netconf:base:1.0"><groups><group nc:operation="delete"><name>` + name + `</name></group></groups></nacm>`
		_ = ns.EditConfig(cleanupCtx, netconf.EditConfigRequest{Config: remove})
	})
	c := client(t, s)
	if _, err := c.DiscoverRoot(ctx); err != nil {
		t.Fatal(err)
	}
	entry := restconf.Path{restconf.Node("ietf-netconf-acm", "nacm"), restconf.Node("", "groups"), restconf.Node("", "group").Entry(name)}
	body, _, err := c.GetData(ctx, entry)
	if err != nil {
		t.Fatalf("GetData = %v, want the entry the key names", err)
	}
	if !strings.Contains(string(body), "<name>"+name+"</name>") || !strings.Contains(string(body), "alice") {
		t.Errorf("the answer is %s, want the group %q", body, name)
	}
	missing := restconf.Path{restconf.Node("ietf-netconf-acm", "nacm"), restconf.Node("", "groups"), restconf.Node("", "group").Entry("ops/other")}
	_, _, err = c.GetData(ctx, missing)
	var status *restconf.StatusError
	if !errors.As(err, &status) || status.Status != http.StatusNotFound {
		t.Errorf("GetData of an absent entry = %v, want the server's 404 for that one entry", err)
	}
}

// TestLive_ARefusedCredentialIsNamed: rousette answers a wrong password with
// 401 and access-denied, which the client reports as the credential refused.
func TestLive_ARefusedCredentialIsNamed(t *testing.T) {
	s := start(t)
	c, _, err := restconf.ClientFor(plainDevice{origin: s.origin}, "notconf", map[string]string{wire.SecretUsername: notconfUser, wire.SecretPassword: "not-the-password"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_, err = c.Discover(ctx)
	var refused *restconf.RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("Discover = %v, want a *RefusedError", err)
	}
	if err == nil || !strings.Contains(err.Error(), "refused the credential") || !strings.Contains(err.Error(), "access-denied") {
		t.Fatalf("Discover = %v, want the credential refused with the server's access-denied", err)
	}
}

// containsString reports whether list holds s.
func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
