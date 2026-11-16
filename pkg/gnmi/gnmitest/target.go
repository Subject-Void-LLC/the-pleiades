// Package gnmitest: the request prefix's target, recorded for every Get,
// Set and Subscribe so a test can assert what each carried, and required
// when the server is set to, as SONiC's gNMI server requires one.
package gnmitest

import (
	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// EmptyTargetRefusal is what SONiC's gNMI server answers a request whose
// prefix names no target with (Unimplemented), as measured on 2026-11-16.
const EmptyTargetRefusal = "Empty target data not supported"

// Targets returns the prefix target of each Get, Set and Subscribe the
// server received, in order, empty for a request naming none.
func (s *Server) Targets() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.targets...)
}

// checkTarget records a request prefix's target and refuses it when the
// server requires another (Options.RequireTarget).
func (s *Server) checkTarget(prefix *gpb.Path) error {
	target := prefix.GetTarget()
	s.mu.Lock()
	s.targets = append(s.targets, target)
	s.mu.Unlock()
	switch {
	case s.opts.RequireTarget == "":
		return nil
	case target == "":
		return status.Error(codes.Unimplemented, EmptyTargetRefusal)
	case target != s.opts.RequireTarget:
		return status.Errorf(codes.InvalidArgument, "target %q is not served here", target)
	}
	return nil
}

// echoed is the prefix an answer carries for a request whose prefix named
// target: the target alone, as SONiC echoes it, or none.
func echoed(target string) *gpb.Path {
	if target == "" {
		return nil
	}
	return &gpb.Path{Target: target}
}
