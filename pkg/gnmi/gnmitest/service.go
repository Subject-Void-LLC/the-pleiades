// Package gnmitest: the gNMI service itself, Capabilities, Get and Set.
package gnmitest

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"time"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi"
)

// Capabilities implements gpb.GNMIServer.
func (s *Server) Capabilities(context.Context, *gpb.CapabilityRequest) (*gpb.CapabilityResponse, error) {
	return &gpb.CapabilityResponse{SupportedModels: s.opts.Models, SupportedEncodings: s.opts.Encodings, GNMIVersion: s.opts.Version}, nil
}

// Get implements gpb.GNMIServer: one notification per path asked, each
// holding every stored value at or under it, and NotFound for a path with
// none, as the gNMI specification says.
func (s *Server) Get(_ context.Context, req *gpb.GetRequest) (*gpb.GetResponse, error) {
	if s.opts.Stratum && req.GetEncoding() != gpb.Encoding_PROTO {
		return nil, status.Error(codes.InvalidArgument, StratumGetRefusal)
	}
	prefix, err := pathOf(req.GetPrefix())
	if err != nil {
		return nil, err
	}
	paths := req.GetPath()
	if len(paths) == 0 {
		paths = []*gpb.Path{{}}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	resp := &gpb.GetResponse{}
	for _, pp := range paths {
		p, err := pathOf(pp)
		if err != nil {
			return nil, err
		}
		full := gnmi.Join(prefix, p)
		found := s.data.match(full)
		if len(found) == 0 {
			return nil, status.Errorf(codes.NotFound, "no data at %s", full)
		}
		n := &gpb.Notification{Timestamp: time.Now().UnixNano()}
		for _, e := range found {
			val, err := encoded(e.val, req.GetEncoding())
			if err != nil {
				return nil, err
			}
			n.Update = append(n.Update, &gpb.Update{Path: wirePath(e.path), Val: val})
		}
		resp.Notification = append(resp.Notification, n)
	}
	return resp, nil
}

// Set implements gpb.GNMIServer: deletes, then replaces, then updates, on a
// copy of the data that replaces it only when every operation applied.
func (s *Server) Set(ctx context.Context, req *gpb.SetRequest) (*gpb.SetResponse, error) {
	prefix, err := pathOf(req.GetPrefix())
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	work := s.data.clone()
	resp := &gpb.SetResponse{Prefix: req.GetPrefix()}
	apply := func() error {
		for _, d := range req.GetDelete() {
			p, err := pathOf(d)
			if err != nil {
				return err
			}
			work.remove(gnmi.Join(prefix, p))
			resp.Response = append(resp.Response, &gpb.UpdateResult{Path: d, Op: gpb.UpdateResult_DELETE})
		}
		for _, u := range req.GetReplace() {
			p, val, err := settable(prefix, u)
			if err != nil {
				return err
			}
			work.remove(p)
			work.put(p, val)
			resp.Response = append(resp.Response, &gpb.UpdateResult{Path: u.GetPath(), Op: gpb.UpdateResult_REPLACE})
		}
		for _, u := range req.GetUpdate() {
			p, val, err := settable(prefix, u)
			if err != nil {
				return err
			}
			if old, ok := work[p.String()]; ok {
				a, aok := jsonOf(old.val)
				b, bok := jsonOf(val)
				if aok && bok {
					merged, err := merge(a, b)
					if err != nil {
						return status.Errorf(codes.InvalidArgument, "merging at %s: %v", p, err)
					}
					val = &gpb.TypedValue{Value: &gpb.TypedValue_JsonIetfVal{JsonIetfVal: merged}}
				}
			}
			work.put(p, val)
			resp.Response = append(resp.Response, &gpb.UpdateResult{Path: u.GetPath(), Op: gpb.UpdateResult_UPDATE})
		}
		return nil
	}
	if err := apply(); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.data = work
	s.sets++
	s.mu.Unlock()
	s.notify()
	resp.Timestamp = time.Now().UnixNano()
	if s.opts.AfterSet != nil {
		if err := s.opts.AfterSet(ctx); err != nil {
			return nil, err
		}
	}
	if s.opts.SetAnswer != nil {
		s.opts.SetAnswer(resp)
	}
	return resp, nil
}

// settable checks one replace or update: a path naming one node and a JSON
// or scalar value that parses.
func settable(prefix gnmi.Path, u *gpb.Update) (gnmi.Path, *gpb.TypedValue, error) {
	p, err := pathOf(u.GetPath())
	if err != nil {
		return gnmi.Path{}, nil, err
	}
	full := gnmi.Join(prefix, p)
	if full.HasWildcard() {
		return gnmi.Path{}, nil, status.Errorf(codes.InvalidArgument, "a Set path names one node: %s", full)
	}
	val := u.GetVal()
	if val == nil || val.GetValue() == nil {
		return gnmi.Path{}, nil, status.Errorf(codes.InvalidArgument, "no value at %s", full)
	}
	if raw, ok := jsonOf(val); ok && !json.Valid(raw) {
		return gnmi.Path{}, nil, status.Errorf(codes.InvalidArgument, "the value at %s is not JSON", full)
	}
	return full, val, nil
}

// encoded is a stored value as a request's encoding asks: a JSON value in
// the JSON member asked for, and a scalar as it is.
func encoded(v *gpb.TypedValue, enc gpb.Encoding) (*gpb.TypedValue, error) {
	raw, isJSON := jsonOf(v)
	switch {
	case !isJSON:
		return v, nil
	case enc == gpb.Encoding_JSON:
		return &gpb.TypedValue{Value: &gpb.TypedValue_JsonVal{JsonVal: raw}}, nil
	case enc == gpb.Encoding_JSON_IETF:
		return &gpb.TypedValue{Value: &gpb.TypedValue_JsonIetfVal{JsonIetfVal: raw}}, nil
	}
	return nil, status.Errorf(codes.InvalidArgument, "a JSON value cannot be encoded as %s", enc)
}

// pathOf reads a request's path, refusing one this server cannot read.
func pathOf(pp *gpb.Path) (gnmi.Path, error) {
	if pp == nil {
		return gnmi.Path{}, nil
	}
	if len(pp.GetElement()) > 0 {
		return gnmi.Path{}, status.Error(codes.InvalidArgument, "the deprecated element field is not read")
	}
	p := gnmi.Path{Origin: pp.GetOrigin()}
	for _, e := range pp.GetElem() {
		elem := gnmi.Elem{Name: e.GetName()}
		if len(e.GetKey()) > 0 {
			elem.Keys = maps.Clone(e.GetKey())
		}
		p.Elems = append(p.Elems, elem)
	}
	if err := p.Validate(true); err != nil {
		return gnmi.Path{}, status.Error(codes.InvalidArgument, fmt.Sprintf("path: %v", err))
	}
	return p, nil
}

// wirePath is p on the wire.
func wirePath(p gnmi.Path) *gpb.Path {
	out := &gpb.Path{Origin: p.Origin}
	for _, e := range p.Elems {
		out.Elem = append(out.Elem, &gpb.PathElem{Name: e.Name, Key: maps.Clone(e.Keys)})
	}
	return out
}
