// Package wire: the dispatch payload's version.
//
// A Runner decodes a dispatch without DisallowUnknownFields, so a key a newer
// Controller adds is dropped in silence by an older Runner. That is safe for
// a field whose absence means "off" (persist_connections, shared_device), and
// unsafe for one that changes what the dispatch means. The payload version is
// how a Controller says a dispatch needs a Runner that understands it: a
// Runner refuses a version past its own by name, reporting the job failed,
// rather than running part of it.
package wire

import (
	"errors"
	"fmt"
)

// DispatchPayloadVersion is the payload version this build writes and the
// newest one it reads.
//
// Raise it when a field is added or changed whose loss on an older Runner
// would make that Runner do something other than what the Controller asked:
// run a check for real, reach the wrong device, skip a refusal. A field whose
// absence is the old behavior needs no raise. Until 1.0 the payload may change
// at any minor release, and a Controller and its Runners are upgraded
// together; docs/13-releases-and-stability.md states the contract.
//
// Version 2 holds a native dispatch to the graph its job pinned
// (DispatchPayload.GraphScheme and GraphDigest). A Controller writes it only
// on a dispatch carrying a pinned graph and writes version 1 on every other
// (PayloadVersionFor), so a Runner of an earlier release refuses exactly the
// dispatches it would run unheld, and still runs rollbacks and playbooks.
const DispatchPayloadVersion = 2

// PinnedGraphPayloadVersion is the version a dispatch carrying a pinned graph
// is written at: the first version whose Runner holds a native dispatch to it.
const PinnedGraphPayloadVersion = 2

// PayloadVersionFor is the version a Controller writes p at: the lowest whose
// Runner reads all p asks of it, so a dispatch that needs nothing new is
// still taken by a Runner of an earlier release.
func PayloadVersionFor(p DispatchPayload) int {
	if p.GraphScheme != "" || p.GraphDigest != "" {
		return PinnedGraphPayloadVersion
	}
	return 1
}

// ErrNewerPayload is what a Runner refuses a dispatch with when the payload's
// version is past the one it reads.
var ErrNewerPayload = errors.New("dispatch payload is newer than this runner reads")

// CheckVersion refuses a payload this build cannot read.
//
// An absent version (zero) is a payload from a Controller that predates the
// field, which has version 1's shape.
func (p DispatchPayload) CheckVersion() error {
	switch {
	case p.PayloadVersion < 0:
		return fmt.Errorf("dispatch payload names version %d, which is not a payload version", p.PayloadVersion)
	case p.PayloadVersion > DispatchPayloadVersion:
		return fmt.Errorf("%w: it is version %d and this runner reads up to %d; upgrade the runner to the controller's release",
			ErrNewerPayload, p.PayloadVersion, DispatchPayloadVersion)
	}
	return nil
}
