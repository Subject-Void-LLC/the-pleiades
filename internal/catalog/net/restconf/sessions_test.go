// Package restconf_test: net.restconf.config and net.restconf.facts against
// a server capping its sessions as IOS XE does (restconftest's SessionCap),
// through each method's registered Invoke and the real client: each write
// method refused for too many sessions and sent again, both by a server that
// applied nothing for the refused try (IOS XE's answer suggests that) and by
// one that applied it before refusing, where the task must say the first
// try's outcome is not known rather than report a plain result.
package restconf_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf/restconftest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// fastSessions is a context whose client waits a millisecond or two for a
// device's sessions, rather than its real seconds; perRequest bounds one
// request's wait.
func fastSessions(perRequest time.Duration) context.Context {
	return restconf.WithPacing(context.Background(), restconf.Pacing{First: time.Millisecond, Max: 2 * time.Millisecond, PerRequest: perRequest, PerTask: time.Minute})
}

// sessionCase is one write method: its parameters and the resource the
// server holds before it.
type sessionCase struct {
	name    string
	params  map[string]any
	stored  bool
	undos   int
	opts    restconftest.Options
	readsAt string
}

// sessionCases are the four write methods, each of which creates, merges,
// replaces or removes the NACM group.
func sessionCases() []sessionCase {
	post := login
	post.PostChild = "group=ops"
	return []sessionCase{
		{name: "post", opts: post, undos: 1, readsAt: "/restconf/data/ietf-netconf-acm:nacm/groups/group=ops",
			params: map[string]any{"method": "post", "path": "ietf-netconf-acm:nacm/groups", "content": `{"ietf-netconf-acm:group": [{"name": "ops"}]}`}},
		{name: "put", opts: login, undos: 1, readsAt: groupStored, params: map[string]any{"method": "put", "path": groupPath, "content": groupDoc}},
		{name: "patch", opts: login, stored: true, readsAt: groupStored, params: map[string]any{"method": "patch", "path": groupPath, "content": strings.ReplaceAll(groupDoc, "alice", "bob")}},
		{name: "delete", opts: login, stored: true, readsAt: groupStored, params: map[string]any{"method": "delete", "path": groupPath}},
	}
}

// server is a server for c, holding the group when c says so.
func (c sessionCase) server(t *testing.T, opts restconftest.Options) *restconftest.Server {
	t.Helper()
	s := restconftest.New(t, opts)
	if c.stored {
		s.Store(groupStored, "application/yang-data+json", []byte(groupDoc))
	}
	return s
}

// beforeWrite is how many requests carrying a credential c's task sends
// before its write, counted on an uncapped server: a cap of that many makes
// the write the first request refused.
func (c sessionCase) beforeWrite(t *testing.T) int {
	t.Helper()
	s := c.server(t, c.opts)
	// Only the requests count here; a server scripted to refuse the write
	// fails the task.
	_, _ = runConfig(t, s, newRecorder(restconftest.Secrets(s)), c.params)
	paths := s.Paths()
	n := 0
	for i, m := range s.Methods() {
		if m != http.MethodGet {
			return n
		}
		if paths[i] != "/.well-known/host-meta" {
			n++
		}
	}
	t.Fatalf("%s sent no write", c.name)
	return 0
}

// capped runs c against a server whose cap refuses its write once, applying
// it first when applied says so, and returns the server, what was recorded
// and the task's error.
func (c sessionCase) capped(t *testing.T, applied bool) (*restconftest.Server, *recorder, error) {
	t.Helper()
	opts := c.opts
	opts.SessionCap, opts.SessionReleaseAfter, opts.SessionApplyRefused = c.beforeWrite(t), 1, applied
	s := c.server(t, opts)
	rc := newRecorder(restconftest.Secrets(s))
	_, err := configMethod(t).Invoke(fastSessions(time.Minute), rc, restconftest.NewDevice(t, s), c.params)
	if s.SessionRefusals() != 1 {
		t.Fatalf("%s: %d refusals, want the write refused once", c.name, s.SessionRefusals())
	}
	return s, rc, err
}

// TestConfig_AWriteRefusedForSessionsIsSentAgainAndLands: a write the device
// refused for too many sessions, having applied nothing, is sent again; it
// lands, its undo (for a create) is recorded once, the resource is read
// after, and the task says it waited.
func TestConfig_AWriteRefusedForSessionsIsSentAgainAndLands(t *testing.T) {
	for _, c := range sessionCases() {
		s, rc, err := c.capped(t, false)
		if err != nil {
			t.Errorf("%s: %v, want the resend to land", c.name, err)
			continue
		}
		if rc.sets[sdk.StatInverse] != c.undos {
			t.Errorf("%s: undo recorded %d times, want %d", c.name, rc.sets[sdk.StatInverse], c.undos)
		}
		if paths := s.Paths(); paths[len(paths)-1] != c.readsAt || s.Methods()[len(paths)-1] != http.MethodGet {
			t.Errorf("%s: last request %s, want the read after of %s", c.name, paths[len(paths)-1], c.readsAt)
		}
		if !strings.Contains(strings.Join(rc.warnings(), "\n"), "answered RESTCONF requests 1 times with 409 in-use") {
			t.Errorf("%s: warnings %q do not say the write waited", c.name, rc.warnings())
		}
	}
}

