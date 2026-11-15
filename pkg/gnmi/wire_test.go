// Package gnmi_test: what the client puts on the wire, read back from the
// bytes it sent. gRPC metadata travels in HTTP/2 HEADERS frames, HPACK-coded
// (often Huffman-coded too), so a credential in it is invisible to a search
// of the raw bytes; these tests decode every frame and look at each header
// field, which is the only reading that can say where a credential went.
package gnmi_test

import (
	"bytes"
	"io"
	"net"
	"strings"
	"sync"
	"testing"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
	"google.golang.org/grpc/codes"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi/gnmitest"
)

// recorder is a TCP proxy to one server keeping every byte its client sent.
type recorder struct {
	lis  net.Listener
	mu   sync.Mutex
	sent bytes.Buffer
	wg   sync.WaitGroup
}

// newRecorder proxies to target, closed when t ends.
func newRecorder(t *testing.T, target string) *recorder {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	r := &recorder{lis: lis}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		for {
			client, err := lis.Accept()
			if err != nil {
				return
			}
			server, err := net.Dial("tcp", target)
			if err != nil {
				_ = client.Close()
				return
			}
			r.wg.Add(2)
			go func() {
				defer r.wg.Done()
				_, _ = io.Copy(io.MultiWriter(server, r), client)
				_ = server.Close()
			}()
			go func() {
				defer r.wg.Done()
				_, _ = io.Copy(client, server)
				_ = client.Close()
			}()
		}
	}()
	t.Cleanup(func() {
		_ = lis.Close()
		r.wg.Wait()
	})
	return r
}

// Write keeps what the client sent.
func (r *recorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sent.Write(p)
}

// fields decodes every header field the client sent, in order.
func (r *recorder) fields(t *testing.T) ([]hpack.HeaderField, []byte) {
	t.Helper()
	r.mu.Lock()
	raw := append([]byte(nil), r.sent.Bytes()...)
	r.mu.Unlock()
	if !bytes.HasPrefix(raw, []byte(http2.ClientPreface)) {
		t.Fatalf("the client sent no HTTP/2 preface (%d bytes)", len(raw))
	}
	framer := http2.NewFramer(io.Discard, bytes.NewReader(raw[len(http2.ClientPreface):]))
	framer.ReadMetaHeaders = hpack.NewDecoder(4096, nil)
	var out []hpack.HeaderField
	for {
		frame, err := framer.ReadFrame()
		if err != nil {
			break
		}
		if headers, ok := frame.(*http2.MetaHeadersFrame); ok {
			out = append(out, headers.Fields...)
		}
	}
	return out, raw
}

// TestWire_ThePasswordTravelsOnlyAsItsOwnMetadata sends a password login over
// a plaintext connection the record accepted, through a recording proxy, and
// holds the password to the "password" header field alone: never the path,
// the authority or any other field, and never visible to a byte search, which
// is why the frames are decoded.
func TestWire_ThePasswordTravelsOnlyAsItsOwnMetadata(t *testing.T) {
	const password = "Hpack-hidden-pw-31f0"
	s := gnmitest.New(t, gnmitest.Options{Plaintext: true, Username: testUser, Password: password})
	rec := newRecorder(t, s.Addr())
	device := gnmitest.NewDevice(t, s)
	device.Target, device.AllowPlaintext = rec.lis.Addr().String(), true
	c, _, err := gnmi.ClientFor(device, "router1", gnmitest.Secrets(s))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Capabilities(ctxOf(t)); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	fields, raw := rec.fields(t)
	found := 0
	for _, f := range fields {
		switch {
		case f.Name == "password" && f.Value == password:
			found++
		case strings.Contains(f.Value, password) || strings.Contains(f.Name, password):
			t.Errorf("header %s = %q carries the password", f.Name, f.Value)
		case f.Name == ":path" && f.Value != "/gnmi.gNMI/Capabilities":
			t.Errorf(":path = %q", f.Value)
		}
	}
	if found != 1 {
		t.Fatalf("the password header was decoded %d times in %v, want once: the decoding is the control", found, fields)
	}
	if bytes.Contains(raw, []byte(password)) {
		t.Log("the password also appears in the raw bytes (HPACK sent it as a literal)")
	}
}

// TestWire_ATokenNeverReachesThePath sends a bearer token and holds it to
// the authorization field alone: gRPC has no query string, and the token is
// in no path, authority or user agent.
func TestWire_ATokenNeverReachesThePath(t *testing.T) {
	const token = "tok-wire-0a1b2c3d4e5f"
	s := gnmitest.New(t, gnmitest.Options{Plaintext: true, Token: token})
	rec := newRecorder(t, s.Addr())
	device := gnmitest.NewDevice(t, s)
	device.Target, device.AllowPlaintext = rec.lis.Addr().String(), true
	c, _, err := gnmi.ClientFor(device, "router1", gnmitest.Secrets(s))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(ctxOf(t), gnmi.GetRequest{Paths: []gnmi.Path{mustPath(t, "/system")}}); gnmi.Code(err) != codes.NotFound {
		t.Fatalf("Get = %v, want the server's NotFound", err)
	}
	_ = c.Close()
	fields, _ := rec.fields(t)
	authorized := 0
	for _, f := range fields {
		switch {
		case f.Name == "authorization" && f.Value == "Bearer "+token:
			authorized++
		case strings.Contains(f.Value, token):
			t.Errorf("header %s = %q carries the token", f.Name, f.Value)
		}
	}
	if authorized != 1 {
		t.Fatalf("authorization decoded %d times in %v, want once", authorized, fields)
	}
}
