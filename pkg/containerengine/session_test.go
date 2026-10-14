// Tests for a session's opening checks: hijacked requests pass the guard, the
// API version is negotiated against the floor, and the account is compared.
package containerengine

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// pipeEngine is a dialer to an in-memory engine: every connection is one
// request, answered by answer, and the requests are counted, so a test can
// tell a request the guard stopped from one that never came through this
// dialer at all.
type pipeEngine struct {
	dials  atomic.Int32
	answer func(req *http.Request) string
}

func (p *pipeEngine) dial(context.Context) (net.Conn, error) {
	p.dials.Add(1)
	c, s := net.Pipe()
	go func() {
		defer s.Close()
		req, err := http.ReadRequest(bufio.NewReader(s))
		if err != nil {
			return
		}
		// The whole request first: a pipe holds nothing, so answering while
		// the client is still writing a body leaves each waiting on the other.
		_, _ = io.Copy(io.Discard, req.Body)
		_, _ = s.Write([]byte(p.answer(req)))
	}()
	return c, nil
}

// pingAnswer answers a ping with apiVersion, or with no version header at
// all when apiVersion is empty.
func pingAnswer(apiVersion string) func(*http.Request) string {
	return func(req *http.Request) string {
		header := ""
		if apiVersion != "" {
			header = "API-Version: " + apiVersion + "\r\n"
		}
		return "HTTP/1.1 200 OK\r\n" + header + "Content-Length: 2\r\n\r\nOK"
	}
}

// TestSession_HijackedRequestsPassTheGuard holds the fix to newAPI's
// transport. A hijacked request (an exec start, an attach) is dialed by the
// client itself rather than through the transport, and with a transport left
// to its defaults it went to the placeholder host, past the guard. So a
// session that did not declare the exec start must have it refused BY THE
// GUARD, through this session's own dialer, and a session that did must
// reach the engine through it.
func TestSession_HijackedRequestsPassTheGuard(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	undeclared := &pipeEngine{answer: func(*http.Request) string { return "HTTP/1.1 500 Unexpected\r\nContent-Length: 0\r\n\r\n" }}
	api, err := newAPI(guard(undeclared.dial, []Op{OpExecCreate, OpExecInspect}))
	if err != nil {
		t.Fatalf("newAPI: %v", err)
	}
	defer api.Close()
	_, err = api.ExecAttach(ctx, "abc123", client.ExecAttachOptions{})
	if !errors.Is(err, errRefused) {
		t.Fatalf("an undeclared exec start answered %v, want the guard's refusal (anything else means it did not pass the guard)", err)
	}
	if undeclared.dials.Load() == 0 {
		t.Fatal("the hijacked request never came through the session's dialer")
	}

	var started atomic.Bool
	declared := &pipeEngine{answer: func(req *http.Request) string {
		if strings.HasSuffix(req.URL.Path, "/exec/abc123/start") {
			started.Store(true)
		}
		return "HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.multiplexed-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n"
	}}
	api2, err := newAPI(guard(declared.dial, ExecOps))
	if err != nil {
		t.Fatalf("newAPI: %v", err)
	}
	defer api2.Close()
	attached, err := api2.ExecAttach(ctx, "abc123", client.ExecAttachOptions{})
	if err != nil {
		t.Fatalf("a declared exec start: %v", err)
	}
	attached.Close()
	if !started.Load() {
		t.Error("the declared exec start did not reach the engine through the session's dialer")
	}
}

// TestNegotiate_FixesTheEnginesVersion proves a session speaks the engine's
// own API version when it is older than the client's newest, and the
// client's newest when the engine is newer.
func TestNegotiate_FixesTheEnginesVersion(t *testing.T) {
	for _, tc := range []struct{ engine, want string }{
		{"1.41", "1.41"},
		{"1.44", "1.44"},
		{"9.99", client.MaxAPIVersion},
	} {
		engine := &pipeEngine{answer: pingAnswer(tc.engine)}
		api, err := newAPI(guard(engine.dial, nil))
		if err != nil {
			t.Fatalf("newAPI: %v", err)
		}
		got, err := negotiate(context.Background(), api, capability.EnginePodman, "test")
		_ = api.Close()
		if err != nil || got != tc.want {
			t.Errorf("engine at %s: negotiate = %q, %v; want %q", tc.engine, got, err, tc.want)
		}
	}
}

