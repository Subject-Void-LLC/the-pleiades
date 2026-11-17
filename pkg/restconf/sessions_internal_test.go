// Package restconf: waiting out a server's session cap, through the real
// client (ClientFor, real TLS pinned to the server's certificate, the
// credential) against restconftest capping its sessions as IOS XE 17.15 was
// measured to: each request a session, whatever connection carries it, held
// a while after its answer, and the request past the cap refused 409 in-use
// "Too many sessions.". The server releases its sessions after a count of
// refusals, never after a time, so what each test proves holds whatever the
// machine's speed.
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

// fastPacing shortens the default pacing for one test, restoring it after:
// waits of 1, 2 and then 4 milliseconds, perRequest for one request and
// perTask for the client.
func fastPacing(t *testing.T, perRequest, perTask time.Duration) {
	t.Helper()
	saved := defaultPacing
	defaultPacing = Pacing{First: time.Millisecond, Max: 4 * time.Millisecond, PerRequest: perRequest, PerTask: perTask}
	t.Cleanup(func() { defaultPacing = saved })
}

// capped is a server capping its sessions as opts says, holding n sources
// (m000 and on), and a client with its root discovered.
func capped(t *testing.T, n int, opts restconftest.Options) (*restconftest.Server, *Client) {
	t.Helper()
	opts.Sources = map[string][]byte{}
	for i := range n {
		name := fmt.Sprintf("m%03d", i)
		opts.Sources[name+"@2026-11-16"] = []byte("module " + name + " {\n  namespace \"urn:t:" + name + "\";\n  prefix p;\n  revision 2026-11-16;\n}\n")
	}
	opts.Username, opts.Password = "admin", "s3cret-Passw0rd"
	s := restconftest.New(t, opts)
	client, _, err := ClientFor(restconftest.NewDevice(t, s), "c8k", restconftest.Secrets(s))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	// host-meta is read with no credential, which opens no session.
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
// a Catalyst 8000V: 676 sources under a cap of 100 sessions, released after
// two refusals. It completes, every refusal waited out, on one connection,
// and the client says it waited and sent each refused request again.
func TestPaced_A676SourceFetchStaysWithinTheCap(t *testing.T) {
	fastPacing(t, time.Minute, time.Minute)
	s, client := capped(t, 676, restconftest.Options{SessionCap: 100, SessionReleaseAfter: 2})
	n, err := fetchAll(t, client)
	if err != nil || n != 676 {
		t.Fatalf("fetched %d of 676: %v", n, err)
	}
	// modules-state and 676 sources are 677 sessions: the cap is met six
	// times, each costing two refusals.
	if s.SessionRefusals() != 12 || s.Connections() != 1 {
		t.Errorf("%d refusals on %d connections, want 12 on the one keep-alive connection", s.SessionRefusals(), s.Connections())
	}
	warned := client.SessionWarnings("c8k")
	if len(warned) != 1 || !strings.Contains(warned[0], "answered RESTCONF requests 12 times with 409 in-use, too many sessions") ||
		!strings.Contains(warned[0], "at most 1m0s per task") || !strings.Contains(warned[0], "sending each refused request again until it was answered") {
		t.Errorf("SessionWarnings = %q, want the 12 refusals, the task's limit and every request answered", warned)
	}
}

// TestPaced_WithoutWaitingTheFetchFailsAsMeasured is the defect itself: a
// client that never waits is refused at the request past the cap, with IOS
// XE's answer, having sent each request once.
func TestPaced_WithoutWaitingTheFetchFailsAsMeasured(t *testing.T) {
	fastPacing(t, 0, time.Minute)
	s, client := capped(t, 676, restconftest.Options{SessionCap: 100})
	n, err := fetchAll(t, client)
	var exhausted *SessionsExhaustedError
	var status *StatusError
	if !errors.As(err, &exhausted) || !errors.As(err, &status) || status.Status != http.StatusConflict || !status.HasTag(tagInUse) {
		t.Fatalf("fetched %d: %v, want the session cap's refusal", n, err)
	}
	// modules-state was the first session, so the 100th source is the 101st.
	if n != 99 || exhausted.Refusals != 1 || exhausted.Waited != 0 || exhausted.Limit != LimitPerRequest || s.SessionRefusals() != 1 {
		t.Errorf("refused at source %d after %d refusals, %s, limit %q, server %d; want the 100th source refused once, unwaited, per request", n, exhausted.Refusals, exhausted.Waited, exhausted.Limit, s.SessionRefusals())
	}
	for _, want := range []string{`"Too many sessions."`, "suggests the request was not applied", "limit of 0s of waiting per request"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q lacks %q", err, want)
		}
	}
	if warned := client.SessionWarnings("c8k"); len(warned) != 1 || !strings.Contains(warned[0], "gave up on 1 of them") || strings.Contains(warned[0], "until it was answered") {
		t.Errorf("SessionWarnings = %q, want the request given up on named", warned)
	}
}

