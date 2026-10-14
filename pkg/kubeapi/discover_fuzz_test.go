// Discovery against an API server whose every answer is chosen by the test:
// the decode of untrusted discovery documents is fuzzed, and its bounds
// (bytes read, rules kept, entries per rule) are held to.
package kubeapi

import (
	"context"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// discoveryAnswers is what a fake API server answers each discovery
// request with.
type discoveryAnswers struct {
	version, apis, resources, rules, nodes []byte
}

// fakeDiscovery is an API server answering discovery from answers, which a
// test may replace between calls.
type fakeDiscovery struct {
	srv     *httptest.Server
	mu      sync.Mutex
	answers discoveryAnswers
}

func newFakeDiscovery(t testing.TB) *fakeDiscovery {
	t.Helper()
	f := &fakeDiscovery{}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		a := f.answers
		f.mu.Unlock()
		var body []byte
		switch {
		case r.URL.Path == "/version":
			body = a.version
		case r.URL.Path == "/api":
			body = []byte(`{"kind":"APIVersions","versions":["v1"]}`)
		case r.URL.Path == "/apis":
			body = a.apis
		case r.URL.Path == "/apis/authorization.k8s.io/v1/selfsubjectrulesreviews":
			body = a.rules
		case r.URL.Path == "/api/v1/nodes":
			body = a.nodes
		case r.URL.Path == "/api/v1" || strings.HasPrefix(r.URL.Path, "/apis/"):
			body = a.resources
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeDiscovery) set(a discoveryAnswers) {
	f.mu.Lock()
	f.answers = a
	f.mu.Unlock()
}

func (f *fakeDiscovery) session(t testing.TB) *Session {
	t.Helper()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.srv.Certificate().Raw})
	s, err := NewSession(Endpoint{Server: f.srv.URL, CAPEM: ca}, Identity{Token: "t"}, Limits{QPS: 1000, Burst: 1000}, "default")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// A well-formed discovery, which each test varies.
var (
	goodVersion   = []byte(`{"major":"1","minor":"37","gitVersion":"v1.37.1"}`)
	goodAPIs      = []byte(`{"kind":"APIGroupList","groups":[{"name":"apps","versions":[{"groupVersion":"apps/v1","version":"v1"}],"preferredVersion":{"groupVersion":"apps/v1","version":"v1"}}]}`)
	goodResources = []byte(`{"kind":"APIResourceList","groupVersion":"v1","resources":[{"name":"pods","kind":"Pod","namespaced":true,"verbs":["get"]},{"name":"pods/log","kind":"Pod","namespaced":true,"verbs":["get"]}]}`)
	goodRules     = []byte(`{"apiVersion":"authorization.k8s.io/v1","kind":"SelfSubjectRulesReview","status":{"incomplete":false,"resourceRules":[{"verbs":["get"],"apiGroups":["apps"],"resources":["deployments"]}]}}`)
	goodNodes     = []byte(`{"apiVersion":"v1","kind":"NodeList","items":[{"metadata":{"name":"n1"},"status":{"nodeInfo":{"containerRuntimeVersion":"containerd://2","machineID":"abc"}}}]}`)
)

// checkBounds fails t when d holds more than a discovery keeps.
func checkBounds(t testing.TB, d Discovery) {
	t.Helper()
	if len(d.Kinds) > maxKinds || len(d.Rules) > maxRules || len(d.Nodes) > maxNodes {
		t.Fatalf("a discovery kept %d kinds, %d rules, %d nodes", len(d.Kinds), len(d.Rules), len(d.Nodes))
	}
	for _, s := range append([]string{d.Version}, d.Kinds...) {
		// "group/version Kind": two bounded names and a space.
		if len(s) > 2*maxNameSize+1 {
			t.Fatalf("a discovery kept a %d-byte name", len(s))
		}
	}
	for _, n := range d.Nodes {
		if len(n.Name) > maxNameSize || len(n.ContainerRuntime) > maxNameSize || len(n.MachineID) > maxNameSize {
			t.Fatalf("a discovery kept an unbounded node: %+v", n)
		}
	}
	for _, r := range d.Rules {
		if strings.Count(r, ",") > 2*maxRuleEntries {
			t.Fatalf("a discovery kept a rule of %d entries", strings.Count(r, ","))
		}
	}
}

// FuzzDiscover feeds every discovery answer from the fuzzer: Discover
// either refuses or returns a discovery within its bounds, and never
// panics.
func FuzzDiscover(f *testing.F) {
	f.Add(goodVersion, goodAPIs, goodResources, goodRules, goodNodes)
	f.Add([]byte(`{}`), []byte(`{"groups":null}`), []byte(`{"resources":[{"name":"","kind":""}]}`), []byte(`{"status":{"resourceRules":[1,"x",{"verbs":null}]}}`), []byte(`{"items":[{}]}`))
	f.Add([]byte(`not json`), []byte(`[]`), []byte(`{"groupVersion":"`+strings.Repeat("g", 4096)+`/v1","resources":[{"name":"x","kind":"`+strings.Repeat("K", 4096)+`"}]}`), []byte(`null`), []byte(`{"items":"x"}`))
	disc := newFakeDiscovery(f)
	s := disc.session(f)
	f.Fuzz(func(t *testing.T, version, apis, resources, rules, nodes []byte) {
		disc.set(discoveryAnswers{version: version, apis: apis, resources: resources, rules: rules, nodes: nodes})
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		d, err := s.Discover(ctx)
		if err == nil {
			checkBounds(t, d)
		}
	})
}

// TestDiscover_AHostileRuleIsBounded is a review naming 2,000 groups and
// 2,000 resources in one rule: four million entries as written, which the
// discovery keeps at maxRuleEntries, marking the rules incomplete.
func TestDiscover_AHostileRuleIsBounded(t *testing.T) {
	names := func(prefix string) string {
		var b strings.Builder
		for i := range 2000 {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, "%q", fmt.Sprintf("%s%d", prefix, i))
		}
		return b.String()
	}
	disc := newFakeDiscovery(t)
	disc.set(discoveryAnswers{version: goodVersion, apis: goodAPIs, resources: goodResources, nodes: goodNodes,
		rules: []byte(`{"apiVersion":"authorization.k8s.io/v1","kind":"SelfSubjectRulesReview","status":{"resourceRules":[{"verbs":[` + names("v") + `],"apiGroups":[` + names("g") + `],"resources":[` + names("r") + `]}]}}`)})
	begun := time.Now()
	d, err := disc.session(t).Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	checkBounds(t, d)
	if !d.RulesIncomplete {
		t.Error("a rule cut to its bound was not marked incomplete")
	}
	if took := time.Since(begun); took > 10*time.Second {
		t.Errorf("a hostile rule took %s to read", took)
	}
}

