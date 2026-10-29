// The reference documentation of build.lock.verify, which
// internal/forge/catalogdata holds an identical copy of
// (internal/archtest's TestCatalogDataDocsMatchTheRegistry).
package lock

import (
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// hostKeyParam is the shared host key parameter.
var hostKeyParam = collection.Param{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."}

// lockDoc is build.lock.verify's reference documentation.
func lockDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Checks that a lockfile covers its manifest, carries an integrity hash for every entry, and draws only from allowed sources.",
		Description: "Reads a lockfile and its manifest on the device and reports every way the lockfile falls short: a dependency the manifest declares that the lockfile does not lock (or locks for a different range), an entry the manifest no longer declares, an entry with no integrity hash or checksum (or only a sha1 one), an entry fetched over plain http, a git dependency not pinned to a commit, and an entry from a source not on the allowed list. npm's package-lock.json (lockfile versions 2 and 3) with package.json, and Cargo's Cargo.lock (versions 3 and 4) with Cargo.toml, are read; a format is a plug-in, so more can be added. An npm entry bundled inside its parent, or a workspace link, carries its parent's integrity and is not a separate source; a Cargo path crate has no source. Any finding fails the task, its error naming them, and the task's returns are reported with the failure. Nothing is written.",
		Params: []collection.Param{
			{Name: paramLockfile, Type: "string", Required: true, Description: "The lockfile's path on the device. Its format is recognized by its name (package-lock.json, Cargo.lock) unless format names one."},
			{Name: paramManifest, Type: "string", Description: "The manifest's path, when it is not the lockfile's sibling of the usual name (package.json, Cargo.toml). A Cargo workspace member is checked by naming its own Cargo.toml with the workspace's Cargo.lock."},
			{Name: paramFormat, Type: "string", Choices: []string{"npm", "cargo"}, Description: "The lockfile's format, npm or cargo, when its name does not say."},
			{Name: paramSources, Type: "list of string", Description: "The sources entries may come from. For npm, registry host names (default registry.npmjs.org), git and file; for Cargo, Cargo's source strings (default both spellings of crates.io) and git."},
			hostKeyParam,
		},
		Returns: []collection.ReturnField{
			{Name: statFindings, Type: "list of dict", Returned: "when both files were read", Description: "Each shortfall: package (with its version where the lockfile has one) and problem; empty for a task that succeeded."},
		},
		Examples: []collection.Example{
			{Name: "Verify an npm lockfile", RunbookYAML: "- name: Verify the lockfile\n  build.lock.verify:\n    lockfile: /srv/app/package-lock.json\n"},
			{Name: "Verify a Cargo lockfile, allowing a private registry", RunbookYAML: "- name: Verify the lockfile\n  build.lock.verify:\n    lockfile: /srv/app/Cargo.lock\n    sources:\n      - sparse+https://index.crates.io/\n      - sparse+https://crates.example.internal/\n"},
		},
		SeeAlso: []string{"build.digests.check"},
	}
}