// TestPaced_TheCallersTimeEndsTheWait: a cap that never frees ends when the
// caller's context does, the error naming whose time that was.
func TestPaced_TheCallersTimeEndsTheWait(t *testing.T) {
	fastPacing(t, time.Hour, time.Hour)
	_, client := capped(t, 1, restconftest.Options{SessionCap: 1})
	// The one session, taken under a context of its own.
	if _, err := client.StateSchemas(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, within := range []string{"", "onboarding's time"} {
		if within != "" {
			client.WaitsWithin(within)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		_, err := client.StateSchemas(ctx)
		cancel()
		var exhausted *SessionsExhaustedError
		if !errors.As(err, &exhausted) || !errors.Is(exhausted.Cause, context.DeadlineExceeded) || exhausted.Limit != "" || exhausted.Refusals < 2 {
			t.Fatalf("a cap that never frees: %v, want the wait ended by the deadline after more than one refusal", err)
		}
		want := "when the task's time ended"
		if within != "" {
			want = "when " + within + " ended"
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not say %q", err, want)
		}
	}
}

// TestPaced_TheTaskBudgetEndsALongWait: the client's waits together are held
// to PerTask, so a request that has waited far less than PerRequest is given
// up on once the requests before it spent the task's budget.
func TestPaced_TheTaskBudgetEndsALongWait(t *testing.T) {
	fastPacing(t, time.Hour, 8*time.Millisecond)
	_, client := capped(t, 1, restconftest.Options{SessionCap: 2, SessionReleaseAfter: 3})
	ctx := context.Background()
	// Two sessions, then the third request is refused three times (waiting
	// 1, 2 and 4 ms), the third refusal releasing both; then two more.
	for range 4 {
		if _, err := client.Capabilities(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if client.sessionWaited != 7*time.Millisecond {
		t.Fatalf("waited %s, want 7ms", client.sessionWaited)
	}
	// The fifth is refused, waits the 1 ms the budget leaves, is refused
	// again and has nothing left.
	_, err := client.Capabilities(ctx)
	var exhausted *SessionsExhaustedError
	if !errors.As(err, &exhausted) || exhausted.Limit != LimitPerTask || exhausted.Bound != 8*time.Millisecond || exhausted.Waited != time.Millisecond || exhausted.Refusals != 2 {
		t.Fatalf("the budget spent: %v (%+v), want it given up on per task after its own 1ms", err, exhausted)
	}
	if !strings.Contains(err.Error(), "limit of 8ms of waiting per task") || client.sessionWaited != 8*time.Millisecond {
		t.Errorf("the refusal %q after %s waited in all", err, client.sessionWaited)
	}
}

// TestPaced_ARefusalInXMLIsWaitedOut: a server answering the refusal in the
// encoding asked for, XML first for a data read, is read as the same cap.
func TestPaced_ARefusalInXMLIsWaitedOut(t *testing.T) {
	fastPacing(t, time.Minute, time.Minute)
	s, client := capped(t, 1, restconftest.Options{SessionCap: 1, SessionReleaseAfter: 1, SessionRefusalXML: true})
	ctx := context.Background()
	if _, err := client.Capabilities(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Capabilities(ctx); err != nil || s.SessionRefusals() != 1 || client.sessionRefusals != 1 {
		t.Fatalf("an XML refusal: %v, %d refusals served, %d read; want it waited out", err, s.SessionRefusals(), client.sessionRefusals)
	}
}

// TestDiscover_ABusyDeviceIsNeverARefusedResource: a request given up on for
// too many sessions ends discovery, whether it was the capabilities or the
// library, rather than being recorded as a resource the device refused.
func TestDiscover_ABusyDeviceIsNeverARefusedResource(t *testing.T) {
	fastPacing(t, 0, time.Minute)
	for _, held := range []int{1, 2} {
		_, client := capped(t, 1, restconftest.Options{SessionCap: held})
		// One session taken first: with a cap of one the capabilities are
		// refused, with two the library.
		if _, err := client.Capabilities(context.Background()); err != nil {
			t.Fatal(err)
		}
		d, err := client.Discover(context.Background())
		var exhausted *SessionsExhaustedError
		if !errors.As(err, &exhausted) {
			t.Errorf("cap %d: Discover = %+v, %v; want the busy device's error, not a capability or library refused", held, d, err)
		}
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
	fastPacing(t, 200*time.Millisecond, 200*time.Millisecond)
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
		if errors.As(err, &exhausted) || !errors.As(err, &status) || status.Status != http.StatusConflict || status.Resent != 0 || script.count() != 1 {
			t.Errorf("409 %s: %v after %d requests, want the answer at once, sent once", body, err, script.count())
		}
		if client.SessionWarnings("c8k") != nil {
			t.Errorf("409 %s: SessionWarnings = %q, want none", body, client.SessionWarnings("c8k"))
		}
	}
}

// TestPaced_AWriteIsSentAgainAndSaysSo: a write refused for too many
// sessions is sent again and lands, its answer counting the resends, which a
// refusal after them carries too; a login refused while waiting ends the
// wait, never sent again.
func TestPaced_AWriteIsSentAgainAndSaysSo(t *testing.T) {
	fastPacing(t, time.Hour, time.Hour)
	const path = "/restconf/data/Cisco-IOS-XE-native:native/interface"
	tooMany := answering(http.StatusConflict, restconftest.IOSXETooManySessions)
	created := func(w http.ResponseWriter) { w.WriteHeader(http.StatusCreated) }
	script := &scripted{answers: []func(http.ResponseWriter){tooMany, tooMany, created}}
	client := scriptedServer(t, path, script)
	p := Path{{Module: "Cisco-IOS-XE-native", Name: "native"}, {Name: "interface"}}
	w, err := client.Write(context.Background(), MethodPost, "", p, MediaTypeJSON, []byte(`{"Cisco-IOS-XE-native:Loopback":[{"name":7}]}`))
	if err != nil || w.Status != http.StatusCreated || w.Resent != 2 || strings.Join(script.methods, " ") != "POST POST POST" {
		t.Fatalf("a write refused twice for too many sessions: %+v, %v, methods %v; want it sent again until it lands, counted", w, err, script.methods)
	}
	if warned := client.SessionWarnings("c8k"); len(warned) != 1 || !strings.Contains(warned[0], "answered RESTCONF requests 2 times") {
		t.Errorf("SessionWarnings = %q", warned)
	}

	exists := answering(http.StatusConflict, `{"ietf-restconf:errors":{"error":[{"error-type":"application","error-tag":"data-exists"}]}}`)
	script = &scripted{answers: []func(http.ResponseWriter){tooMany, exists}}
	client = scriptedServer(t, path, script)
	_, err = client.Write(context.Background(), MethodPost, "", p, MediaTypeJSON, []byte(`{}`))
	var status *StatusError
	if !errors.As(err, &status) || !status.HasTag("data-exists") || status.Resent != 1 {
		t.Errorf("a resent write answered data-exists: %v, want the answer saying it was sent again once", err)
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

// TestPacing_ContextAndDefaults: a context's pacing is used, each zero field
// its default, and the first-try wording of a lost answer names the first
// try.
func TestPacing_ContextAndDefaults(t *testing.T) {
	got := pacingOf(WithPacing(context.Background(), Pacing{First: time.Millisecond}))
	if got.First != time.Millisecond || got.Max != defaultPacing.Max || got.PerRequest != defaultPacing.PerRequest || got.PerTask != defaultPacing.PerTask {
		t.Errorf("pacing = %+v, want First set and the rest defaults", got)
	}
	if pacingOf(context.Background()) != defaultPacing {
		t.Error("a context carrying no pacing is not the default")
	}
	lost := &AnswerLostError{Err: errors.New("the post was answered 409"), FirstTry: true}
	if !strings.Contains(lost.Error(), "whether the first try applied it is not known") {
		t.Errorf("AnswerLostError = %q", lost)
	}
}