// TestDiscover_AnswersPastTheBudgetAreRefused proves the byte budget ends
// a discovery whose answers are too large, whether the server declares the
// length or streams the body.
func TestDiscover_AnswersPastTheBudgetAreRefused(t *testing.T) {
	disc := newFakeDiscovery(t)
	disc.set(discoveryAnswers{version: goodVersion, apis: goodAPIs, resources: goodResources, rules: goodRules, nodes: goodNodes})
	s := disc.session(t)
	if _, err := s.Discover(context.Background()); err != nil {
		t.Fatalf("a well-formed discovery: %v", err)
	}

	// The same server against a budget smaller than its answers.
	d, _, err := boundedClients(s.Config, 16)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ServerVersionWithContext(context.Background()); err == nil || !strings.Contains(err.Error(), "larger than 16 bytes") {
		t.Fatalf("an answer past the budget: %v, want a refusal naming the bound", err)
	}

	// A streamed answer, with no declared length, is cut where it crosses.
	streamed := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"major":"1","gitVersion":"`))
		w.(http.Flusher).Flush()
		for range 64 {
			_, _ = w.Write([]byte(strings.Repeat("x", 1024)))
			w.(http.Flusher).Flush()
		}
		_, _ = w.Write([]byte(`"}`))
	}))
	defer streamed.Close()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: streamed.Certificate().Raw})
	cfg, err := Config(Endpoint{Server: streamed.URL, CAPEM: ca}, Identity{Token: "t"}, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	d, _, err = boundedClients(cfg, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.ServerVersionWithContext(context.Background()); err == nil || !strings.Contains(err.Error(), "larger than 4096 bytes") {
		t.Fatalf("a streamed answer past the budget: %v, want a refusal naming the bound", err)
	}
}

// TestDiscover_AServersErrorTextIsBounded is an API server answering with
// two megabytes that do not decode, holding a terminal escape: the error
// keeps a bounded, escaped part of it.
func TestDiscover_AServersErrorTextIsBounded(t *testing.T) {
	disc := newFakeDiscovery(t)
	disc.set(discoveryAnswers{version: []byte("\x1b]0;owned\x07" + strings.Repeat("x", 2<<20)), apis: goodAPIs, resources: goodResources, rules: goodRules, nodes: goodNodes})
	_, err := disc.session(t).Discover(context.Background())
	if err == nil {
		t.Fatal("an undecodable version answer was accepted")
	}
	text := err.Error()
	if len(text) > 2*maxServerErrorText {
		t.Errorf("the error kept %d bytes of the server's answer", len(text))
	}
	if strings.ContainsAny(text, "\x1b\x07") {
		t.Errorf("the error carries the server's control characters: %q", text)
	}
}
