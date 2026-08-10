package wire

// JobEvent is the one shared shape a job publishes to describe its own
// progress, replacing three independently hand-copied private structs that
// predate this type: internal/adapters/native.LogEvent,
// internal/engine's own nodeEvent (executor.go), and the former
// internal/ansible.AnsibleEvent. All three already agreed on this same
// field shape (nodeEvent's own doc comment says outright that it mirrors
// LogEvent's), so this type gives that shape one definition instead of
// three that could silently drift apart.
//
// Phase 16 (Native Go Execution Adapter) adopted this type for
// internal/adapters/native only, deliberately leaving internal/engine's
// nodeEvent and internal/ansible's AnsibleEvent alone as belonging to
// other tiers and unrequested scope. Phase 17 (Legacy Ansible Adapter)
// closed the second of those two: internal/adapters/legacy (the renamed,
// now-real successor to internal/ansible) publishes this type directly,
// and AnsibleEvent no longer exists. This was not just DRY cleanup:
// AnsibleEvent's own Event field could hold a raw, untranslated Ansible
// callback name ("runner_on_ok"), which is exactly the vocabulary this
// package's own Anti-Corruption Layer boundary must never let reach the
// bus; JobEvent's closed Status vocabulary (below) cannot represent that
// at all, so removing the type removed the risk structurally rather than
// by discipline. internal/engine's own nodeEvent remains a deliberate,
// separate type: it travels over a private, in-process Bus for a
// different purpose (internal/engine/executor.go's own doc comment
// explains why), not this package's own cross-process wire contract.
//
// It is wrapped inside an event.Event envelope on the actual wire (see
// internal/event), never published bare, the same convention
// DispatchPayload documents.
type JobEvent struct {
	// Timestamp is the RFC 3339 time this event was produced.
	Timestamp string `json:"timestamp"`

	// Status is one of "started", "ok", "changed", "failed", or
	// "task.completed". It is a plain string, not a typed enum, because
	// the UI (a JavaScript consumer outside this module) is the primary
	// reader and gains nothing from a Go-side type it cannot see.
	Status string `json:"status"`

	// Host is the device this event describes, the same value
	// DispatchPayload.DeviceHost carries.
	Host string `json:"host"`

	// Task names the task or phase this event reports on (a runbook task
	// id, or a lifecycle marker like "task.completed").
	Task string `json:"task"`

	// EventData carries the event's own message. It is a nested struct
	// rather than a bare Message field because that is the wire shape the
	// UI already consumes; flattening it would be a breaking change to a
	// consumer this package cannot see or update itself.
	EventData struct {
		Message string `json:"message"`
	} `json:"event_data"`
}
