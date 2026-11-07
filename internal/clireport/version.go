// Package clireport: the document `pleiades version --json` and `runner
// version --json` print.
package clireport

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/buildinfo"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/external"
)

// VersionReportVersion is VersionReport's schema_version.
const VersionReportVersion = 1

// VersionReport is what a binary says about itself: its version, its
// release's name, and the external Collection protocols it speaks, so a
// Collection author's CI, or an operator about to upgrade, reads which
// programs this build will load without running one.
type VersionReport struct {
	// SchemaVersion is this document's major version.
	SchemaVersion int `json:"schema_version"`
	// Program is the binary that printed it: pleiades or runner.
	Program string `json:"program"`
	// Version is the build's version: a release, or 0.0.0-dev+<commit>.
	Version string `json:"version"`
	// Codename is the release's name, absent for a development build.
	Codename string `json:"codename,omitempty"`
	// ExtensionProtocols are the external Collection protocol versions this
	// build speaks.
	ExtensionProtocols ExtensionProtocols `json:"extension_protocols"`
}

// ExtensionProtocols is the external Collection protocol window.
type ExtensionProtocols struct {
	// Current is the newest version, the one a program built with this
	// release's pkg/external names as its protocol.
	Current int `json:"current"`
	// Versions are every version this build speaks, oldest first, each
	// supported or deprecated with the release that removes it.
	Versions []external.Protocol `json:"versions"`
}

// NewVersionReport builds the report for program at version.
func NewVersionReport(program, version string) VersionReport {
	return VersionReport{
		SchemaVersion: VersionReportVersion, Program: program, Version: version,
		Codename:           buildinfo.CodenameFor(version),
		ExtensionProtocols: ExtensionProtocols{Current: external.ProtocolVersion, Versions: external.Protocols},
	}
}
