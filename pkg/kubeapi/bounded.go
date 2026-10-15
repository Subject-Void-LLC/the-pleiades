// A byte budget on what a discovery reads from an API server, whose
// answers are untrusted: one answer, and all of a discovery's answers
// together, end in an error past their bound rather than being held.
package kubeapi

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// maxDiscoveryBytes bounds everything one discovery reads: the version,
// every group version's resources, the rules review and the node list. A
// large real cluster's discovery is a few megabytes; a server answering
// more, or naming thousands of group versions to be fetched one by one,
// is refused rather than held in memory.
const maxDiscoveryBytes = 128 << 20

// budget is the bytes a discovery may still read, shared by every answer.
type budget struct{ left atomic.Int64 }

// boundedClients builds a discovery client and a dynamic client from cfg
// whose answers together draw on one budget of max bytes.
func boundedClients(cfg *rest.Config, max int64) (discovery.DiscoveryInterfaceWithContext, dynamic.Interface, error) {
	b := &budget{}
	b.left.Store(max)
	bounded := rest.CopyConfig(cfg)
	bounded.Wrap(func(next http.RoundTripper) http.RoundTripper {
		return &budgetTransport{next: next, budget: b, max: max}
	})
	disc, err := discovery.NewDiscoveryClientForConfig(bounded)
	if err != nil {
		return nil, nil, fmt.Errorf("kubeapi: building the discovery client: %w", err)
	}
	dyn, err := dynamic.NewForConfig(bounded)
	if err != nil {
		return nil, nil, fmt.Errorf("kubeapi: building the dynamic client: %w", err)
	}
	return disc, dyn, nil
}

// budgetTransport draws every response body it carries from one budget.
type budgetTransport struct {
	next   http.RoundTripper
	budget *budget
	max    int64
	// what names the answers the budget covers, for its error; discovery's
	// when empty.
	what string
}

func (t *budgetTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(r)
	if err != nil || resp.Body == nil {
		return resp, err
	}
	if resp.ContentLength > t.budget.left.Load() {
		_ = resp.Body.Close()
		return nil, t.exceeded()
	}
	resp.Body = &budgetBody{body: resp.Body, t: t}
	return resp, nil
}

// exceeded is the error an answer past the budget ends in.
func (t *budgetTransport) exceeded() error {
	what := t.what
	if what == "" {
		what = "the server's discovery answers are"
	}
	return fmt.Errorf("kubeapi: %s larger than %d bytes", what, t.max)
}

// maxSessionResponseBytes bounds one answer a session's clients read: a get,
// a list, an apply's response, a log read without follow. A variable so a
// test can watch it end an answer without making 64 MiB.
var maxSessionResponseBytes int64 = 64 << 20

// responseBoundTransport bounds each response body it carries to max bytes,
// except a stream, which the method that opens it bounds (sessionStream).
// Discovery's single total budget is the wrong shape here: a session lives
// for a task, and a total would end a long watch after a fixed number of
// bytes however healthy it was.
type responseBoundTransport struct {
	next http.RoundTripper
	max  int64
}

func (t *responseBoundTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := t.next.RoundTrip(r)
	if err != nil || resp.Body == nil || sessionStream(r, resp) {
		return resp, err
	}
	bound := &budgetTransport{budget: &budget{}, max: t.max, what: "one answer from the server is"}
	bound.budget.left.Store(t.max)
	if resp.ContentLength > t.max {
		_ = resp.Body.Close()
		return nil, bound.exceeded()
	}
	resp.Body = &budgetBody{body: resp.Body, t: bound}
	return resp, nil
}

// sessionStream reports whether a response is a stream rather than one
// answer: a watch (?watch=true, or the old /watch/ paths), a followed log
// (?follow=true), or a connection upgraded for exec, attach or port-forward.
func sessionStream(r *http.Request, resp *http.Response) bool {
	q := r.URL.Query()
	return q.Get("watch") == "true" || q.Get("watch") == "1" || q.Get("follow") == "true" ||
		strings.Contains(r.URL.Path, "/watch/") || r.Header.Get("Upgrade") != "" ||
		resp.StatusCode == http.StatusSwitchingProtocols
}

// budgetBody is a response body that ends in an error once its budget is
// spent.
type budgetBody struct {
	body io.ReadCloser
	t    *budgetTransport
}

func (b *budgetBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	if n > 0 && b.t.budget.left.Add(-int64(n)) < 0 {
		return 0, b.t.exceeded()
	}
	return n, err
}

func (b *budgetBody) Close() error { return b.body.Close() }
