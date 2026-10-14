// Fuzz targets for the guard's request-line decision and for reading an
// engine's identity from an untrusted answer.
package containerengine

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// execOnly is what an exec-only session may send, written independently of
// the routes table so the fuzz checks the table rather than restating it.
var execOnly = regexp.MustCompile(`^(POST /(v[0-9.]+/)?containers/[^/]+/exec|POST /(v[0-9.]+/)?exec/[^/]+/start|GET /(v[0-9.]+/)?exec/[^/]+/json|(GET|HEAD) /(v[0-9.]+/)?_ping)$`)

// FuzzGuard_RequestLine proves the guard never panics on any request line
// and that whatever an exec-only session lets through is one of the three
// exec requests or the version ping, never anything that could create,
// change or remove.
func FuzzGuard_RequestLine(f *testing.F) {
	for _, s := range []string{
		"POST /v1.44/containers/web/exec HTTP/1.1\r\n",
		"POST /v1.44/containers/create HTTP/1.1\r\n",
		"POST /v1.44/containers/web%2F..%2Fcreate/exec HTTP/1.1\r\n",
		"GET http://engine/info HTTP/1.1\r\n",
		"POST /v1.44/exec/abc/start?x=/../create HTTP/1.1\r\n",
		"GET /_ping HTTP/1.1\r\n",
		"\r\n",
		"POST  /x HTTP/1.1\r\n",
		strings.Repeat("A", 200),
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		client, server := net.Pipe()
		defer server.Close()
		received := make(chan string, 1)
		go func() {
			buf := make([]byte, 64<<10)
			n, _ := server.Read(buf)
			received <- string(buf[:n])
		}()
		g := &guardedConn{Conn: client, ops: []Op{OpExecCreate, OpExecStart, OpExecInspect}}
		_, err := g.Write([]byte(line))
		_ = g.Close()
		if err != nil {
			return
		}
		got := ""
		select {
		case got = <-received:
		case <-time.After(time.Second):
		}
		if got == "" {
			// The line was incomplete, so nothing was forwarded.
			return
		}
		first, _, _ := strings.Cut(got, "\r\n")
		fields := strings.Fields(first)
		if len(fields) != 3 {
			t.Fatalf("forwarded a malformed line %q", first)
		}
		path := fields[1]
		if i := strings.IndexAny(path, "?#"); i >= 0 {
			path = path[:i]
		}
		if !execOnly.MatchString(fields[0] + " " + path) {
			t.Fatalf("an exec-only session forwarded %q", first)
		}
	})
}

// FuzzReadIdentity_Answer proves an identity read never panics or hangs on
// any answer, for either engine: whatever answers on a socket is untrusted.
func FuzzReadIdentity_Answer(f *testing.F) {
	for _, s := range []string{
		`{"ID":"abc","ServerVersion":"29.9.0"}`,
		`{"host":{"hostname":"h"},"store":{"graphRoot":"/g","runRoot":"/r"},"version":{"Version":"5.8.7"}}`,
		`{"ID":""}`, `[]`, `{`, "", `{"store":{"graphRoot":5}}`,
		`{"ID":"` + strings.Repeat("a", 4096) + `"}`,
	} {
		f.Add(s, true)
		f.Add(s, false)
	}
	f.Fuzz(func(t *testing.T, body string, docker bool) {
		dial := func(context.Context) (net.Conn, error) {
			c, s := net.Pipe()
			go func() {
				defer s.Close()
				_, _ = http.ReadRequest(bufio.NewReader(s))
				fmt.Fprintf(s, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
			}()
			return c, nil
		}
		kind := capability.EnginePodman
		if docker {
			kind = capability.EngineDocker
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		id, err := ReadIdentity(ctx, dial, kind)
		if err == nil && id.ID == "" {
			t.Fatal("an identity read succeeded with no identity")
		}
	})
}
