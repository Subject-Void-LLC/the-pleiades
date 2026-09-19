// Package catalyst_test: tests of the net.catalyst checks.
package catalyst_test

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestReadOnlyChecks_OnlyRead runs each net.catalyst method's registered
// check against the replayed sandbox with every request recorded: the
// check sends nothing but GETs and the one token POST, reports no change,
// and emits exactly the facts the real run emits from the same controller.
func TestReadOnlyChecks_OnlyRead(t *testing.T) {
	for _, fqcn := range []string{"net.catalyst.device_facts", "net.catalyst.reachability", "net.catalyst.site_facts", "net.catalyst.tag_facts"} {
		t.Run(fqcn, func(t *testing.T) {
			d, ok := collection.Lookup(fqcn)
			if !ok || !d.Manifest.SupportsCheck || d.Check == nil {
				t.Fatalf("%s does not declare a check", fqcn)
			}
			srv, controller := newSandbox(t)
			var mu sync.Mutex
			var sent []string
			replay := srv.Config.Handler
			srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				sent = append(sent, r.Method+" "+r.URL.Path)
				mu.Unlock()
				replay.ServeHTTP(w, r)
			})

			checked := newFakeContext(validSecrets())
			result, err := d.Check(context.Background(), checked, controller, map[string]any{})
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			if result.Changed {
				t.Error("a check of a read-only method predicted a change")
			}
			mu.Lock()
			requests := append([]string(nil), sent...)
			mu.Unlock()
			if len(requests) == 0 {
				t.Fatal("the check sent nothing, so it read nothing")
			}
			for _, req := range requests {
				if req != "POST /dna/system/api/v1/auth/token" && !strings.HasPrefix(req, "GET ") {
					t.Errorf("the check sent %q, which is not a read", req)
				}
			}

			ran := newFakeContext(validSecrets())
			if _, err := d.Invoke(context.Background(), ran, controller, map[string]any{}); err != nil {
				t.Fatalf("the real run: %v", err)
			}
			if !reflect.DeepEqual(checked.facts, ran.facts) {
				t.Errorf("the check emitted %v, the real run %v", checked.facts, ran.facts)
			}
		})
	}
}
