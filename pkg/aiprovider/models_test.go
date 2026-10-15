// Tests for listing a provider's models, and for a provider never reached.
package aiprovider_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/aiprovider"
)

// TestModels_EachKindListsItsOwnWay, with the key each sends.
func TestModels_EachKindListsItsOwnWay(t *testing.T) {
	srv := newServer(t, func(n int, w http.ResponseWriter, s seen) {
		switch s.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"model-a"},{"id":"model-b"}]}`)
		case "/v1beta/models":
			fmt.Fprint(w, `{"models":[{"name":"models/gemini-a"}]}`)
		default:
			w.WriteHeader(404)
		}
	})
	for name, tc := range map[string]struct {
		path, key, header, want string
	}{
		"anthropic": {"", "sk-a", "X-Api-Key", "model-a,model-b"},
		"openai":    {"/v1", "sk-o", "Authorization", "model-a,model-b"},
		"google":    {"", "AIza", "X-Goog-Api-Key", "gemini-a"},
	} {
		models, err := kind(t, name).Models(context.Background(), srv.endpoint(t, tc.path, tc.key))
		if err != nil || strings.Join(models, ",") != tc.want {
			t.Errorf("%s: Models = %v, %v", name, models, err)
		}
	}
	for _, r := range srv.requests() {
		if r.Header.Get("X-Api-Key")+r.Header.Get("Authorization")+r.Header.Get("X-Goog-Api-Key") == "" {
			t.Errorf("%s was listed with no key", r.Path)
		}
	}
}

// TestCall_AProviderNeverReachedIsRetriedThenNamed.
func TestCall_AProviderNeverReachedIsRetriedThenNamed(t *testing.T) {
	srv := newServer(t, func(int, http.ResponseWriter, seen) {})
	u, _ := url.Parse(srv.URL + "/v1")
	ep := aiprovider.Endpoint{Base: u, Client: srv.Client()}
	srv.Close()
	var waited sleeps
	_, usage, err := aiprovider.Call(context.Background(), kind(t, "openai"), ep, aiprovider.Request{Model: "m"}, nil, aiprovider.CallOptions{Retries: 1, Sleep: waited.sleep})
	if err == nil || !strings.Contains(err.Error(), "could not be reached") || usage.Retries != 1 {
		t.Errorf("an unreachable provider: %v, %+v", err, usage)
	}
}
