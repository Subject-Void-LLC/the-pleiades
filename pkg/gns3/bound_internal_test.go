// Tests of the bound on a GNS3 server's answer: read to exactly
// maxResponse, refused one byte past it, and never read further than that
// however long the server goes on.
package gns3

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// padded is body followed by spaces, JSON's own blank, to size bytes: a
// client that cut it at its bound would still read a whole, valid answer.
func padded(body string, size int) []byte {
	return []byte(body + strings.Repeat(" ", size-len(body)))
}

// TestRaw_AnAnswerAtTheBoundIsReadAndOneByteMoreIsNot: a version, project
// or node answer of exactly maxResponse bytes is read; one byte more is
// refused, naming the call and the bound, though it is valid JSON either
// way.
func TestRaw_AnAnswerAtTheBoundIsReadAndOneByteMoreIsNot(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		path, body string
		call       func(*Client) error
	}{
		"version":  {"/v2/version", `{"version":"2.2.61"}`, func(c *Client) error { _, err := c.Connect(ctx); return err }},
		"projects": {"/v2/projects", `[{"project_id":"p","name":"lab"}]`, func(c *Client) error { _, err := c.Projects(ctx); return err }},
		"nodes":    {"/v2/projects/p/nodes", `[{"node_id":"n1","name":"R1"}]`, func(c *Client) error { _, err := c.Nodes(ctx, "p"); return err }},
	} {
		for _, extra := range []int{0, 1} {
			answer := padded(tc.body, maxResponse+extra)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case tc.path:
					_, _ = w.Write(answer)
				case "/v2/version":
					_, _ = w.Write([]byte(`{"version":"2.2.61"}`))
				default:
					http.NotFound(w, r)
				}
			}))
			c, err := New(srv.URL, srv.Client(), Credentials{})
			if err != nil {
				t.Fatal(err)
			}
			if tc.path != "/v2/version" {
				if _, err := c.Connect(ctx); err != nil {
					t.Fatalf("%s: Connect: %v", name, err)
				}
			}
			err = tc.call(c)
			srv.Close()
			switch {
			case extra == 0 && err != nil:
				t.Errorf("%s: an answer of exactly %d bytes was refused: %v", name, maxResponse, err)
			case extra == 1 && (err == nil || !strings.Contains(err.Error(), tc.path) || !strings.Contains(err.Error(), strconv.Itoa(maxResponse))):
				t.Errorf("%s: an answer of %d bytes = %v, want a refusal naming %s and the bound", name, maxResponse+1, err, tc.path)
			}
		}
	}
}

// TestRaw_ReadsNoMoreThanItsBound: an answer that does not end (here four
// times maxResponse) is refused once it passes maxResponse, and the client
// stops reading there. What the server managed to write is the proof:
// with the read unbounded it writes all of it; bounded, it writes the
// bound plus what the sockets between them buffer.
func TestRaw_ReadsNoMoreThanItsBound(t *testing.T) {
	const total = 4 * maxResponse
	wrote := make(chan int, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/version" {
			http.NotFound(w, r)
			return
		}
		chunk := []byte(`{"version":"2.2.61"` + strings.Repeat(" ", 64<<10-19))
		n := 0
		for n < total {
			k, err := w.Write(chunk)
			n += k
			if err != nil {
				break
			}
			chunk = []byte(strings.Repeat(" ", 64<<10))
		}
		wrote <- n
	}))
	defer srv.Close()
	c, err := New(srv.URL, srv.Client(), Credentials{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Connect(context.Background()); err == nil || !strings.Contains(err.Error(), "over") {
		t.Fatalf("Connect to an answer with no end = %v, want it refused as over the bound", err)
	}
	select {
	case n := <-wrote:
		// The sockets of a loopback connection buffer a few megabytes.
		if slack := 16 << 20; n > maxResponse+slack {
			t.Errorf("the server wrote %d bytes before the client stopped reading, want at most %d", n, maxResponse+slack)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the server was still writing: the client never stopped reading")
	}
}
