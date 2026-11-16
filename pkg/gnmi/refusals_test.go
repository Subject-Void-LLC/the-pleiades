// Package gnmi_test: what each call refuses before sending, and how a
// server's refusal of a subscription or a login with none comes back.
package gnmi_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi/gnmitest"
)

// TestCalls_RefuseAMalformedRequestUnsent holds Get and each subscription
// mode to refusing a request that breaks a rule, with nothing sent.
func TestCalls_RefuseAMalformedRequestUnsent(t *testing.T) {
	s := gnmitest.New(t, gnmitest.Options{})
	c, _ := clientOf(t, s)
	ctx := ctxOf(t)
	wild := mustPath(t, "/a[k=*]")
	many := make([]gnmi.Path, gnmi.MaxSubscriptionPaths+1)
	for name, call := range map[string]func() error{
		"get, bad type":     func() error { _, err := c.Get(ctx, gnmi.GetRequest{Type: 9}); return err },
		"get, bad encoding": func() error { _, err := c.Get(ctx, gnmi.GetRequest{Encoding: 9}); return err },
		"get, wild prefix":  func() error { _, err := c.Get(ctx, gnmi.GetRequest{Prefix: wild}); return err },
		"get, bad path": func() error {
			_, err := c.Get(ctx, gnmi.GetRequest{Paths: []gnmi.Path{{Elems: []gnmi.Elem{{Name: "a b"}}}}})
			return err
		},
		"once, no path": func() error { _, err := c.SubscribeOnce(ctx, gnmi.SubscribeRequest{}); return err },
		"once, many":    func() error { _, err := c.SubscribeOnce(ctx, gnmi.SubscribeRequest{Paths: many}); return err },
		"poll, encoding": func() error {
			_, _, err := c.SubscribePoll(ctx, gnmi.SubscribeRequest{Paths: []gnmi.Path{wild}, Encoding: 9})
			return err
		},
		"stream, mode": func() error {
			return c.SubscribeStream(ctx, gnmi.StreamRequest{SubscribeRequest: gnmi.SubscribeRequest{Paths: []gnmi.Path{wild}}, Mode: 7}, nil)
		},
		"stream, interval": func() error {
			return c.SubscribeStream(ctx, gnmi.StreamRequest{SubscribeRequest: gnmi.SubscribeRequest{Paths: []gnmi.Path{wild}}, SampleInterval: -1}, nil)
		},
		"stream, prefix": func() error {
			return c.SubscribeStream(ctx, gnmi.StreamRequest{SubscribeRequest: gnmi.SubscribeRequest{Paths: []gnmi.Path{wild}, Prefix: wild}}, nil)
		},
	} {
		if err := call(); err == nil {
			t.Errorf("%s: nil, want a refusal", name)
		}
	}
	if rpcs := s.RPCs(); len(rpcs) != 0 {
		t.Errorf("server saw %v, want nothing sent", rpcs)
	}
}

// TestSubscribe_AServersRefusalComesBackAsItsStatus has the server refuse
// each subscription mode, and a stream handler's own error end the stream.
func TestSubscribe_AServersRefusalComesBackAsItsStatus(t *testing.T) {
	var refuse atomic.Bool
	refuse.Store(true)
	s := gnmitest.New(t, gnmitest.Options{Hook: func(_ context.Context, rpc string) error {
		if refuse.Load() && strings.HasSuffix(rpc, "/Subscribe") {
			return status.Error(codes.ResourceExhausted, "too many subscriptions")
		}
		return nil
	}})
	s.Put(t, "/a/b", `1`)
	c, _ := clientOf(t, s)
	ctx := ctxOf(t)
	req := gnmi.SubscribeRequest{Paths: []gnmi.Path{mustPath(t, "/a")}, Encoding: gnmi.EncodingJSONIETF}
	if _, err := c.SubscribeOnce(ctx, req); gnmi.Code(err) != codes.ResourceExhausted {
		t.Errorf("ONCE = %v", err)
	}
	if _, _, err := c.SubscribePoll(ctx, req); gnmi.Code(err) != codes.ResourceExhausted {
		t.Errorf("POLL = %v", err)
	}
	if err := c.SubscribeStream(ctx, gnmi.StreamRequest{SubscribeRequest: req}, func(gnmi.StreamEvent) error { return nil }); gnmi.Code(err) != codes.ResourceExhausted {
		t.Errorf("STREAM = %v", err)
	}
	refuse.Store(false)
	mine := errors.New("the handler's own reason")
	if err := c.SubscribeStream(ctx, gnmi.StreamRequest{SubscribeRequest: req, Mode: gnmi.StreamSample}, func(gnmi.StreamEvent) error { return mine }); !errors.Is(err, mine) {
		t.Errorf("STREAM with a failing handler = %v, want its error", err)
	}
	if c.Target() != s.Addr() {
		t.Errorf("Target = %s, want %s", c.Target(), s.Addr())
	}
}

// TestRefusal_ASendsNothingDeviceIsToldWhatToSend asks for a login of a device
// whose record sends none, and holds the refusal to saying so.
func TestRefusal_ASendsNothingDeviceIsToldWhatToSend(t *testing.T) {
	s := gnmitest.New(t, gnmitest.Options{Username: testUser, Password: testPassword})
	device := gnmitest.NewDevice(t, s)
	device.Auth = gnmi.AuthNone
	c, _, err := gnmi.ClientFor(device, "router1", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_, err = c.Time(ctxOf(t))
	var refused *gnmi.RefusedError
	if !errors.As(err, &refused) || !refused.NoneSent || !strings.Contains(err.Error(), "gnmi_auth none") {
		t.Fatalf("Time = %v, want a refusal naming the missing credential", err)
	}
	if _, err := c.FileStat(ctxOf(t), "/x"); !errors.As(err, &refused) {
		t.Errorf("FileStat after the refusal = %v", err)
	}
	if _, err := c.Certificates(ctxOf(t)); !errors.As(err, &refused) {
		t.Errorf("Certificates after the refusal = %v", err)
	}
	if _, _, err := c.SubscribePoll(ctxOf(t), gnmi.SubscribeRequest{Paths: []gnmi.Path{mustPath(t, "/")}}); !errors.As(err, &refused) {
		t.Errorf("SubscribePoll after the refusal = %v", err)
	}
	if err := c.SubscribeStream(ctxOf(t), gnmi.StreamRequest{SubscribeRequest: gnmi.SubscribeRequest{Paths: []gnmi.Path{mustPath(t, "/")}}}, nil); !errors.As(err, &refused) {
		t.Errorf("SubscribeStream after the refusal = %v", err)
	}
	if _, err := c.Capabilities(ctxOf(t)); !errors.As(err, &refused) {
		t.Errorf("Capabilities after the refusal = %v", err)
	}
	lost := &gnmi.AnswerLostError{Err: context.DeadlineExceeded}
	if !errors.Is(lost, context.DeadlineExceeded) || !strings.Contains(lost.Error(), "not known") {
		t.Errorf("AnswerLostError = %v", lost)
	}
	if gnmi.Code(errors.New("plain")) != codes.Unknown {
		t.Error("Code of a plain error is not Unknown")
	}
}