// TestConfig_AResentWriteAFirstTryMayHaveAppliedIsNotKnown: against a server
// that applied each write before refusing it, the resend's answer is one the
// first try explains too. A post answered 409, a delete answered 404 and a
// put of a resource not there answered 204 each fail saying whether the
// first try applied it is not known, and record no undo; a patch merges the
// same document again and is a plain change.
func TestConfig_AResentWriteAFirstTryMayHaveAppliedIsNotKnown(t *testing.T) {
	for _, c := range sessionCases() {
		_, rc, err := c.capped(t, true)
		if c.name == "patch" {
			if err != nil {
				t.Errorf("patch: %v, want a plain change", err)
			}
			continue
		}
		var lost *restconf.AnswerLostError
		if !errors.As(err, &lost) || !lost.FirstTry || !strings.Contains(err.Error(), "whether the first try applied it is not known") {
			t.Errorf("%s: %v, want the first try's outcome said not known", c.name, err)
		}
		if rc.sets[sdk.StatInverse] != 0 {
			t.Errorf("%s: an undo was recorded for what the first try may have made", c.name)
		}
		if c.name != "post" {
			diff, _ := rc.stats[sdk.StatDiff].(map[string]any)
			after, _ := diff[sdk.DiffAfter].(map[string]any)
			if after["exists"] != (c.name == "put") {
				t.Errorf("%s: diff %v, want the read after's answer recorded", c.name, diff)
			}
		}
	}
}

// TestFactsAndConfig_ABusyDeviceFailsSayingItWaited: a discovery the device
// kept refusing for too many sessions fails, never recording the refused
// read as a capability or library the device lacks, and the task records
// the wait beside its failure.
func TestFactsAndConfig_ABusyDeviceFailsSayingItWaited(t *testing.T) {
	for _, name := range []string{"net.restconf.facts", "net.restconf.config"} {
		opts := login
		// The capabilities take the one session, so the library is refused
		// for good.
		opts.SessionCap = 1
		s := restconftest.New(t, opts)
		rc := newRecorder(restconftest.Secrets(s))
		d := method(t)
		params := map[string]any{}
		if name == "net.restconf.config" {
			d, params = configMethod(t), map[string]any{"method": "put", "path": groupPath, "content": groupDoc}
		}
		_, err := d.Invoke(fastSessions(5*time.Millisecond), rc, restconftest.NewDevice(t, s), params)
		var exhausted *restconf.SessionsExhaustedError
		if !errors.As(err, &exhausted) || writes(s) != 0 {
			t.Errorf("%s: %v with %d writes, want the busy device's error and nothing written", name, err, writes(s))
		}
		if warned := strings.Join(rc.warnings(), "\n"); !strings.Contains(warned, "409 in-use, too many sessions") || !strings.Contains(warned, "gave up on 1 of them") {
			t.Errorf("%s: warnings %q do not say the task waited and gave up", name, warned)
		}
		if len(rc.facts) != 0 {
			t.Errorf("%s: facts %v recorded from a discovery that failed", name, rc.facts)
		}
		// A wait that cannot be recorded is said beside the failure.
		s = restconftest.New(t, opts)
		rc = newRecorder(restconftest.Secrets(s))
		rc.failOn = map[string]int{sdk.StatWarnings: 2}
		if _, err := d.Invoke(fastSessions(5*time.Millisecond), rc, restconftest.NewDevice(t, s), params); err == nil || !strings.Contains(err.Error(), "the stat store is full") || !errors.As(err, &exhausted) {
			t.Errorf("%s: an unrecordable wait: %v, want both failures", name, err)
		}
	}
}

// TestConfig_AResentDeleteWhoseReadAfterFailsSaysBoth: a delete sent again
// and answered 404, whose read after then fails, still says the first try's
// outcome is not known, and why the resource's state is not known either; a
// diff that cannot be recorded is the task's failure.
func TestConfig_AResentDeleteWhoseReadAfterFailsSaysBoth(t *testing.T) {
	gets, failAfter := 0, true
	var mu sync.Mutex
	group := func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/yang-data+json")
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"ietf-restconf:errors":{"error":[{"error-type":"application","error-tag":"invalid-value"}]}}`))
		case gets == 0 || !failAfter:
			gets++
			_, _ = w.Write([]byte(groupDoc))
		default:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"ietf-restconf:errors":{"error":[{"error-type":"application","error-tag":"operation-failed"}]}}`))
		}
	}
	c := sessionCase{name: "delete", opts: login, params: map[string]any{"method": "delete", "path": groupPath}}
	c.opts.Override = map[string]http.HandlerFunc{groupStored: group}
	before := c.beforeWrite(t)
	for _, failDiff := range []bool{false, true} {
		mu.Lock()
		// The read after answers when the diff is what fails.
		gets, failAfter = 0, !failDiff
		mu.Unlock()
		opts := c.opts
		opts.SessionCap, opts.SessionReleaseAfter = before, 1
		s := restconftest.New(t, opts)
		rc := newRecorder(restconftest.Secrets(s))
		if failDiff {
			rc.failOn = map[string]int{sdk.StatDiff: 1}
		}
		_, err := configMethod(t).Invoke(fastSessions(time.Minute), rc, restconftest.NewDevice(t, s), c.params)
		var lost *restconf.AnswerLostError
		switch {
		case failDiff && (err == nil || !strings.Contains(err.Error(), "the stat store is full")):
			t.Errorf("a diff that cannot be recorded: %v", err)
		case !failDiff && (!errors.As(err, &lost) || !lost.FirstTry || !strings.Contains(err.Error(), "reading the resource after it failed")):
			t.Errorf("a resent delete whose read after fails: %v", err)
		}
	}
}
