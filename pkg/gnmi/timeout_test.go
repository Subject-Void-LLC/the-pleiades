// Package gnmi_test: every call's bound in time, against a real gRPC server
// that accepts each call and never answers it, and the keepalive ping that
// closes a connection whose far side went silent under an open stream.
package gnmi_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi/gnmitest"
)

// silentServer is a plaintext server that accepts every call and answers
// none, until the caller's deadline reaches it.
func silentServer(t *testing.T) *gnmitest.Server {
	t.Helper()
	return gnmitest.New(t, gnmitest.Options{Plaintext: true, Hook: func(ctx context.Context, _ string) error {
		<-ctx.Done()
		return ctx.Err()
	}})
}

// plainClient is a client of target over plaintext, sending no credential,
// with the call timeout given, closed when t ends.
func plainClient(t *testing.T, target string, timeout time.Duration) *gnmi.Client {
	t.Helper()
	c, err := gnmi.New(gnmi.Options{Target: target, Plaintext: true, Auth: gnmi.AuthNone, CallTimeout: timeout})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestCalls_AServerThatNeverAnswersEndsAtTheCallTimeout makes every bounded
// call to a server that never answers, under a caller's deadline far past the
// client's bound, and holds each to ending at the bound, saying so.
func TestCalls_AServerThatNeverAnswersEndsAtTheCallTimeout(t *testing.T) {
	const bound = 300 * time.Millisecond
	s := silentServer(t)
	c := plainClient(t, s.Addr(), bound)
	root := []gnmi.Path{mustPath(t, "/")}
	for name, call := range map[string]func(context.Context) error{
		"Capabilities": func(ctx context.Context) error { _, err := c.Capabilities(ctx); return err },
		"Get":          func(ctx context.Context) error { _, err := c.Get(ctx, gnmi.GetRequest{Paths: root}); return err },
		"Set": func(ctx context.Context) error {
			_, err := c.Set(ctx, gnmi.SetRequest{Delete: []gnmi.Path{mustPath(t, "/a")}})
			var lost *gnmi.AnswerLostError
			if err != nil && (!errors.As(err, &lost) || lost.Applied) {
				t.Errorf("Set = %v, want a lost answer, its outcome unknown", err)
			}
			return err
		},
		"System.Time":  func(ctx context.Context) error { _, err := c.Time(ctx); return err },
		"File.Stat":    func(ctx context.Context) error { _, err := c.FileStat(ctx, "/etc/hostname"); return err },
		"Certificates": func(ctx context.Context) error { _, err := c.Certificates(ctx); return err },
		"ONCE": func(ctx context.Context) error {
			_, err := c.SubscribeOnce(ctx, gnmi.SubscribeRequest{Paths: root})
			return err
		},
		"POLL": func(ctx context.Context) error {
			p, _, err := c.SubscribePoll(ctx, gnmi.SubscribeRequest{Paths: root})
			if p != nil {
				p.Close()
			}
			return err
		},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		start := time.Now()
		err := call(ctx)
		took := time.Since(start)
		cancel()
		if err == nil || !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "did not answer within 300ms") {
			t.Errorf("%s = %v, want the client's bound named", name, err)
		}
		if took > 3*time.Second {
			t.Errorf("%s took %s, want it ended at the %s bound", name, took, bound)
		}
	}
}

// TestNew_TakesTheDefaultCallTimeout holds a client given none to
// DefaultCallTimeout, and refuses a negative one.
func TestNew_TakesTheDefaultCallTimeout(t *testing.T) {
	c := plainClient(t, "127.0.0.1:1", 0)
	if got := gnmi.CallTimeoutOf(c); got != gnmi.DefaultCallTimeout {
		t.Errorf("call timeout = %s, want %s", got, gnmi.DefaultCallTimeout)
	}
	if _, err := gnmi.New(gnmi.Options{Target: "127.0.0.1:1", Plaintext: true, Auth: gnmi.AuthNone, CallTimeout: -time.Second}); err == nil {
		t.Error("a negative call timeout was accepted")
	}
}

// silentProxy forwards TCP to one server until it goes silent, after which
// it drops every byte both ways and keeps every connection open: what a far
// side that died without closing its connection looks like.
type silentProxy struct {
	lis    net.Listener
	silent atomic.Bool
	mu     sync.Mutex
	conns  []net.Conn
	wg     sync.WaitGroup
}

// newSilentProxy proxies to target, every connection closed when t ends.
func newSilentProxy(t *testing.T, target string) *silentProxy {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &silentProxy{lis: lis}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
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
			p.mu.Lock()
			p.conns = append(p.conns, client, server)
			p.mu.Unlock()
			p.wg.Add(2)
			go p.pipe(server, client)
			go p.pipe(client, server)
		}
	}()
	t.Cleanup(func() {
		_ = lis.Close()
		p.mu.Lock()
		for _, c := range p.conns {
			_ = c.Close()
		}
		p.mu.Unlock()
		p.wg.Wait()
	})
	return p
}

// pipe copies src to dst until either ends, dropping what it reads once the
// proxy is silent.
func (p *silentProxy) pipe(dst, src net.Conn) {
	defer p.wg.Done()
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 && !p.silent.Load() {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// TestStream_AConnectionThatGoesSilentIsClosed opens an ON_CHANGE stream
// through a proxy that goes silent after the server's sync, and holds the
// stream to ending with an error once the keepalive ping goes unanswered,
// long before the caller's own deadline would end it.
func TestStream_AConnectionThatGoesSilentIsClosed(t *testing.T) {
	gnmi.SetKeepalive(t, 10*time.Second, time.Second)
	s := gnmitest.New(t, gnmitest.Options{Plaintext: true})
	s.Put(t, "/system/config", `{"hostname":"r1"}`)
	proxy := newSilentProxy(t, s.Addr())
	c := plainClient(t, proxy.lis.Addr().String(), 0)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	start := time.Now()
	err := c.SubscribeStream(ctx, gnmi.StreamRequest{SubscribeRequest: gnmi.SubscribeRequest{Paths: []gnmi.Path{mustPath(t, "/system")}}, Mode: gnmi.StreamOnChange}, func(ev gnmi.StreamEvent) error {
		if ev.Sync {
			proxy.silent.Store(true)
		}
		return nil
	})
	if err == nil || errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "Unavailable") {
		t.Fatalf("SubscribeStream = %v after %s, want the connection closed by the keepalive", err, time.Since(start))
	}
	if took := time.Since(start); took > 30*time.Second {
		t.Errorf("the stream ended after %s, want about the ping's time and timeout", took)
	}
}
