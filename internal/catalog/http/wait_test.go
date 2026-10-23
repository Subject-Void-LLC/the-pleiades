// Tests for http.wait against real HTTP servers: a service that comes up
// late, one that answers wrongly until it is ready, a wait that runs out,
// and the calls refused before any request.
package http_test

import (
	"context"
	"errors"
	"net"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	httpcoll "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/http"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

func runWait(t *testing.T, params map[string]any) (*checkCtx, error) {
	t.Helper()
	rc := &checkCtx{stats: map[string]any{}}
	_, err := httpcoll.Wait(context.Background(), rc, nil, params)
	return rc, err
}

// TestWait_UntilTheServiceAnswersAsWanted: 503 twice, then 200 with the
// body wanted; the wait passes on the third request and returns that
// answer, never the body.
func TestWait_UntilTheServiceAnswersAsWanted(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		if hits.Add(1) < 3 {
			w.WriteHeader(nethttp.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(secretBody))
	}))
	defer srv.Close()
	rc, err := runWait(t, map[string]any{"url": srv.URL + "/v2/version", "body_contains": `"status":"ok"`, "timeout": 20, "sleep": 1})
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if rc.stats["attempts"] != 3 || rc.stats["status"] != 200 {
		t.Errorf("stats %v, want 3 attempts ending in 200", rc.stats)
	}
	for k, v := range rc.stats {
		if strings.Contains(strings.ToLower(k+" "+toString(v)), "body-sentinel") {
			t.Errorf("the body reached stat %s", k)
		}
	}
}

func toString(v any) string {
	s, _ := v.(string)
	return s
}

// TestWait_AServiceThatIsNotReadyYet: a connection the service drops
// before answering is "not yet", so a service that starts answering during
// the wait passes it. The port stays held throughout: a released port is
// anyone's.
func TestWait_AServiceThatIsNotReadyYet(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ready := time.Now().Add(1500 * time.Millisecond)
	srv := &nethttp.Server{ReadHeaderTimeout: time.Second, Handler: nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) {})}
	defer func() { _ = srv.Close() }()
	go func() { _ = srv.Serve(&droppingListener{Listener: l, until: ready}) }()
	rc, err := runWait(t, map[string]any{"url": "http://" + l.Addr().String() + "/", "timeout": 20})
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if n, _ := rc.stats["attempts"].(int); n < 2 {
		t.Errorf("attempts %v, want the dropped ones counted too", rc.stats["attempts"])
	}
}

// droppingListener closes every connection it accepts before until, as a
// service still starting does.
type droppingListener struct {
	net.Listener
	until time.Time
}

func (d *droppingListener) Accept() (net.Conn, error) {
	for {
		c, err := d.Listener.Accept()
		if err != nil || time.Now().After(d.until) {
			return c, err
		}
		_ = c.Close()
	}
}

// TestWait_RunningOutNamesTheLastAnswer: a service that never answers as
// wanted fails the task after its timeout, naming what it answered.
func TestWait_RunningOutNamesTheLastAnswer(t *testing.T) {
	srv := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		w.WriteHeader(nethttp.StatusBadGateway)
	}))
	defer srv.Close()
	start := time.Now()
	rc, err := runWait(t, map[string]any{"url": srv.URL, "timeout": 2})
	if err == nil || !strings.Contains(err.Error(), "timed out after 2s") || !strings.Contains(err.Error(), "answered 502") {
		t.Fatalf("err = %v, want a timeout naming the last answer", err)
	}
	if took := time.Since(start); took > 6*time.Second {
		t.Errorf("a 2 second wait took %s", took)
	}
	if rc.stats["status"] != 502 {
		t.Errorf("stats %v, want the last answer's status", rc.stats)
	}
}

// TestWait_Refusals: calls refused before any request is made.
func TestWait_Refusals(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) { hits.Add(1) }))
	defer srv.Close()
	for name, params := range map[string]map[string]any{
		"no url":                    {},
		"a credential in the url":   {"url": "http://user:pw@127.0.0.1/"},
		"zero timeout":              {"url": srv.URL, "timeout": 0},
		"zero sleep":                {"url": srv.URL, "sleep": 0},
		"a delay past the timeout":  {"url": srv.URL, "timeout": 5, "delay": 5},
		"a timeout past a day":      {"url": srv.URL, "timeout": 86401},
		"a method that changes":     {"url": srv.URL, "method": "POST"},
		"a quoted number":           {"url": srv.URL, "timeout": "30"},
		"a body test on a HEAD":     {"url": srv.URL, "method": "HEAD", "body_contains": "x"},
		"two body tests":            {"url": srv.URL, "body_contains": "x", "body_matches": "y"},
		"a negative delay":          {"url": srv.URL, "delay": -1},
		"too many redirects to try": {"url": srv.URL, "max_redirects": 11},
	} {
		if _, err := runWait(t, params); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if hits.Load() != 0 {
		t.Errorf("a refused call reached the server %d times", hits.Load())
	}
}

// TestWait_StopsWhenTheRunIsCancelled: a torn-down run ends the wait at
// once, saying so, whether it comes between requests or during one.
func TestWait_StopsWhenTheRunIsCancelled(t *testing.T) {
	for name, handler := range map[string]nethttp.HandlerFunc{
		"between requests": func(w nethttp.ResponseWriter, _ *nethttp.Request) { w.WriteHeader(nethttp.StatusServiceUnavailable) },
		"during a request": func(_ nethttp.ResponseWriter, r *nethttp.Request) { <-r.Context().Done() },
	} {
		srv := httptest.NewServer(handler)
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		start := time.Now()
		_, err := httpcoll.Wait(ctx, &checkCtx{stats: map[string]any{}}, nil, map[string]any{"url": srv.URL, "timeout": 60, "sleep": 1})
		if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") || time.Since(start) > 5*time.Second {
			t.Errorf("%s: err = %v after %s, want a prompt failure naming the cancellation", name, err, time.Since(start))
		}
		cancel()
		srv.Close()
	}
}

// failingCtx refuses to record one stat.
type failingCtx struct {
	checkCtx
	fail string
}

func (c *failingCtx) SetStat(k string, v any) error {
	if k == c.fail {
		return errors.New("recording refused")
	}
	return c.checkCtx.SetStat(k, v)
}

// TestWait_AStatThatCannotBeRecordedFailsTheTask: the wait's own counts and
// the answer it reports are each part of the result.
func TestWait_AStatThatCannotBeRecordedFailsTheTask(t *testing.T) {
	srv := httptest.NewServer(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) {}))
	defer srv.Close()
	for _, stat := range []string{"attempts", "status"} {
		rc := &failingCtx{checkCtx: checkCtx{stats: map[string]any{}}, fail: stat}
		if _, err := httpcoll.Wait(context.Background(), rc, nil, map[string]any{"url": srv.URL}); err == nil || !strings.Contains(err.Error(), "recording refused") {
			t.Errorf("%s not recorded: err = %v", stat, err)
		}
	}
}

func TestWait_Registered(t *testing.T) {
	d, ok := collection.Lookup("http.wait")
	if !ok || d.Manifest.Status != collection.StatusImplemented || !d.Manifest.Reversibility.ReadOnly || d.Manifest.SupportsCheck || d.Manifest.NoCheckReason == "" {
		t.Fatalf("http.wait = %+v", d.Manifest)
	}
}
