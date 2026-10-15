// Tests for Session.RunOnce against an in-memory engine that answers each
// request the way a real one does, recording what the client asked for.
package containerengine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// runID is the container ID the fake engine hands out.
var runID = strings.Repeat("ab", 32)

// runImage is a digest-pinned image reference.
var runImage = "docker.io/library/alpine@sha256:" + strings.Repeat("cd", 32)

// runEngine answers the requests one RunOnce sends.
type runEngine struct {
	imagePresent bool
	containerID  string
	stream       []byte
	exitCode     int
	waitFor      time.Duration

	mu      sync.Mutex
	create  map[string]any
	pulled  int
	created int
	killed  int
	removed int
}

func (e *runEngine) answer(req *http.Request, body []byte) string {
	path := versionedPath.ReplaceAllString(req.URL.Path, "")
	e.mu.Lock()
	defer e.mu.Unlock()
	switch {
	case path == "/_ping":
		return "HTTP/1.1 200 OK\r\nAPI-Version: 1.44\r\nContent-Length: 2\r\n\r\nOK"
	case strings.HasPrefix(path, "/images/") && strings.HasSuffix(path, "/json"):
		if !e.imagePresent {
			return jsonAnswer("404 Not Found", `{"message":"No such image"}`)
		}
		return jsonAnswer("200 OK", `{"Id":"sha256:`+strings.Repeat("ef", 32)+`"}`)
	case path == "/images/create":
		e.pulled++
		e.imagePresent = true
		return jsonAnswer("200 OK", `{"status":"Pulled"}`)
	case path == "/containers/create":
		e.created++
		_ = json.Unmarshal(body, &e.create)
		id := e.containerID
		if id == "" {
			id = runID
		}
		return jsonAnswer("201 Created", fmt.Sprintf(`{"Id":%q,"Warnings":[]}`, id))
	case strings.HasSuffix(path, "/attach"):
		return "HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.multiplexed-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n" + string(e.stream)
	case strings.HasSuffix(path, "/start"):
		return "HTTP/1.1 204 No Content\r\n\r\n"
	case strings.HasSuffix(path, "/wait"):
		e.mu.Unlock()
		time.Sleep(e.waitFor)
		e.mu.Lock()
		return jsonAnswer("200 OK", fmt.Sprintf(`{"StatusCode":%d}`, e.exitCode))
	case strings.HasSuffix(path, "/kill"):
		e.killed++
		return "HTTP/1.1 204 No Content\r\n\r\n"
	case req.Method == http.MethodDelete && strings.HasPrefix(path, "/containers/"):
		e.removed++
		return "HTTP/1.1 204 No Content\r\n\r\n"
	}
	return "HTTP/1.1 418 Unexpected\r\nContent-Length: 0\r\n\r\n"
}

// jsonAnswer is an HTTP answer with a JSON body.
func jsonAnswer(status, body string) string {
	return fmt.Sprintf("HTTP/1.1 %s\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", status, len(body), body)
}

// runSession opens a session on e allowing exactly what RunOnce sends.
func runSession(t *testing.T, e *runEngine) *Session {
	t.Helper()
	api, err := newAPI(guard(e.dial, RunOnceOps))
	if err != nil {
		t.Fatalf("newAPI: %v", err)
	}
	t.Cleanup(func() { _ = api.Close() })
	return &Session{API: api}
}

// dial is pipeEngine's, keeping each request's body for the answer, which
// is where the create's configuration is read.
func (e *runEngine) dial(context.Context) (net.Conn, error) {
	c, s := net.Pipe()
	go func() {
		defer s.Close()
		req, err := http.ReadRequest(bufio.NewReader(s))
		if err != nil {
			return
		}
		body, _ := io.ReadAll(req.Body)
		_, _ = s.Write([]byte(e.answer(req, body)))
	}()
	return c, nil
}

