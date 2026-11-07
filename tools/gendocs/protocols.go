// Package main: the external Collection protocol versions this build speaks,
// as data a CI job of a Collection's author can read.
package main

import "github.com/Subject-Void-LLC/the-pleiades/pkg/external"

// extensionProtocols is the document's shape: its own version, the newest
// protocol, and every version this build speaks with its status.
type extensionProtocols struct {
	// SchemaVersion is this document's own version.
	SchemaVersion int `json:"schemaVersion"`
	// Current is the newest protocol version, the one a program built with
	// this release's pkg/external names as its protocol.
	Current int `json:"current"`
	// Protocols are the versions this build speaks, oldest first.
	Protocols []external.Protocol `json:"protocols"`
}

// generateExtensionProtocols emits outDir/schemas/extension-protocols.json
// and the identical copy the Controller serves at
// /.well-known/pleiades/extension-protocols.json, from external.Protocols,
// the one declaration the loader negotiates from.
func generateExtensionProtocols(outDir string) error {
	return writeSchema(outDir, "extension-protocols.json", extensionProtocols{
		SchemaVersion: 1, Current: external.ProtocolVersion, Protocols: external.Protocols,
	})
}
