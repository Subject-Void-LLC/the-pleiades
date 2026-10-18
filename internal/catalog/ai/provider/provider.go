// Package provider implements ai.provider.list and ai.provider.probe:
// what a language model provider is and serves, and one counted call that
// shows it answers (docs/design/ai.md).
package provider

import (
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// The methods' names.
const (
	fqcnList  = "ai.provider.list"
	fqcnProbe = "ai.provider.probe"
)

// probeMessage is what a probe asks: one token's worth of answer.
const probeMessage = "Reply with the single word OK."

// manifest is what both methods declare.
func manifest(doc collection.Doc, rev collection.Reversibility) collection.Manifest {
	return collection.Manifest{
		SupportedTransports:  []string{capability.TransportHTTPS},
		RequiredCapabilities: []capability.Name{capability.NameAIProvider},
		ExecutionContext:     collection.ExecutionContext{Site: collection.SiteController, Device: collection.DeviceRequired},
		EngineVersion:        ">=0.2.0",
		Status:               collection.StatusImplemented,
		SupportsCheck:        true,
		Reversibility:        rev,
		Doc:                  doc,
	}
}

var now = time.Now

// strs is a string list as a return value.
func strs(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}
