// Package yang: net.yang.schema against real servers, the notconf image the
// NETCONF suites pin: Netopeer2 answering get-schema on 830 and rousette
// serving RESTCONF on 80, over one sysrepo, which installs every module under
// /yang-modules when it starts. Two modules are installed beside its own: the
// probe (words a configuration masker takes for secrets) and one holding a
// value the run knows as a secret. Each saved source is held, byte for byte,
// to what rousette's /yang/ endpoint serves for it, read by this test's own
// HTTP client: the same server's text, reached by a path the method never
// takes, so nothing the method does to a source can hide.
package yang

import (
	"context"
	"fmt"
	"io"
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
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// liveImage is the notconf image pkg/netconf's conformance suite pins by
// digest, for the reason it gives there.
const liveImage = "ghcr.io/notconf/notconf@sha256:9ef5677e35d535ca81852d40135236e603526f4380547bc13ffc69ede1b6d03d"

// The image's own fixed test login ("echo admin:admin | chpasswd" in its
// Dockerfile): a throwaway container, never a device. Its password is shorter
// than a source is masked for, which the task says.
const (
	liveUser     = "admin"
	livePassword = "admin"
)

// knownInSchema is a value the run knows as a secret, written into a module's
// description, so a source holding one is masked.
const knownInSchema = "pleiades-known-secret-0123456789"

// secretModule holds knownInSchema.
const secretModule = `module pleiades-secret {
  yang-version 1.1;
  namespace "urn:pleiades:secret";
  prefix ps;
  revision 2026-11-16 {
    description "Holds a value the run knows as a secret: ` + knownInSchema + `.";
  }
  leaf note { type string; }
}
`

// One server for every live test here.
var (
	liveOnce      sync.Once
	liveErr       error
	liveContainer testcontainers.Container
	liveHost      string
	liveNETCONF   int
	liveWeb       int
)

// TestMain stops the shared server once every test has run.
func TestMain(m *testing.M) {
	code := m.Run()
	if liveContainer != nil {
		_ = testcontainers.TerminateContainer(liveContainer)
	}
	os.Exit(code)
}

// startLive starts the shared server once.
func startLive(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("starts a container")
	}
	liveOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image:        liveImage,
				ExposedPorts: []string{"80/tcp", "830/tcp"},
				Files: []testcontainers.ContainerFile{
					{Reader: strings.NewReader(probeSource), ContainerFilePath: "/yang-modules/pleiades-probe.yang", FileMode: 0o644},
					{Reader: strings.NewReader(secretModule), ContainerFilePath: "/yang-modules/pleiades-secret.yang", FileMode: 0o644},
				},
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
			liveErr = fmt.Errorf("starting %s: %w", liveImage, err)
			return
		}
		liveContainer = c
		if liveHost, err = c.Host(ctx); err != nil {
			liveErr = err
			return
		}
		for port, into := range map[string]*int{"830/tcp": &liveNETCONF, "80/tcp": &liveWeb} {
			mapped, err := c.MappedPort(ctx, port)
			if err != nil {
				liveErr = err
				return
			}
			*into = int(mapped.Num())
		}
	})
	if liveErr != nil {
		if os.Getenv("CI") == "" {
			t.Skipf("could not start the NETCONF server (is Docker running?): %v", liveErr)
		}
		t.Fatal(liveErr)
	}
}

// liveDevice is the server as a device serving NETCONF and RESTCONF, the
// latter over the image's plain HTTP proxy, which needs its credential sent
// there accepted.
type liveDevice struct{ *netconfDevice }

// RestconfBaseURL implements capability.RestconfCapable.
func (liveDevice) RestconfBaseURL() string {
	return "http://" + net.JoinHostPort(liveHost, strconv.Itoa(liveWeb))
}

// RestconfAuth implements capability.RestconfCapable.
func (liveDevice) RestconfAuth() string { return httpapi.AuthBasic }

// RestconfAllowPlaintextCredentials implements capability.RestconfCapable.
func (liveDevice) RestconfAllowPlaintextCredentials() bool { return true }

// live returns the server as a device and a fresh context holding its login.
func live(t *testing.T) (liveDevice, *testContext) {
	t.Helper()
	startLive(t)
	rc := newContext(t)
	rc.secrets = map[string]string{wire.SecretUsername: liveUser, wire.SecretPassword: livePassword}
	return liveDevice{&netconfDevice{Stub: &inventorytest.Stub{StubID: "nc1", StubName: "nc1", Caps: bothKinds}, host: liveHost, port: liveNETCONF}}, rc
}

