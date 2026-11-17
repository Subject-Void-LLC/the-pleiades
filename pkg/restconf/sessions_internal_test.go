// Package restconf: waiting out a server's session cap, through the real
// client (ClientFor, real TLS pinned to the server's certificate, the
// credential) against restconftest capping its sessions as IOS XE 17.15 was
// measured to: each request a session, whatever connection carries it, held
// a while after its answer, and the request past the cap refused 409 in-use
// "Too many sessions.".
package restconf

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf/restconftest"
)

// fastPacing shortens the pacing for one test, restoring it after.
func fastPacing(t *testing.T, bound time.Duration) {
	t.Helper()
	first, most, all := sessionRetryFirst, sessionRetryMax, sessionWaitBound
	sessionRetryFirst, sessionRetryMax, sessionWaitBound = 5*time.Millisecond, 40*time.Millisecond, bound
	t.Cleanup(func() { sessionRetryFirst, sessionRetryMax, sessionWaitBound = first, most, all })
}

// cappedSources serves n sources, m000 and on, under a cap of sessions held
// for hold, and returns the server and a client with its root discovered.
func cappedSources(t *testing.T, n, sessionCap int, hold time.Duration) (*restconftest.Server, *Client) {
	t.Helper()
	sources := map[string][]byte{}
	for i := range n {
		name := fmt.Sprintf("m%03d", i)
		sources[name+"@2026-11-16"] = []byte("module " + name + " {\n  namespace \"urn:t:" + name + "\";\n  prefix p;\n  revision 2026-11-16;\n}\n")
	}
	s := restconftest.New(t, restconftest.Options{Username: "admin", Password: "s3cret-Passw0rd", Sources: sources, SessionCap: sessionCap, SessionHold: hold})
	client, _, err := ClientFor(restconftest.NewDevice(t, s), "c8k", restconftest.Secrets(s))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	if _, err := client.DiscoverRoot(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s, client
}

// fetchAll fetches every source modules-state names, in name order, and
// returns how many came back and the first failure.
func fetchAll(t *testing.T, client *Client) (int, error) {
	t.Helper()
	schemas, err := client.StateSchemas(context.Background())
	if err != nil {
		return 0, err
	}
	for i, e := range schemas {
		got, err := client.GetSchema(context.Background(), e.Locations[0], 1<<20)
		if err != nil {
			return i, fmt.Errorf("fetching %s: %w", e.Key(), err)
		}
		if !strings.HasPrefix(string(got), "module "+e.Name+" {") {
			return i, fmt.Errorf("%s came back as %q", e.Key(), got)
		}
	}
	return len(schemas), nil
}

// TestPaced_A676SourceFetchStaysWithinTheCap is the fetch measured failing on
// a Catalyst 8000V: 676 sources under a cap of 100 sessions. It completes,
// every refusal waited out, on one connection, and the client says it waited.
func TestPaced_A676SourceFetchStaysWithinTheCap(t *testing.T) {
	fastPacing(t, 10*time.Second)
	s, client := cappedSources(t, 676, 100, 50*time.Millisecond)
	n, err := fetchAll(t, client)
	if err != nil || n != 676 {
		t.Fatalf("fetched %d of 676: %v", n, err)
	}
	if s.SessionRefusals() == 0 {
		t.Fatal("the server refused nothing, so the cap was never reached and nothing is proven")
	}
	if s.Connections() != 1 {
		t.Errorf("%d connections, want the one keep-alive connection for the whole fetch", s.Connections())
	}
	warned := client.SessionWarnings("c8k")
	if len(warned) != 1 || !strings.Contains(warned[0], fmt.Sprintf("refused %d RESTCONF requests", s.SessionRefusals())) || !strings.Contains(warned[0], "too many sessions") {
		t.Errorf("SessionWarnings = %q, want the %d refusals and the wait named", warned, s.SessionRefusals())
	}
}

// TestPaced_WithoutWaitingTheFetchFailsAsMeasured is the defect itself: a
// client that never waits is refused at the request past the cap, with IOS
// XE's answer, having sent each request once.
func TestPaced_WithoutWaitingTheFetchFailsAsMeasured(t *testing.T) {
	fastPacing(t, 0)
	s, client := cappedSources(t, 676, 100, time.Hour)
	n, err := fetchAll(t, client)
	var exhausted *SessionsExhaustedError
	var status *StatusError
	if !errors.As(err, &exhausted) || !errors.As(err, &status) || status.Status != http.StatusConflict || !status.HasTag(tagInUse) {
		t.Fatalf("fetched %d: %v, want the session cap's refusal", n, err)
	}
	// modules-state was the first session, so the 100th source is the 101st.
	if n != 99 || exhausted.Refusals != 1 || exhausted.Waited != 0 || s.SessionRefusals() != 1 {
		t.Errorf("refused at source %d after %d refusals and %s, server %d; want the 100th source refused once, unwaited", n, exhausted.Refusals, exhausted.Waited, s.SessionRefusals())
	}
	if !strings.Contains(err.Error(), `"Too many sessions."`) || !strings.Contains(err.Error(), "nothing was applied") || !strings.Contains(err.Error(), "bound of 0s") {
		t.Errorf("the refusal says %q", err)
	}
}

// TestPaced_TheCallersTimeEndsTheWait: a cap that never frees ends when the
// caller's context does, saying so.
func TestPaced_TheCallersTimeEndsTheWait(t *testing.T) {
	fastPacing(t, time.Hour)
	_, client := cappedSources(t, 1, 1, time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := client.StateSchemas(ctx)
	if err == nil {
		// DiscoverRoot read host-meta with no credential, which opens no
		// session, so modules-state took the one session.
		_, err = client.StateSchemas(ctx)
	}
	var exhausted *SessionsExhaustedError
	if !errors.As(err, &exhausted) || !errors.Is(exhausted.Cause, context.DeadlineExceeded) || exhausted.Refusals < 2 {
		t.Fatalf("a cap that never frees: %v, want the wait ended by the deadline after more than one refusal", err)
	}
	if !strings.Contains(err.Error(), "when the task's own time ended") {
		t.Errorf("the refusal says %q", err)
	}
}

// scripted answers each request to path with the next of answers, the last
// repeated, counting the requests and the methods they used.
type scripted struct {
	mu      sync.Mutex
	answers []func(http.ResponseWriter)
	methods []string
}

// serve answers one request.
func (s *scripted) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.methods = append(s.methods, r.Method)
	next := s.answers[min(len(s.methods), len(s.answers))-1]
	s.mu.Unlock()
	next(w)
}

// count is how many requests were answered.
func (s *scripted) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.methods)
}

