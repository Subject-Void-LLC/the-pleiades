// Tests that a session bounds each answer it reads, and leaves a stream to
// the method that opens it.
package kubeapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestSession_AnAnswerPastItsBoundIsRefused: a list larger than one answer's
// bound fails, naming the bound, and one within it is read whole.
func TestSession_AnAnswerPastItsBoundIsRefused(t *testing.T) {
	was := maxSessionResponseBytes
	t.Cleanup(func() { maxSessionResponseBytes = was })
	maxSessionResponseBytes = 4 << 10
	padding := ""
	api := newFakeAPI(t, func(_ int32, w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"apiVersion":"v1","kind":"ServiceList","metadata":{"resourceVersion":%q},"items":[]}`, padding)
	})
	s := api.session(t, Identity{Token: "t0k3n-for-the-bound"})

	if _, err := s.Dynamic.Resource(testServices).Namespace("default").List(context.Background(), metav1.ListOptions{}); err != nil {
		t.Fatalf("an answer within the bound = %v", err)
	}
	padding = strings.Repeat("x", 8<<10)
	_, err := s.Dynamic.Resource(testServices).Namespace("default").List(context.Background(), metav1.ListOptions{})
	if err == nil || !strings.Contains(err.Error(), "one answer from the server is larger than") {
		t.Fatalf("an answer past the bound = %v, want it refused", err)
	}
}

// TestResponseBound_StreamsPassWhole: a watch, a followed log and an
// upgraded connection are streams the method that opens them bounds, so a
// body past one answer's bound passes through whole; an ordinary answer of
// the same size is cut off.
func TestResponseBound_StreamsPassWhole(t *testing.T) {
	body := strings.Repeat("e", 8<<10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }))
	t.Cleanup(srv.Close)
	client := &http.Client{Transport: &responseBoundTransport{next: http.DefaultTransport, max: 4 << 10}}
	read := func(path string, upgrade bool) (int, error) {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+path, nil)
		if upgrade {
			req.Header.Set("Upgrade", "websocket")
		}
		resp, err := client.Do(req)
		if err != nil {
			return 0, err
		}
		defer func() { _ = resp.Body.Close() }()
		b, err := io.ReadAll(resp.Body)
		return len(b), err
	}
	for _, tc := range []struct {
		path    string
		upgrade bool
	}{
		{"/api/v1/services?watch=true", false},
		{"/api/v1/watch/services", false},
		{"/api/v1/namespaces/default/pods/p/log?follow=true", false},
		{"/api/v1/namespaces/default/pods/p/exec", true},
	} {
		if n, err := read(tc.path, tc.upgrade); err != nil || n != len(body) {
			t.Errorf("%s (upgrade %v): read %d, %v; want the whole stream", tc.path, tc.upgrade, n, err)
		}
	}
	if _, err := read("/api/v1/services", false); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("an ordinary answer past the bound = %v, want it refused", err)
	}
}

// TestResponseBound_ADeclaredLengthPastTheBoundIsRefusedUnread: an answer
// whose Content-Length already exceeds the bound is refused before any of
// it is read.
func TestResponseBound_ADeclaredLengthPastTheBoundIsRefusedUnread(t *testing.T) {
	body := strings.Repeat("e", 8<<10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	client := &http.Client{Transport: &responseBoundTransport{next: http.DefaultTransport, max: 4 << 10}}
	resp, err := client.Get(srv.URL + "/api/v1/services")
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("an answer declaring more than the bound was accepted")
	}
	if !strings.Contains(err.Error(), "larger than 4096 bytes") {
		t.Fatalf("err = %v, want the bound named", err)
	}
}
