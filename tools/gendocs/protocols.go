// Package main: the external Collection protocol versions this build speaks,
// as data a CI job of a Collection's author can read.
package main

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/clireport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
)

// extensionProtocols is the document's shape: its own version, and the
// window in exactly the shape `pleiades version --json` prints it
// (clireport.ExtensionProtocols), so one reader reads both.
type extensionProtocols struct {
	// SchemaVersion is this document's own version.
	SchemaVersion int `json:"schema_version"`
	clireport.ExtensionProtocols
}

// generateExtensionProtocols emits outDir/schemas/extension-protocols.json
// and the identical copy the Controller serves at
// /.well-known/pleiades/extension-protocols.json, from external.Protocols,
// the one declaration the loader negotiates from.
func generateExtensionProtocols(outDir string) error {
	return writeSchema(outDir, "extension-protocols.json", extensionProtocols{
		SchemaVersion:      1,
		ExtensionProtocols: clireport.ExtensionProtocols{Current: external.ProtocolVersion, Versions: external.Protocols},
	})
}
