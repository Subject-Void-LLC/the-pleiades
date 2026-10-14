// The failure-mode walk at the port: a request is never resent, a refused
// pull is reported as it is, and a canceled pull returns at once.
package containerengine

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

// countingEngine counts the requests it receives by path (version prefix
// removed) and answers each with answer, which may hold the connection open.
type countingEngine struct {
	creates, pulls atomic.Int32
	answer         func(path string, c net.Conn)
}

func (e *countingEngine) dial(context.Context) (net.Conn, error) {
	c, s := net.Pipe()
	go func() {
		defer s.Close()
		req, err := http.ReadRequest(bufio.NewReader(s))
		if err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, req.Body)
		path := versionedPath.ReplaceAllString(req.URL.Path, "")
		switch path {
		case "/_ping":
			_, _ = s.Write([]byte("HTTP/1.1 200 OK\r\nAPI-Version: 1.44\r\nContent-Length: 2\r\n\r\nOK"))
			return
		case "/containers/create":
			e.creates.Add(1)
		case "/images/create":
			e.pulls.Add(1)
		}
		e.answer(path, s)
	}()
	return c, nil
}

// session opens a session on e allowing ops.
func (e *countingEngine) session(t *testing.T, ops ...Op) *Session {
	t.Helper()
	api, err := newAPI(guard(e.dial, ops))
	if err != nil {
		t.Fatalf("newAPI: %v", err)
	}
	t.Cleanup(func() { _ = api.Close() })
	return &Session{API: api}
}

// TestSession_ACreateIsSentOnceWhenItsCarrierFails is the walk's "a carrier
// that fails after the engine accepted a create". The engine reads the
// create and the connection then ends with no answer, so whether the
// container exists is unknown. The session reports the failure and sends
// nothing again: a retry could create a second container, so finding out is
// the method's own re-inspection by name, never a resend here.
func TestSession_ACreateIsSentOnceWhenItsCarrierFails(t *testing.T) {
	e := &countingEngine{answer: func(string, net.Conn) {}} // read, then hang up
	s := e.session(t, OpContainerCreate)
	_, err := s.API.ContainerCreate(context.Background(), client.ContainerCreateOptions{
		Config: &container.Config{Image: "alpine:3.20.10"},
		Name:   "web",
	})
	if err == nil {
		t.Fatal("a create whose connection ended unanswered reported success")
	}
	if n := e.creates.Load(); n != 1 {
		t.Fatalf("the engine received %d creates, want exactly 1", n)
	}
}

// TestSession_ARegistryRefusalIsNotRetried is the walk's "a registry 429":
// the engine relays the registry's refusal, and the session reports it as
// it is rather than asking again, since every retry spends the pull quota a
// registry counts per address.
func TestSession_ARegistryRefusalIsNotRetried(t *testing.T) {
	body := `{"message":"toomanyrequests: You have reached your pull rate limit."}`
	e := &countingEngine{answer: func(_ string, c net.Conn) {
		_, _ = c.Write([]byte("HTTP/1.1 500 Internal Server Error\r\nContent-Type: application/json\r\nContent-Length: " +
			strconv.Itoa(len(body)) + "\r\n\r\n" + body))
	}}
	s := e.session(t, OpImagePull)
	resp, err := s.API.ImagePull(context.Background(), "alpine:3.20.10", client.ImagePullOptions{})
	if err == nil {
		_ = resp.Close()
		t.Fatal("a refused pull reported success")
	}
	if !strings.Contains(err.Error(), "toomanyrequests") {
		t.Errorf("the refusal %q does not carry the registry's reason", err)
	}
	if n := e.pulls.Load(); n != 1 {
		t.Fatalf("the engine received %d pulls, want exactly 1", n)
	}
}

// TestSession_ACanceledPullReturnsPromptly is the walk's "a pull canceled
// mid-stream": the progress stream never ends on its own, the caller's
// context ends it, the session returns at once, and the pull was sent once.
// The engine may well go on pulling (it keeps what it fetched); a method
// that pulls again later finds the layers already present.
func TestSession_ACanceledPullReturnsPromptly(t *testing.T) {
	e := &countingEngine{answer: func(_ string, c net.Conn) {
		_, _ = c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nTransfer-Encoding: chunked\r\n\r\n"))
		for {
			line := `{"status":"Downloading","progressDetail":{"current":1,"total":100}}` + "\n"
			if _, err := c.Write([]byte(strconv.FormatInt(int64(len(line)), 16) + "\r\n" + line + "\r\n")); err != nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}}
	s := e.session(t, OpImagePull)
	ctx, cancel := context.WithCancel(context.Background())
	resp, err := s.API.ImagePull(ctx, "alpine:3.20.10", client.ImagePullOptions{})
	if err != nil {
		t.Fatalf("ImagePull: %v", err)
	}
	defer resp.Close()
	time.AfterFunc(200*time.Millisecond, cancel)
	begun := time.Now()
	_, err = io.Copy(io.Discard, resp)
	if err == nil || !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("reading a canceled pull ended with %v, want an error after the cancel", err)
	}
	if took := time.Since(begun); took > 5*time.Second {
		t.Errorf("a canceled pull took %s to return", took)
	}
	if n := e.pulls.Load(); n != 1 {
		t.Fatalf("the engine received %d pulls, want exactly 1", n)
	}
}
