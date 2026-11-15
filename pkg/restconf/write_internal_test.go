// Package restconf: a write whose answer never comes whole, the status line
// a caller hears before the body, and which Location names a post's child.
package restconf

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/httpapi"
)

// TestWrite_ATimeoutAfterSendingSaysTheEffectIsUnknown: the device holds a
// put past the client's deadline; the error says the write may or may not
// have been applied, where a read's timeout does not.
func TestWrite_ATimeoutAfterSendingSaysTheEffectIsUnknown(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	c, err := New(srv.URL, &http.Client{Timeout: 200 * time.Millisecond}, httpapi.AuthNone, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.root, c.rootFound = "/restconf", true
	_, err = c.Write(context.Background(), MethodPut, "", Path{Node("m", "a")}, MediaTypeJSON, []byte("{}"))
	var lost *AnswerLostError
	if !errors.As(err, &lost) {
		t.Fatalf("Write = %v, want an AnswerLostError", err)
	}
	if _, _, err := c.Read(context.Background(), "", Path{Node("m", "a")}, MediaTypeJSON); errors.As(err, &lost) || err == nil {
		t.Errorf("Read = %v, want a timeout that is no lost write", err)
	}
}

// TestWriteThen_HearsTheStatusBeforeTheBody: answered is called with the
// status and the post's Location, and its error is the write's; a status
// outside 2xx is never handed to it.
func TestWriteThen_HearsTheStatusBeforeTheBody(t *testing.T) {
	status := http.StatusCreated
	c := refusingClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "/restconf/data/m:a/b=1")
		w.WriteHeader(status)
	}, httpapi.AuthNone)
	var heard Written
	stop := errors.New("stop")
	_, err := c.WriteThen(context.Background(), MethodPost, "", Path{Node("m", "a")}, MediaTypeJSON, []byte("{}"), func(w Written) error {
		heard = w
		return stop
	})
	if !errors.Is(err, stop) || heard.Status != http.StatusCreated || !heard.Location.Child(Path{Node("m", "a")}) {
		t.Errorf("WriteThen = %v, heard %+v", err, heard)
	}
	status = http.StatusBadRequest
	called := false
	if _, err := c.WriteThen(context.Background(), MethodPost, "", Path{Node("m", "a")}, MediaTypeJSON, []byte("{}"), func(Written) error { called = true; return nil }); err == nil || called {
		t.Errorf("a 400 = %v, answered called %v", err, called)
	}
}

// TestPath_Child: one node more than its parent, the same nodes before it by
// module (inherited or written out), name and keys.
func TestPath_Child(t *testing.T) {
	parent := Path{Node("m", "a"), Node("", "b").Entry("1")}
	for name, tc := range map[string]struct {
		p    Path
		want bool
	}{
		"a child":                 {Path{Node("m", "a"), Node("", "b").Entry("1"), Node("", "c")}, true},
		"a child, module written": {Path{Node("m", "a"), Node("m", "b").Entry("1"), Node("x", "c")}, true},
		"the parent":              {parent, false},
		"a grandchild":            {Path{Node("m", "a"), Node("", "b").Entry("1"), Node("", "c"), Node("", "d")}, false},
		"a sibling's child":       {Path{Node("m", "a"), Node("", "b").Entry("2"), Node("", "c")}, false},
		"another module":          {Path{Node("x", "a"), Node("", "b").Entry("1"), Node("", "c")}, false},
		"another name":            {Path{Node("m", "a"), Node("", "z").Entry("1"), Node("", "c")}, false},
	} {
		if got := tc.p.Child(parent); got != tc.want {
			t.Errorf("%s: Child = %v, want %v", name, got, tc.want)
		}
	}
}
