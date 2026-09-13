// Package journal: the shape a run journal takes on the wire.
//
// This type is shared rather than declared twice on purpose. The Runner
// publishes it and the Controller decodes it, and the two live in
// different processes: a struct copied into each side is a struct that
// drifts, and the first symptom of that drift is a field the Controller
// silently reads as its zero value.
//
// It lives here rather than in pkg/wire, where this codebase's other
// cross-process DTOs live, for a mechanical reason: it carries
// engine.JournalEntry, pkg/ may never import internal/, and flattening
// the entry into a pkg-safe copy would reintroduce exactly the
// duplicate-struct drift this type exists to avoid.
package journal

import "github.com/Subject-Void-LLC/the-pleiades/internal/engine"

// Batch is one topological level's journal entries as published.
//
// A batch rather than one message per entry because the engine hands a
// sink a whole level at a time and the store writes them together.
// Splitting here would multiply the message count by the task count and
// buy nothing.
type Batch struct {
	// JobID is the dispatch these entries belong to, repeated outside
	// the entries so a consumer can route on it without decoding them.
	JobID string `json:"job_id"`

	// DeviceID is the one device this dispatch names, repeated for the
	// same reason JobID is.
	DeviceID string `json:"device_id"`

	// Attempt is the dispatch's redelivery count.
	Attempt int `json:"attempt"`

	// Entries is the level's entries, already stamped with JobID and
	// Attempt by the publisher.
	Entries []engine.JournalEntry `json:"entries"`
}

// EventType is the envelope type a Batch is published under, matching
// the "<noun>.<verb>" shape every other event in this codebase uses.
//
// Exported so the publisher and the subscriber name the same string. A
// consumer filtering on a literal that had drifted from the producer's
// would see an empty stream and no error.
const EventType = "job.journal"
