// Package gnmi: Subscribe, in its three modes. ONCE reads every value under
// the paths up to the server's sync_response and ends; POLL does the same on
// each Poll the caller sends; STREAM delivers updates until the caller's
// context ends or its handler stops it. Every subscription is bounded: by
// the caller's context always, a ONCE subscription and each POLL round by the
// client's call timeout too, by MaxUpdates for a round up to a sync, and by
// MaxSubscriptionPaths for what one request may name.
package gnmi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
)

// MaxSubscriptionPaths bounds the paths one subscription may name.
const MaxSubscriptionPaths = 1024

// ErrStop is what a stream handler returns to end a STREAM subscription
// cleanly: SubscribeStream then returns nil.
var ErrStop = errors.New("the subscription was stopped by its handler")

// SubscribeRequest names what a subscription reads.
type SubscribeRequest struct {
	// Prefix is prepended to every path, the zero Path for none.
	Prefix Path
	// Paths are the nodes subscribed to; wildcards are allowed.
	Paths []Path
	// Encoding is how values come back.
	Encoding Encoding
	// UpdatesOnly asks the server to send no initial values, only changes,
	// before its sync_response.
	UpdatesOnly bool
}

// StreamMode is how a STREAM subscription's server decides when to send.
type StreamMode uint8

const (
	// StreamTargetDefined lets the server choose per leaf.
	StreamTargetDefined StreamMode = iota
	// StreamOnChange sends a value whenever it changes.
	StreamOnChange
	// StreamSample sends every value every SampleInterval.
	StreamSample
)

// StreamRequest is one STREAM subscription.
type StreamRequest struct {
	SubscribeRequest
	// Mode applies to every path.
	Mode StreamMode
	// SampleInterval is how often a StreamSample subscription sends; zero
	// lets the server choose.
	SampleInterval time.Duration
}

// StreamEvent is one thing a STREAM subscription delivered: a notification,
// or the server's sync_response, after which every update is a change.
type StreamEvent struct {
	// Notification is the update; its zero value when Sync is true.
	Notification Notification
	// Sync is true for the server's sync_response.
	Sync bool
}

// list builds the wire request in mode, each (when not nil) setting what a
// mode adds to every subscription.
func (req SubscribeRequest) list(mode gpb.SubscriptionList_Mode, each func(*gpb.Subscription)) (*gpb.SubscribeRequest, error) {
	if len(req.Paths) == 0 {
		return nil, errors.New("the subscription names no path")
	}
	if len(req.Paths) > MaxSubscriptionPaths {
		return nil, fmt.Errorf("the subscription names %d paths, more than %d", len(req.Paths), MaxSubscriptionPaths)
	}
	if int(req.Encoding) >= len(encodingNames) {
		return nil, fmt.Errorf("encoding %d is not one gNMI names", req.Encoding)
	}
	if err := req.Prefix.Validate(false); err != nil {
		return nil, fmt.Errorf("prefix: %w", err)
	}
	list := &gpb.SubscriptionList{Mode: mode, Encoding: req.Encoding.proto(), UpdatesOnly: req.UpdatesOnly}
	if req.Prefix.Origin != "" || len(req.Prefix.Elems) > 0 {
		list.Prefix = req.Prefix.proto()
	}
	for _, p := range req.Paths {
		if err := p.Validate(true); err != nil {
			return nil, fmt.Errorf("path %s: %w", p, err)
		}
		sub := &gpb.Subscription{Path: p.proto()}
		if each != nil {
			each(sub)
		}
		list.Subscription = append(list.Subscription, sub)
	}
	return &gpb.SubscribeRequest{Request: &gpb.SubscribeRequest_Subscribe{Subscribe: list}}, nil
}

// SubscribeOnce subscribes in ONCE mode and returns every notification the
// server sends up to its sync_response, then ends the stream: a server is
// to close it there, and this client does not wait to see it do so.
func (c *Client) SubscribeOnce(ctx context.Context, req SubscribeRequest) ([]Notification, error) {
	if err := c.refused(); err != nil {
		return nil, err
	}
	wire, err := req.list(gpb.SubscriptionList_ONCE, nil)
	if err != nil {
		return nil, err
	}
	// The whole subscription, up to its sync, is one call's bound.
	ctx, cancel := c.call(ctx)
	defer cancel()
	stream, err := c.api.Subscribe(ctx)
	if err != nil {
		return nil, c.fail(ctx, "Subscribe", err)
	}
	if err := stream.Send(wire); err != nil && !errors.Is(err, io.EOF) {
		return nil, c.fail(ctx, "Subscribe", err)
	}
	return c.untilSync(ctx, stream)
}

