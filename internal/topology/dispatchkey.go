// Package topology: the one identity of a unit of dispatched work, one job
// on one device, and its two spellings.
package topology

import "strings"

// DispatchIdentity is one job's work on one device, written "jobID:deviceID".
// jobID is a UUID, so it holds no colon; one that did would make the
// identity ambiguous, which is why the Runner refuses a dispatch whose job
// id is not a UUID before anything derives this.
// It is the dispatch's publish idempotency key and the Runner's result WAL
// entry id, so a dead letter's idempotency key names the job and device it
// was for (ParseDispatchIdentity).
func DispatchIdentity(jobID, deviceID string) string {
	return jobID + ":" + deviceID
}

// ParseDispatchIdentity reads back DispatchIdentity's two parts. A job id
// is a UUID and never holds a colon, so the first colon divides them and a
// device id may hold colons of its own. It reports false for text that is
// not an identity: no colon, or an empty part.
func ParseDispatchIdentity(identity string) (jobID, deviceID string, ok bool) {
	jobID, deviceID, ok = strings.Cut(identity, ":")
	if !ok || jobID == "" || deviceID == "" {
		return "", "", false
	}
	return jobID, deviceID, true
}

// DispatchKey is DispatchIdentity as one subject token ([A-Za-z0-9_-]+):
// the Runner's duplicate-suppression marker, a NATS KV key, and the owner a
// device lease records, a value other processes read back. It is
// deterministic but not reversible; ParseDispatchIdentity reads the
// identity, not this.
func DispatchKey(jobID, deviceID string) string {
	return SubjectToken(DispatchIdentity(jobID, deviceID))
}