// TestNegotiate_RefusesBelowTheFloor proves an engine older than its floor
// is refused naming its version, the floor and what to upgrade to, both
// above the client's own minimum (1.40) and below it (1.30), where the
// client's own refusal would otherwise be the message.
func TestNegotiate_RefusesBelowTheFloor(t *testing.T) {
	for _, version := range []string{"1.40", "1.30"} {
		for _, kind := range capability.Engines() {
			engine := &pipeEngine{answer: pingAnswer(version)}
			api, err := newAPI(guard(engine.dial, nil))
			if err != nil {
				t.Fatalf("newAPI: %v", err)
			}
			_, err = negotiate(context.Background(), api, kind, "old")
			_ = api.Close()
			if err == nil {
				t.Errorf("%s at API %s was accepted", kind, version)
				continue
			}
			for _, want := range []string{version, apiFloor[kind], upgradeHint[kind]} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%s at API %s: refusal %q does not name %q", kind, version, err, want)
				}
			}
		}
	}
}

// TestNegotiate_RefusesAnEngineThatNamesNoVersion proves silence is not read
// as the newest version, which is what the client itself would assume.
func TestNegotiate_RefusesAnEngineThatNamesNoVersion(t *testing.T) {
	engine := &pipeEngine{answer: pingAnswer("")}
	api, err := newAPI(guard(engine.dial, nil))
	if err != nil {
		t.Fatalf("newAPI: %v", err)
	}
	defer api.Close()
	if _, err := negotiate(context.Background(), api, capability.EngineDocker, "x"); err == nil || !strings.Contains(err.Error(), "named no Engine API version") {
		t.Fatalf("negotiate = %v, want a refusal for an engine that names no version", err)
	}
}

// TestSameAccount covers the account check's three answers: the same
// account passes, another is refused naming both and re-onboarding, and a
// record with none is refused naming onboarding.
func TestSameAccount(t *testing.T) {
	if err := sameAccount("m", "d", capability.EngineDocker, "deploy", "deploy"); err != nil {
		t.Errorf("the same account was refused: %v", err)
	}
	err := sameAccount("m", "d", capability.EngineDocker, "deploy", "root")
	if err == nil || !strings.Contains(err.Error(), `"deploy"`) || !strings.Contains(err.Error(), `"root"`) || !strings.Contains(err.Error(), "pleiades onboard d") {
		t.Errorf("another account: %v", err)
	}
	if err := sameAccount("m", "d", capability.EnginePodman, "", "deploy"); err == nil || !strings.Contains(err.Error(), "pleiades onboard d") {
		t.Errorf("a record with no account: %v", err)
	}
}

// TestValidName holds the shapes a name may and may not take before it is
// put into a request path.
func TestValidName(t *testing.T) {
	for _, ok := range []string{"web", "web-1", "web_1.blue", "0abc", strings.Repeat("a", 64)} {
		if err := ValidName("container", ok); err != nil {
			t.Errorf("ValidName(%q) = %v, want accepted", ok, err)
		}
	}
	for _, bad := range []string{"", "-web", ".web", "web/x", "web?x", "web#x", "web%2F", "web x", "web\x00", "../x", "_web"} {
		if err := ValidName("container", bad); err == nil {
			t.Errorf("ValidName(%q) accepted a name that could reshape a path", bad)
		}
	}
	if err := ValidName("container", "web/x"); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("%q", "web/x")) {
		t.Errorf("the refusal does not name the value: %v", err)
	}
}
