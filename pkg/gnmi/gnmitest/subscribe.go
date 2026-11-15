// Package gnmitest: Subscribe in its three modes. Each round sends one
// notification per stored value under the subscription's paths, one update
// each, as Stratum does, then the sync_response: ONCE ends the stream there,
// POLL waits for the next poll, and STREAM sends a round every sample
// interval (SAMPLE) or whenever a Set changes the data (ON_CHANGE and
// TARGET_DEFINED).
package gnmitest

import (
	"errors"
	"io"
	"time"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi"
)

// Subscribe implements gpb.GNMIServer.
func (s *Server) Subscribe(stream gpb.GNMI_SubscribeServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	list := first.GetSubscribe()
	if list == nil {
		return status.Error(codes.InvalidArgument, "the first message of a subscription is its subscription list")
	}
	prefix, err := pathOf(list.GetPrefix())
	if err != nil {
		return err
	}
	var paths []gnmi.Path
	for _, sub := range list.GetSubscription() {
		p, err := pathOf(sub.GetPath())
		if err != nil {
			return err
		}
		paths = append(paths, gnmi.Join(prefix, p))
	}
	round := func() error {
		if !list.GetUpdatesOnly() {
			if err := s.sendRound(stream, paths, list.GetEncoding()); err != nil {
				return err
			}
		}
		return stream.Send(&gpb.SubscribeResponse{Response: &gpb.SubscribeResponse_SyncResponse{SyncResponse: true}})
	}
	switch list.GetMode() {
	case gpb.SubscriptionList_ONCE:
		return round()
	case gpb.SubscriptionList_POLL:
		if err := round(); err != nil {
			return err
		}
		for {
			msg, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return err
			}
			if msg.GetPoll() == nil {
				return status.Error(codes.InvalidArgument, "a POLL subscription takes only polls after its list")
			}
			// A poll's answer ends with a sync_response, and sends values
			// even to a subscription that asked for updates only.
			if err := s.sendRound(stream, paths, list.GetEncoding()); err != nil {
				return err
			}
			if err := stream.Send(&gpb.SubscribeResponse{Response: &gpb.SubscribeResponse_SyncResponse{SyncResponse: true}}); err != nil {
				return err
			}
		}
	}
	return s.streamMode(stream, list, paths, round)
}

// streamMode serves a STREAM subscription until the client ends it.
func (s *Server) streamMode(stream gpb.GNMI_SubscribeServer, list *gpb.SubscriptionList, paths []gnmi.Path, round func() error) error {
	if err := round(); err != nil {
		return err
	}
	sample := time.Duration(0)
	for _, sub := range list.GetSubscription() {
		if sub.GetMode() == gpb.SubscriptionMode_SAMPLE {
			sample = time.Duration(sub.GetSampleInterval()) // #nosec G115 -- a test server's own interval
			if sample <= 0 {
				sample = time.Second
			}
		}
	}
	ctx := stream.Context()
	for {
		s.mu.Lock()
		changed := s.changed
		s.mu.Unlock()
		var tick <-chan time.Time
		if sample > 0 {
			tick = time.After(sample)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick:
		case <-changed:
			if sample > 0 {
				continue
			}
		}
		if err := s.sendRound(stream, paths, list.GetEncoding()); err != nil {
			return err
		}
	}
}

// sendRound sends one notification per stored value under paths.
func (s *Server) sendRound(stream gpb.GNMI_SubscribeServer, paths []gnmi.Path, enc gpb.Encoding) error {
	s.mu.Lock()
	var found []entry
	for _, p := range paths {
		found = append(found, s.data.match(p)...)
	}
	s.mu.Unlock()
	for _, e := range found {
		val := e.val
		if !s.opts.Stratum {
			var err error
			if val, err = encoded(e.val, enc); err != nil {
				return err
			}
		}
		n := &gpb.Notification{Timestamp: time.Now().UnixNano(), Update: []*gpb.Update{{Path: wirePath(e.path), Val: val}}}
		if err := stream.Send(&gpb.SubscribeResponse{Response: &gpb.SubscribeResponse_Update{Update: n}}); err != nil {
			return err
		}
	}
	return nil
}