// untilSync reads notifications until the server's sync_response, refusing
// a round past MaxUpdates and a stream that ends before its sync.
func (c *Client) untilSync(ctx context.Context, stream gpb.GNMI_SubscribeClient) ([]Notification, error) {
	var out []Notification
	total := 0
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("gnmi Subscribe: the server ended the stream before its sync_response")
		}
		if err != nil {
			return nil, c.fail(ctx, "Subscribe", err)
		}
		switch r := resp.GetResponse().(type) {
		case *gpb.SubscribeResponse_SyncResponse:
			return out, nil
		case *gpb.SubscribeResponse_Update:
			total += len(r.Update.GetUpdate()) + len(r.Update.GetDelete())
			if total > MaxUpdates {
				return nil, fmt.Errorf("gnmi Subscribe: the server sent more than %d updates before its sync_response", MaxUpdates)
			}
			n, err := decodeNotification(r.Update)
			if err != nil {
				return nil, fmt.Errorf("gnmi Subscribe: %w", err)
			}
			out = append(out, n)
		default:
			// The deprecated error member, or one this client does not know.
			return nil, fmt.Errorf("gnmi Subscribe: the server sent a response this client does not read (%T)", r)
		}
	}
}

// Poll is a POLL subscription: each Poll reads every value under its paths
// again, up to the server's sync_response.
type Poll struct {
	c      *Client
	ctx    context.Context
	cancel context.CancelCauseFunc
	stream gpb.GNMI_SubscribeClient
}

// SubscribePoll subscribes in POLL mode and returns the subscription and
// its first round, the values up to the server's first sync_response. The
// subscription lives until Close or ctx ends; a round that takes longer than
// the client's call timeout ends it.
func (c *Client) SubscribePoll(ctx context.Context, req SubscribeRequest) (*Poll, []Notification, error) {
	if err := c.refused(); err != nil {
		return nil, nil, err
	}
	wire, err := req.list(gpb.SubscriptionList_POLL, nil)
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancelCause(ctx)
	p := &Poll{c: c, ctx: ctx, cancel: cancel}
	first, err := p.round(func() error {
		stream, err := c.api.Subscribe(ctx)
		if err != nil {
			return err
		}
		p.stream = stream
		return stream.Send(wire)
	})
	if err != nil {
		cancel(nil)
		return nil, nil, err
	}
	return p, first, nil
}

// Poll asks the server for every value again and returns them, up to its
// sync_response. One Poll at a time: a Poll is not safe for concurrent use.
func (p *Poll) Poll() ([]Notification, error) {
	return p.round(func() error {
		return p.stream.Send(&gpb.SubscribeRequest{Request: &gpb.SubscribeRequest_Poll{Poll: &gpb.Poll{}}})
	})
}

// round asks with ask and reads the round it starts up to the server's
// sync_response, within the client's call timeout. A round past it ends the
// subscription, whose stream cannot be read again with a round left half
// read, and fails saying the server did not answer in time.
func (p *Poll) round(ask func() error) ([]Notification, error) {
	timer := time.AfterFunc(p.c.callTimeout, func() { p.cancel(p.c.timedOut()) })
	defer timer.Stop()
	// A send that meets the stream's end (io.EOF) leaves the server's reason
	// to the read; a stream that never opened has nothing to read.
	if err := ask(); err != nil && (p.stream == nil || !errors.Is(err, io.EOF)) {
		return nil, p.c.fail(p.ctx, "Subscribe", err)
	}
	return p.c.untilSync(p.ctx, p.stream)
}

// Close ends the subscription.
func (p *Poll) Close() {
	p.cancel(nil)
}

// SubscribeStream subscribes in STREAM mode and calls handle with each
// notification and with the server's sync_response, until ctx ends (it
// returns ctx's error), the server ends the stream (nil, or the server's
// error), or handle returns an error (nil for ErrStop, else that error).
// handle runs on the caller's goroutine, one event at a time.
func (c *Client) SubscribeStream(ctx context.Context, req StreamRequest, handle func(StreamEvent) error) error {
	if err := c.refused(); err != nil {
		return err
	}
	if req.Mode > StreamSample {
		return fmt.Errorf("stream mode %d is not one gNMI names", req.Mode)
	}
	if req.SampleInterval < 0 {
		return errors.New("a sample interval is not negative")
	}
	wire, err := req.list(gpb.SubscriptionList_STREAM, func(s *gpb.Subscription) {
		s.Mode = gpb.SubscriptionMode(req.Mode)
		s.SampleInterval = uint64(req.SampleInterval.Nanoseconds()) // #nosec G115 -- checked not negative above
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := c.api.Subscribe(ctx)
	if err != nil {
		return c.fail(ctx, "Subscribe", err)
	}
	if err := stream.Send(wire); err != nil && !errors.Is(err, io.EOF) {
		return c.fail(ctx, "Subscribe", err)
	}
	for {
		resp, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return c.fail(ctx, "Subscribe", err)
		}
		var event StreamEvent
		switch r := resp.GetResponse().(type) {
		case *gpb.SubscribeResponse_SyncResponse:
			event.Sync = true
		case *gpb.SubscribeResponse_Update:
			if event.Notification, err = decodeNotification(r.Update); err != nil {
				return fmt.Errorf("gnmi Subscribe: %w", err)
			}
		default:
			return fmt.Errorf("gnmi Subscribe: the server sent a response this client does not read (%T)", r)
		}
		if err := handle(event); err != nil {
			if errors.Is(err, ErrStop) {
				return nil
			}
			return err
		}
	}
}
