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
const DispatchPayloadVersion = 1

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
