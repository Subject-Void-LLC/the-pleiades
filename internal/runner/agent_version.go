// Package runner: a dispatch from a newer Controller than this Runner reads.
package runner

import (
	"encoding/json"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// newerUndecodable reads only the identity and version of a dispatch that
// failed to decode. When its version is past this build's, the failure is
// the newer shape (a field retyped, say) rather than garbage; when its
// version is one no build writes (zero, null, a fraction, text), the version
// itself is what failed. In both cases it returns a payload naming the job
// and device, and the version error, so the job can be reported failed with
// that reason before the message is dropped, rather than reading
// "dispatched" until the Reaper sends it into the same refusal again.
// Otherwise it reports false, and the dispatch is malformed.
func newerUndecodable(data []byte) (wire.DispatchPayload, error, bool) {
	var head struct {
		// PayloadVersion is the version the Controller wrote, kept as
		// written so a value that is not a version is told from a newer one.
		PayloadVersion json.RawMessage `json:"payload_version"`
		// JobID, DeviceID, DeviceName and RunbookID name what to report on.
		JobID      string `json:"job_id"`
		DeviceID   string `json:"device_id"`
		DeviceName string `json:"device_name"`
		RunbookID  string `json:"runbook_id"`
	}
	// Unmarshal into a struct of only these keys reads them even when
	// another key's value has a shape this build does not know; a key with
	// the wrong type here is a malformed dispatch, not a newer one.
	if err := json.Unmarshal(data, &head); err != nil {
		return wire.DispatchPayload{}, nil, false
	}
	p := wire.DispatchPayload{JobID: head.JobID,
		DeviceID: head.DeviceID, DeviceName: head.DeviceName, RunbookID: head.RunbookID}
	version, err := wire.ParsePayloadVersion(head.PayloadVersion)
	if err != nil {
		return p, err, true
	}
	if version <= wire.DispatchPayloadVersion {
		return wire.DispatchPayload{}, nil, false
	}
	p.PayloadVersion = version
	return p, p.CheckVersion(), true
}
