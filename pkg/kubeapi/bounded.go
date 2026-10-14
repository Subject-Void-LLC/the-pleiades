// A byte budget on what a discovery reads from an API server, whose
// answers are untrusted: one answer, and all of a discovery's answers
// together, end in an error past their bound rather than being held.
package kubeapi

import (
	"fmt"
	"io"
	"net/http"
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
	return fmt.Errorf("kubeapi: the server's discovery answers are larger than %d bytes", t.max)
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
