// This file holds the build section of the catalog: build.digests.check,
// build.digests.refresh and build.lock.verify, the image pins and lockfiles
// a build depends on. The Doc literals are copies of
// internal/catalog/build/digests' checkDoc and refreshDoc and
// internal/catalog/build/lock's lockDoc, kept equal by internal/archtest's
// TestCatalogDataDocsMatchTheRegistry.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

var buildCollections = []collectionscaffold.Config{
	{
		Name:          "build.digests.check",
		Capabilities:  []capability.Name{capability.NameCommandExec},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Lists every container image reference in a build's files that is not pinned by both tag and digest.",
			Description: "Reads the named files on the device and checks each image reference in them: pinned means named by both tag and digest (name:tag@sha256:...), since a tag alone moves underneath a build and a digest alone says nothing about what it is. A reference built from a build argument with no default, or from a variable a Compose file is interpolated with, cannot be read as written and is an offender too. With resolve, each pinned reference's tag is also resolved to the digest it names now, through the device's container engine (its own network, proxy, mirrors and registry logins, and the run's credential for that registry when it has one), and a pinned digest the tag no longer names is reported as drift. Any offender fails the task, and since a failed task's returns are not kept, its error names them. Nothing is written.",
			Params: []collection.Param{
				{Name: "files", Type: "list of string", Required: true, Description: "The files on the device to read, each recognized by its name and content: a Dockerfile or Containerfile (each FROM, COPY --from and RUN --mount from that names an image, a FROM through an ARG read from the ARG's default), a Compose file (each service's image), or Kubernetes manifests (every container's image, in every document). A file of none of these kinds is refused by name. Each is read whole, at most 1 MiB."},
				{Name: "resolve", Type: "bool", Default: "false", Description: "Also resolve each pinned reference's tag through the device's container engine and report drift. The device must have its engine onboarded."},
				collection.Param{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "references", Type: "list of dict", Returned: "on success", Description: "Every reference found: file, line, reference, where (FROM, COPY --from, RUN --mount, ARG NAME, image), pinned, problem (empty when there is none), and current (the digest its tag names now, when it drifted)."},
				{Name: "offenders", Type: "int", Returned: "on success", Description: "How many references are offenders, which is zero for a task that succeeded."},
			},
			Fragments: []string{"container_engine"},
			Examples: []collection.Example{
				{Name: "Check a project's images are pinned", RunbookYAML: "- name: Check image pins\n  build.digests.check:\n    files: [/srv/app/Dockerfile, /srv/app/compose.yaml, /srv/app/k8s/deploy.yaml]\n"},
			},
			SeeAlso: []string{"build.digests.refresh", "build.lock.verify"},
		},
	},
	{
		Name:          "build.digests.refresh",
		Capabilities:  []capability.Name{capability.NameCommandExec},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Pins every container image reference in a build's files to the digest its tag names now.",
			Description: "Reads the named files on the device, resolves each reference's tag to the digest it names now through the device's container engine (for a multi-platform image the index's digest, which pins every platform, never one platform's), and rewrites each reference that changes as name:tag@sha256:..., keeping its own spelling of the name. Every reference is resolved before anything is written, so a registry refusing one (gone, rate limited, unauthorized) leaves every file as it was. A reference with no tag, or one built from a build argument with no default, is refused, since nothing says what to refresh it to. Each file is rewritten once, only if it still holds what was read, and keeps its mode, owner and group: the new content is written beside it and renamed over it, or, when a new file cannot be given its owner and group by this login, copied into it in place (named in warnings); a write that fails puts back every file it had rewritten. Each change is reported as file, line, from and to. With set, the given rewrites are made instead of resolving any: each names a file, a line, the reference there now and the one to write. Before writing anything, a run keeps the references it replaces in an undo record on the device, in the login's own state directory (${XDG_STATE_HOME:-~/.local/state}/pleiades/build-digests, private to it, records older than 90 days removed by the next run), and its recorded undo is this method with undo naming that record, which writes each reference back only where the file still holds what the run wrote. A check resolves every reference as a run does and reports the changes it would make, writing nothing.",
			Params: []collection.Param{
				{Name: "files", Type: "list of string", Description: "Required unless undo is given. The files on the device to read, each recognized by its name and content: a Dockerfile or Containerfile (each FROM, COPY --from and RUN --mount from that names an image, a FROM through an ARG read from the ARG's default), a Compose file (each service's image), or Kubernetes manifests (every container's image, in every document). A file of none of these kinds is refused by name. Each is read whole, at most 1 MiB."},
				{Name: "set", Type: "list of dict", Description: "Exact rewrites to make instead of resolving: each a mapping of file, line, from (the reference written there now) and to (the reference to write, which must parse). A file must also be in files. An entry whose from is not on that line refuses the task."},
				{Name: "undo", Type: "string", Description: "The id of an undo record an earlier run of this method kept on the device: the references it names are written back as they were, and files, when not given, are the files it names. Cannot be combined with set. A run that writes keeps a record of its own, and the record it replayed is removed."},
				collection.Param{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "changes", Type: "list of dict", Returned: "always", Description: "Each reference changed, or that a check would change: file, line, from and to."},
				{Name: "warnings", Type: "list of string", Returned: "when a file was written in place", Description: "Each file written in place rather than renamed over, and why."},
			},
			Fragments: []string{"container_engine"},
			Examples: []collection.Example{
				{Name: "Move a project's image pins forward", RunbookYAML: "- name: Refresh image pins\n  build.digests.refresh:\n    files: [/srv/app/Dockerfile, /srv/app/compose.yaml]\n"},
			},
			SeeAlso: []string{"build.digests.check"},
		},
	},
	{
		Name:          "build.lock.verify",
		Capabilities:  []capability.Name{capability.NameCommandExec},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Checks that a lockfile covers its manifest, carries an integrity hash for every entry, and draws only from allowed sources.",
			Description: "Reads a lockfile and its manifest on the device and reports every way the lockfile falls short: a dependency the manifest declares that the lockfile does not lock (or locks for a different range), an entry the manifest no longer declares, an entry with no integrity hash or checksum (or only a sha1 one), an entry fetched over plain http, a git dependency not pinned to a commit, and an entry from a source not on the allowed list. npm's package-lock.json (lockfile versions 2 and 3) with package.json, and Cargo's Cargo.lock (versions 3 and 4) with Cargo.toml, are read; a format is a plug-in, so more can be added. An npm entry bundled inside its parent, or a workspace link, carries its parent's integrity and is not a separate source; a Cargo path crate has no source. Any finding fails the task, and since a failed task's returns are not kept, its error names them. Nothing is written.",
			Params: []collection.Param{
				{Name: "lockfile", Type: "string", Required: true, Description: "The lockfile's path on the device. Its format is recognized by its name (package-lock.json, Cargo.lock) unless format names one."},
				{Name: "manifest", Type: "string", Description: "The manifest's path, when it is not the lockfile's sibling of the usual name (package.json, Cargo.toml). A Cargo workspace member is checked by naming its own Cargo.toml with the workspace's Cargo.lock."},
				{Name: "format", Type: "string", Description: "The lockfile's format, npm or cargo, when its name does not say."},
				{Name: "sources", Type: "list of string", Description: "The sources entries may come from. For npm, registry host names (default registry.npmjs.org), git and file; for Cargo, Cargo's source strings (default both spellings of crates.io) and git."},
				collection.Param{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "findings", Type: "list of dict", Returned: "on success", Description: "Each shortfall: package (with its version where the lockfile has one) and problem; empty for a task that succeeded."},
			},
			Examples: []collection.Example{
				{Name: "Verify an npm lockfile", RunbookYAML: "- name: Verify the lockfile\n  build.lock.verify:\n    lockfile: /srv/app/package-lock.json\n"},
				{Name: "Verify a Cargo lockfile, allowing a private registry", RunbookYAML: "- name: Verify the lockfile\n  build.lock.verify:\n    lockfile: /srv/app/Cargo.lock\n    sources:\n      - sparse+https://index.crates.io/\n      - sparse+https://crates.example.internal/\n"},
			},
			SeeAlso: []string{"build.digests.check"},
		},
	},
}