// served is the source rousette serves for key at /yang/, read by this
// test's own client.
func served(t *testing.T, key string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "http://"+net.JoinHostPort(liveHost, strconv.Itoa(liveWeb))+"/yang/"+key+".yang", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.SetBasicAuth(liveUser, livePassword)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("rousette's /yang/%s.yang: %d, %v", key, resp.StatusCode, err)
	}
	return string(body)
}

// TestLive_NETCONFSavesEverySourceAsTheServerSendsIt fetches every module the
// library lists over NETCONF and holds each file to the server's own text:
// the probe's password leaf, key statement, secret words, numbers and address
// all there, nothing masked.
func TestLive_NETCONFSavesEverySourceAsTheServerSendsIt(t *testing.T) {
	dev, rc := live(t)
	if _, err := Schema(context.Background(), rc, dev, map[string]any{"insecure_skip_host_key_verify": true}); err != nil {
		t.Fatal(err)
	}
	records := rc.records(t)
	if len(records) < 50 || rc.stats["via"] != viaNETCONF {
		t.Fatalf("%d sources over %v, want Netopeer2's whole library over netconf", len(records), rc.stats["via"])
	}
	for _, r := range records {
		key := r["name"].(string) + "@" + r["revision"].(string)
		got, ok := rc.read(t, r["file"].(string))
		if !ok || got != served(t, key) {
			t.Errorf("%s: the saved source is not the server's byte for byte", key)
		}
		if strings.Contains(got, netcfg.MaskPrefix) || r["masked"] != false {
			t.Errorf("%s holds a placeholder", key)
		}
	}
	probe, _ := rc.read(t, "yang/nc1/pleiades-probe@2026-11-16.yang")
	for _, word := range []string{"leaf password", `key "name"`, "a secret and 10.1.2.3", "the secret key 100", "$1$abcd$efghijklmnopqrstuv", "leaf key"} {
		if !strings.Contains(probe, word) {
			t.Errorf("the saved probe lacks %q:\n%s", word, probe)
		}
	}
	warnings, _ := rc.stats["warnings"].([]string)
	joined := strings.Join(warnings, "\n")
	if !strings.Contains(joined, "host key was not verified") || !strings.Contains(joined, "shorter than 8 bytes") {
		t.Errorf("warnings = %v, want the unverified host key and the short password said", warnings)
	}
}

// TestLive_NETCONFMasksAKnownSecretInASource replaces the value the run knows
// wherever the server's text holds it, which the server's own text proves it
// does.
func TestLive_NETCONFMasksAKnownSecretInASource(t *testing.T) {
	dev, rc := live(t)
	rc.known = []string{knownInSchema}
	if _, err := Schema(context.Background(), rc, dev, map[string]any{paramModules: []any{"pleiades-secret"}, "insecure_skip_host_key_verify": true}); err != nil {
		t.Fatal(err)
	}
	raw := served(t, "pleiades-secret@2026-11-16")
	got, _ := rc.read(t, "yang/nc1/pleiades-secret@2026-11-16.yang")
	if !strings.Contains(raw, knownInSchema) || strings.Contains(got, knownInSchema) || got != strings.ReplaceAll(raw, knownInSchema, placeholder) {
		t.Errorf("saved\n%s\nwant the server's text with the known value masked and nothing else changed", got)
	}
}

// TestLive_CheckWritesNothing fetches every source a run would and writes
// none.
func TestLive_CheckWritesNothing(t *testing.T) {
	dev, rc := live(t)
	res, err := Check(context.Background(), rc, dev, map[string]any{paramModules: []any{"pleiades-probe"}, "insecure_skip_host_key_verify": true})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rc.read(t, "yang/nc1/pleiades-probe@2026-11-16.yang"); ok || !res.Changed || rc.writes != 0 {
		t.Errorf("check: %v, %d writes; want a predicted change and no file", res, rc.writes)
	}
}

// TestLive_RESTCONFRousetteNamesNoSourceAndNETCONFIsNotTried asks for
// RESTCONF on a device serving both: rousette names no source's URL, so the
// task fails naming that, and does not go on over the NETCONF that would have
// served it.
func TestLive_RESTCONFRousetteNamesNoSourceAndNETCONFIsNotTried(t *testing.T) {
	dev, rc := live(t)
	_, err := Schema(context.Background(), rc, dev, map[string]any{paramModules: []any{"pleiades-probe"}, paramVia: viaRESTCONF})
	if err == nil || !strings.Contains(err.Error(), "over restconf") || !strings.Contains(err.Error(), "names no location for pleiades-probe@2026-11-16's source") {
		t.Fatalf("RESTCONF against rousette: %v, want the refusal naming no location", err)
	}
	if rc.writes != 0 {
		t.Errorf("%d files written", rc.writes)
	}
}