// answering is an answer of status with body as JSON.
func answering(status int, body string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/yang-data+json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

// scriptedServer answers path as script says and returns a client with its
// root discovered.
func scriptedServer(t *testing.T, path string, script *scripted) *Client {
	t.Helper()
	s := restconftest.New(t, restconftest.Options{Username: "admin", Password: "s3cret-Passw0rd", PostChild: "Loopback=7",
		Override: map[string]http.HandlerFunc{path: script.serve}})
	client, _, err := ClientFor(restconftest.NewDevice(t, s), "c8k", restconftest.Secrets(s))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.DiscoverRoot(context.Background()); err != nil {
		t.Fatal(err)
	}
	return client
}

// TestPaced_OnlyTheSessionCapIsWaitedOut: a 409 that is not the session cap
// (a lock held, which is in-use too, and data-exists) is the caller's at
// once, sent once.
func TestPaced_OnlyTheSessionCapIsWaitedOut(t *testing.T) {
	// Short, so a client waiting out any of these fails here rather than
	// hangs.
	fastPacing(t, 200*time.Millisecond)
	const path = "/restconf/data/Cisco-IOS-XE-native:native"
	for _, body := range []string{
		`{"ietf-restconf:errors":{"error":[{"error-type":"protocol","error-tag":"in-use","error-message":"the configuration database is locked by session 12"}]}}`,
		`{"ietf-restconf:errors":{"error":[{"error-type":"application","error-tag":"data-exists","error-message":"Too many sessions."}]}}`,
		`{"ietf-restconf:errors":{"error":[{"error-type":"application","error-tag":"in-use"}]}}`,
	} {
		script := &scripted{answers: []func(http.ResponseWriter){answering(http.StatusConflict, body)}}
		client := scriptedServer(t, path, script)
		_, _, err := client.GetData(context.Background(), Path{{Module: "Cisco-IOS-XE-native", Name: "native"}})
		var exhausted *SessionsExhaustedError
		var status *StatusError
		if errors.As(err, &exhausted) || !errors.As(err, &status) || status.Status != http.StatusConflict || script.count() != 1 {
			t.Errorf("409 %s: %v after %d requests, want the answer at once, sent once", body, err, script.count())
		}
		if client.SessionWarnings("c8k") != nil {
			t.Errorf("409 %s: SessionWarnings = %q, want none", body, client.SessionWarnings("c8k"))
		}
	}
}

// TestPaced_AWriteIsSentAgainAndARefusedLoginNever: the device opened no
// session for a write it refused for too many sessions, so the write is sent
// again and lands; a login refused while waiting ends the wait, never sent
// again.
func TestPaced_AWriteIsSentAgainAndARefusedLoginNever(t *testing.T) {
	fastPacing(t, time.Hour)
	const path = "/restconf/data/Cisco-IOS-XE-native:native/interface"
	tooMany := answering(http.StatusConflict, restconftest.IOSXETooManySessions)
	created := func(w http.ResponseWriter) { w.WriteHeader(http.StatusCreated) }
	script := &scripted{answers: []func(http.ResponseWriter){tooMany, tooMany, created}}
	client := scriptedServer(t, path, script)
	p := Path{{Module: "Cisco-IOS-XE-native", Name: "native"}, {Name: "interface"}}
	w, err := client.Write(context.Background(), MethodPost, "", p, MediaTypeJSON, []byte(`{"Cisco-IOS-XE-native:Loopback":[{"name":7}]}`))
	if err != nil || w.Status != http.StatusCreated || strings.Join(script.methods, " ") != "POST POST POST" {
		t.Fatalf("a write refused twice for too many sessions: %+v, %v, methods %v; want it sent again until it lands", w, err, script.methods)
	}
	if warned := client.SessionWarnings("c8k"); len(warned) != 1 || !strings.Contains(warned[0], "refused 2 RESTCONF requests") {
		t.Errorf("SessionWarnings = %q", warned)
	}

	unauthorized := answering(http.StatusUnauthorized, `{"ietf-restconf:errors":{"error":[{"error-type":"protocol","error-tag":"access-denied"}]}}`)
	script = &scripted{answers: []func(http.ResponseWriter){tooMany, unauthorized}}
	client = scriptedServer(t, path, script)
	_, err = client.Write(context.Background(), MethodPost, "", p, MediaTypeJSON, []byte(`{}`))
	var refused *RefusedError
	if !errors.As(err, &refused) || script.count() != 2 {
		t.Fatalf("a login refused while waiting: %v after %d requests, want the refusal, sent no more", err, script.count())
	}
	if _, err := client.Write(context.Background(), MethodPost, "", p, MediaTypeJSON, []byte(`{}`)); !errors.As(err, &refused) || script.count() != 2 {
		t.Errorf("after a refused login: %v after %d requests, want nothing sent", err, script.count())
	}
}