// TestRunOnce_RunsHardenedStreamsAndRemoves is the ordinary run: no
// network, a read-only root with a writable /tmp, no new privileges, both
// streams to the writer in order, the real exit code, and the container
// removed.
func TestRunOnce_RunsHardenedStreamsAndRemoves(t *testing.T) {
	e := &runEngine{imagePresent: true, stream: frames(byte(1), "out\n", byte(2), "err\n"), exitCode: 3}
	var out bytes.Buffer
	res, err := runSession(t, e).RunOnce(context.Background(), RunOnceOptions{
		Image: runImage, Cmd: []string{"/bin/sh", "-c", "make test"}, Timeout: time.Minute, Output: &out,
		Labels: map[string]string{"org.pleiades.gate": "unit"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 3 || res.TimedOut || res.Pulled || out.String() != "out\nerr\n" {
		t.Errorf("RunOnce = %+v, output %q", res, out.String())
	}
	host, _ := e.create["HostConfig"].(map[string]any)
	if host["NetworkMode"] != "none" || host["ReadonlyRootfs"] != true || !strings.Contains(fmt.Sprint(host["SecurityOpt"]), "no-new-privileges") {
		t.Errorf("host config = %v", host)
	}
	if tmpfs, _ := host["Tmpfs"].(map[string]any); tmpfs["/tmp"] == nil {
		t.Errorf("no writable /tmp on a read-only root: %v", host["Tmpfs"])
	}
	if e.removed != 1 || e.killed != 0 {
		t.Errorf("removed %d, killed %d; want removed once, never killed", e.removed, e.killed)
	}
}

// TestRunOnce_ANamedNetworkAndAWritableRoot are opted into, never default.
func TestRunOnce_ANamedNetworkAndAWritableRoot(t *testing.T) {
	e := &runEngine{imagePresent: true}
	if _, err := runSession(t, e).RunOnce(context.Background(), RunOnceOptions{Image: runImage, Cmd: []string{"true"}, Timeout: time.Minute, Network: "gate-net", Writable: true}); err != nil {
		t.Fatal(err)
	}
	host, _ := e.create["HostConfig"].(map[string]any)
	if host["NetworkMode"] != "gate-net" || host["ReadonlyRootfs"] == true || host["Tmpfs"] != nil {
		t.Errorf("host config = %v", host)
	}
}

// TestRunOnce_MountsAreReadOnlyAndEnvIsPassed: every mount reaches the
// engine as a read-only bind, and the environment as given.
func TestRunOnce_MountsAreReadOnlyAndEnvIsPassed(t *testing.T) {
	e := &runEngine{imagePresent: true}
	opts := RunOnceOptions{Image: runImage, Cmd: []string{"true"}, Timeout: time.Minute,
		Mounts: []ReadOnlyMount{{Source: "/srv/app", Target: "/src"}, {Source: "/var/lib/osv", Target: "/db"}},
		Env:    []string{"OSV_SCANNER_LOCAL_DB_CACHE_DIRECTORY=/db"}}
	if _, err := runSession(t, e).RunOnce(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	host, _ := e.create["HostConfig"].(map[string]any)
	mounts, _ := host["Mounts"].([]any)
	if len(mounts) != 2 {
		t.Fatalf("mounts = %v", host["Mounts"])
	}
	for _, raw := range mounts {
		m := raw.(map[string]any)
		if m["Type"] != "bind" || m["ReadOnly"] != true {
			t.Errorf("mount %v is not a read-only bind", m)
		}
	}
	if env, _ := e.create["Env"].([]any); len(env) != 1 || env[0] != "OSV_SCANNER_LOCAL_DB_CACHE_DIRECTORY=/db" {
		t.Errorf("env = %v", e.create["Env"])
	}
}

// TestRunOnce_AnAbsentImageIsPulledOnlyWhenAsked refuses it without pull,
// creating nothing, and pulls it with.
func TestRunOnce_AnAbsentImageIsPulledOnlyWhenAsked(t *testing.T) {
	e := &runEngine{}
	if _, err := runSession(t, e).RunOnce(context.Background(), RunOnceOptions{Image: runImage, Cmd: []string{"true"}, Timeout: time.Minute}); err == nil || !strings.Contains(err.Error(), "pulling it was not asked for") {
		t.Fatalf("an absent image without pull = %v", err)
	}
	if e.created != 0 || e.pulled != 0 {
		t.Fatalf("created %d, pulled %d after a refusal", e.created, e.pulled)
	}
	res, err := runSession(t, e).RunOnce(context.Background(), RunOnceOptions{Image: runImage, Cmd: []string{"true"}, Timeout: time.Minute, Pull: true})
	if err != nil || !res.Pulled || e.pulled != 1 {
		t.Fatalf("with pull = %+v, %v (pulled %d)", res, err, e.pulled)
	}
}

// TestRunOnce_ATimeoutKillsAndRemoves: a run past its timeout is killed,
// reported as timed out, and still removed.
func TestRunOnce_ATimeoutKillsAndRemoves(t *testing.T) {
	e := &runEngine{imagePresent: true, waitFor: 2 * time.Second}
	start := time.Now()
	res, err := runSession(t, e).RunOnce(context.Background(), RunOnceOptions{Image: runImage, Cmd: []string{"sleep", "60"}, Timeout: 100 * time.Millisecond})
	if err != nil || !res.TimedOut {
		t.Fatalf("RunOnce = %+v, %v; want a timeout", res, err)
	}
	if time.Since(start) > time.Second {
		t.Errorf("the timeout took %v to be noticed", time.Since(start))
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.killed != 1 || e.removed != 1 {
		t.Errorf("killed %d, removed %d; want once each", e.killed, e.removed)
	}
}

// TestRunOnce_RefusesBeforeContact: an unpinned image, no command, no
// timeout and a malformed network name are refused before a request, and
// an unusable container ID from the engine is refused, never used.
func TestRunOnce_RefusesBeforeContact(t *testing.T) {
	e := &runEngine{imagePresent: true}
	s := runSession(t, e)
	for name, opts := range map[string]RunOnceOptions{
		"an image by tag":  {Image: "alpine:3.20", Cmd: []string{"true"}, Timeout: time.Minute},
		"no command":       {Image: runImage, Timeout: time.Minute},
		"no timeout":       {Image: runImage, Cmd: []string{"true"}},
		"a network":        {Image: runImage, Cmd: []string{"true"}, Timeout: time.Minute, Network: "--privileged"},
		"a relative mount": {Image: runImage, Cmd: []string{"true"}, Timeout: time.Minute, Mounts: []ReadOnlyMount{{Source: "srv", Target: "/src"}}},
		"a mount over /":   {Image: runImage, Cmd: []string{"true"}, Timeout: time.Minute, Mounts: []ReadOnlyMount{{Source: "/srv", Target: "/"}}},
		"an unclean mount": {Image: runImage, Cmd: []string{"true"}, Timeout: time.Minute, Mounts: []ReadOnlyMount{{Source: "/srv/../etc", Target: "/src"}}},
		"a bad env name":   {Image: runImage, Cmd: []string{"true"}, Timeout: time.Minute, Env: []string{"1X=y"}},
		"env with no =":    {Image: runImage, Cmd: []string{"true"}, Timeout: time.Minute, Env: []string{"NAME"}},
	} {
		if _, err := s.RunOnce(context.Background(), opts); err == nil {
			t.Errorf("%s was run", name)
		}
	}
	if e.created != 0 {
		t.Fatalf("%d containers created from refused runs", e.created)
	}
	hostile := &runEngine{imagePresent: true, containerID: "../../etc"}
	if _, err := runSession(t, hostile).RunOnce(context.Background(), RunOnceOptions{Image: runImage, Cmd: []string{"true"}, Timeout: time.Minute}); err == nil || !strings.Contains(err.Error(), "unusable container ID") {
		t.Fatalf("a hostile container ID = %v", err)
	}
	if hostile.removed != 0 {
		t.Error("a removal was sent for a container ID that was refused")
	}
}
