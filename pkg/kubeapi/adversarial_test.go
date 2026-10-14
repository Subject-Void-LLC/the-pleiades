// The failure-mode walk for the Kubernetes port, against an in-memory API
// server that counts what it is sent: refusals are not retried, a server's
// 429 is waited out as it asks, a token nearing expiry is minted again, and
// a canceled watch ends at once.
package kubeapi

import (
	"context"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// testServices is the resource the walk reads.
var testServices = schema.GroupVersionResource{Version: "v1", Resource: "services"}

// fakeAPI is an API server answering the services list with answer, and
// counting the requests to it.
type fakeAPI struct {
	srv    *httptest.Server
	hits   atomic.Int32
	tokens sync.Map
	answer func(n int32, w http.ResponseWriter, r *http.Request)
}

func newFakeAPI(t *testing.T, answer func(n int32, w http.ResponseWriter, r *http.Request)) *fakeAPI {
	t.Helper()
	f := &fakeAPI{answer: answer}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/services") {
			http.NotFound(w, r)
			return
		}
		f.tokens.Store(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), true)
		f.answer(f.hits.Add(1), w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// session reaches f with id.
func (f *fakeAPI) session(t *testing.T, id Identity) *Session {
	t.Helper()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.srv.Certificate().Raw})
	s, err := NewSession(Endpoint{Server: f.srv.URL, CAPEM: ca}, id, Limits{}, "default")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// emptyList is a services list with nothing in it.
func emptyList(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"apiVersion":"v1","kind":"ServiceList","metadata":{},"items":[]}`)
}

// status writes a Kubernetes Status error.
func status(w http.ResponseWriter, code int, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	fmt.Fprintf(w, `{"apiVersion":"v1","kind":"Status","status":"Failure","reason":%q,"code":%d,"message":"walk"}`, reason, code)
}

// TestWalk_ARefusedCredentialIsNotRetried proves a 401 and a 403 are each
// sent once and reported as what they mean: like a refused SSH login, no
// retry changes the answer, and retrying a refused credential can lock an
// account.
func TestWalk_ARefusedCredentialIsNotRetried(t *testing.T) {
	for code, want := range map[int]error{http.StatusUnauthorized: ErrCredentialRefused, http.StatusForbidden: ErrForbidden} {
		reason := map[int]string{401: "Unauthorized", 403: "Forbidden"}[code]
		f := newFakeAPI(t, func(_ int32, w http.ResponseWriter, _ *http.Request) { status(w, code, reason) })
		_, err := f.session(t, Identity{Token: "t"}).Dynamic.Resource(testServices).Namespace("default").List(context.Background(), metav1.ListOptions{})
		if !errors.Is(Explain(err), want) {
			t.Errorf("a %d answered %v, want %v", code, err, want)
		}
		if n := f.hits.Load(); n != 1 {
			t.Errorf("a %d was sent %d times, want once", code, n)
		}
	}
}

// TestWalk_AServers429IsWaitedOut proves API Priority and Fairness's 429 is
// honored: the request waits the Retry-After the server asked for, once,
// then succeeds, rather than failing the task or hammering the server.
func TestWalk_AServers429IsWaitedOut(t *testing.T) {
	f := newFakeAPI(t, func(n int32, w http.ResponseWriter, _ *http.Request) {
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			status(w, http.StatusTooManyRequests, "TooManyRequests")
			return
		}
		emptyList(w)
	})
	begun := time.Now()
	_, err := f.session(t, Identity{Token: "t"}).Dynamic.Resource(testServices).Namespace("default").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("a request answered 429 then 200 failed: %v", err)
	}
	if took := time.Since(begun); took < 900*time.Millisecond {
		t.Errorf("the retry came after %s, sooner than the Retry-After of one second", took)
	}
	if n := f.hits.Load(); n != 2 {
		t.Errorf("the server saw %d requests, want 2: the refused one and the retry", n)
	}
}

// TestWalk_ATokenNearingExpiryIsMintedAgain is "a token that expires
// mid-run": the first token expires inside the minting margin soon after
// it is minted, so the next request mints a fresh one rather than sending
// a token about to lapse, and the server sees both.
func TestWalk_ATokenNearingExpiryIsMintedAgain(t *testing.T) {
	f := newFakeAPI(t, func(_ int32, w http.ResponseWriter, _ *http.Request) { emptyList(w) })
	var mints atomic.Int32
	id := Identity{Mint: func(context.Context) (string, time.Time, error) {
		n := mints.Add(1)
		// Just outside the margin when minted, inside it half a second on.
		return fmt.Sprintf("token-%d", n), time.Now().Add(mintMargin + 300*time.Millisecond), nil
	}}
	s := f.session(t, id)
	list := func() {
		if _, err := s.Dynamic.Resource(testServices).Namespace("default").List(context.Background(), metav1.ListOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	list()
	list()
	if n := mints.Load(); n != 1 {
		t.Fatalf("two requests at once minted %d tokens, want 1", n)
	}
	time.Sleep(500 * time.Millisecond)
	list()
	if n := mints.Load(); n != 2 {
		t.Errorf("a request after the token entered its margin minted %d tokens in all, want 2", n)
	}
	for _, tok := range []string{"token-1", "token-2"} {
		if _, ok := f.tokens.Load(tok); !ok {
			t.Errorf("the server never saw %s", tok)
		}
	}
}

// TestWalk_ACanceledWatchEndsAtOnce is "a watch canceled mid-stream": the
// server keeps the stream open and silent, and canceling the caller's
// context closes the result channel promptly.
func TestWalk_ACanceledWatchEndsAtOnce(t *testing.T) {
	f := newFakeAPI(t, func(_ int32, w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	ctx, cancel := context.WithCancel(context.Background())
	w, err := f.session(t, Identity{Token: "t"}).Dynamic.Resource(testServices).Namespace("default").Watch(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(200*time.Millisecond, cancel)
	begun := time.Now()
	for range w.ResultChan() {
	}
	if took := time.Since(begun); took > 5*time.Second {
		t.Errorf("a canceled watch took %s to end", took)
	}
}
