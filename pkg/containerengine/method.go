// What every container.* Collection method built on this package shares in
// its manifest and documentation, so a method package outside the
// namespace's root states it the same way.
package containerengine

import (
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// ReachNote is how every container.* method reaches its engine, appended
// to a method's description.
const ReachNote = " The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine."

// HostKeyParam is the SSH host key parameter every container.* method
// accepts, for an engine reached over SSH.
var HostKeyParam = collection.Param{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task, when the engine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."}

// MethodManifest is the manifest a container.* method opening a Session
// declares: an engine's transports and capability, run against the device,
// checkable, with the method's own reversibility and documentation.
func MethodManifest(rev collection.Reversibility, doc collection.Doc) collection.Manifest {
	return collection.Manifest{
		SupportedTransports:  capability.EngineTransports(),
		RequiredCapabilities: []capability.Name{capability.NameContainerEngine},
		ExecutionContext:     collection.ExecutionContext{Site: collection.SiteTarget, Device: collection.DeviceRequired},
		EngineVersion:        ">=0.2.0",
		Status:               collection.StatusImplemented,
		SupportsCheck:        true,
		Reversibility:        rev,
		Doc:                  doc,
	}
}

// StringMapParam reads an optional mapping of text to text (labels), each
// value refused when it is not text rather than converted.
func StringMapParam(params map[string]any, key string) (map[string]string, error) {
	return stringMap(params, key)
}

// TextParam reads an optional text parameter: absent is "", and any value
// that is not text is refused rather than read as absent, since a task that
// wrote mode: 0755 or id: 42 meant something by it.
func TextParam(params map[string]any, key string) (string, error) {
	return textOf(params, key)
}

// RecordUndo records inv as the task's undo with the engine s reached
// named in it, so the undo replays against that engine on a device that
// runs both Docker and Podman, where a run naming no engine is refused.
// Every container method's InverseSpec records ParamEngine for this.
func RecordUndo(rc sdk.RunbookContext, s *Session, inv sdk.Inverse) error {
	params := make(map[string]any, len(inv.Params)+1)
	for k, v := range inv.Params {
		params[k] = v
	}
	params[ParamEngine] = string(s.Kind)
	inv.Params = params
	return sdk.RecordInverse(rc, inv)
}
