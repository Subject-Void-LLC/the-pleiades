package wire

import "github.com/Subject-Void-LLC/the-pleiades/pkg/capability"

// ChildRequest and ChildResponse are the two messages that cross the
// per-task subprocess boundary PLAN.md Section 17.5 requires (a Collection
// method's secrets reach it over stdin or a local IPC mechanism, never
// argv or an environment variable). internal/adapters/native's parent
// process writes exactly one ChildRequest to the child's stdin at spawn;
// the child writes exactly one ChildResponse back on a dedicated pipe (see
// below) before exiting. Neither type is a streaming session: a Collection
// method's SetStat/EmitFact calls are collected in-process inside the
// child and returned as part of ChildResponse.Facts, not streamed
// mid-flight, because nothing on the parent side reads them until after
// the method returns anyway (internal/engine/collection_action.go's own
// FactCollector.Facts() call happens once, after Invoke returns).
//
// Both types are exported from pkg/, not internal/, on purpose: Phase 42
// (the Collection Artifact and the Payload Decision) already names this
// same per-job subprocess boundary as the leading candidate for running
// third-party Collection code, Terraform-provider-style. A third-party
// Collection binary speaking this protocol needs to depend on pkg/, never
// internal/, so the wire types themselves have to live here from the
// start rather than move here later as a breaking change.
//
// Wire format is newline-terminated JSON, one line per message: the
// payloads are small (task params, a handful of secret strings, a
// changed/facts/error result), the exchange is exactly one request and
// one response, and a human or a test can read a captured frame directly.
// A binary length-prefixed format would save little at this size and cost
// debuggability; if throughput at scale ever demands it, that is a
// deliberate, separately-justified change to this file, not something to
// speculate into it now.
type ChildRequest struct {
	// FQCN is the fully-qualified Collection method to invoke, e.g.
	// "net.ssh.ping".
	FQCN string `json:"fqcn"`

	// Mode says which of the method's two functions the child runs: a
	// pkg/collection Mode value, where "execute" runs Invoke and "check"
	// runs Check (collection.Descriptor.MethodFor). Empty means execute,
	// which is what every request written before this field existed meant,
	// so a child reading an older parent's request behaves exactly as
	// before. A value the child does not recognize is refused, never run.
	//
	// It is a plain string rather than collection.Mode because this
	// package cannot import pkg/collection: pkg/remoteexec imports this
	// package, and pkg/collection reaches pkg/remoteexec through pkg/sdk.
	// collection.ParseMode is the one place the string is read.
	Mode string `json:"mode,omitempty"`

	// Params is the task's own params block, passed through unchanged.
	Params map[string]any `json:"params"`

	// JobID and DeviceID/DeviceName/DeviceHost/SSHPort/Capabilities mirror
	// the fields of the same name on DispatchPayload: the child needs
	// enough of the device's identity to build its own RunbookContext and
	// device adapter without a second round trip to the parent.
	JobID        string            `json:"job_id"`
	DeviceID     string            `json:"device_id"`
	DeviceName   string            `json:"device_name"`
	DeviceHost   string            `json:"device_host"`
	SSHPort      int               `json:"ssh_port"`
	Capabilities []capability.Name `json:"capabilities"`
	Secrets      map[string]string `json:"secrets,omitempty"`

	// DeviceType and DeviceProperties are DispatchPayload's, handed on so
	// the child rebuilds the same device the Runner did.
	DeviceType       string         `json:"device_type,omitempty"`
	DeviceProperties map[string]any `json:"device_properties,omitempty"`
}

// ChildResponse is the one message the child writes back, on a dedicated
// extra file descriptor (never stdout: an arbitrary fmt.Println inside a
// Collection method must not be able to corrupt this frame by interleaving
// with it on a shared stream).
type ChildResponse struct {
	// Changed reports whether the Collection method's Invoke call changed
	// anything, mirroring pkg/collection.Result.Changed.
	Changed bool `json:"changed"`

	// Facts is whatever the method recorded via SetStat/EmitFact during
	// its one invocation, collected once after Invoke returns.
	Facts map[string]any `json:"facts,omitempty"`

	// Error is the Collection method's own reported failure, if any. It is
	// a string, not a wrapped error type, since it crosses a process
	// boundary as JSON: a non-empty Error here means "the method ran and
	// reported failure," which the parent must distinguish from "the
	// child process never wrote a response at all" (a crash), a
	// distinction ChildResponse's mere presence or absence on the pipe
	// already carries without needing a second field for it.
	Error string `json:"error,omitempty"`

	// CannotCheck reports that Error is a check's answer that it cannot
	// check this call (a pkg/collection.CannotCheckError, whose reason
	// Error then holds), rather than a failure. It is set only by a child
	// running a check, and read only by a parent that asked for one: from
	// any other exchange it means nothing, and the response is an
	// ordinary failure. An optional field, so a parent that predates it
	// reads such an answer as a failed check, never as a passed one.
	CannotCheck bool `json:"cannot_check,omitempty"`
}
