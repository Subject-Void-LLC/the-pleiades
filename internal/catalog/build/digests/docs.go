// The reference documentation of build.digests.check and
// build.digests.refresh, which
// internal/forge/catalogdata holds an identical copy of
// (internal/archtest's TestCatalogDataDocsMatchTheRegistry).
package digests

import (
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// filesParamDoc is the files parameter both digest methods take.
const filesParamDoc = "The files on the device to read, each recognized by its name and content: a Dockerfile or Containerfile (each FROM, COPY --from and RUN --mount from that names an image, a FROM through an ARG read from the ARG's default), a Compose file (each service's image), or Kubernetes manifests (every container's image, in every document). A file of none of these kinds is refused by name. Each is read whole, at most 1 MiB."

// hostKeyParam is the shared host key parameter.
var hostKeyParam = collection.Param{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."}

// checkDoc is build.digests.check's reference documentation.
func checkDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Lists every container image reference in a build's files that is not pinned by both tag and digest.",
		Description: "Reads the named files on the device and checks each image reference in them: pinned means named by both tag and digest (name:tag@sha256:...), since a tag alone moves underneath a build and a digest alone says nothing about what it is. A reference built from a build argument with no default, or from a variable a Compose file is interpolated with, cannot be read as written and is an offender too. With resolve, each pinned reference's tag is also resolved to the digest it names now, through the device's container engine (its own network, proxy, mirrors and registry logins, and the run's credential for that registry when it has one), and a pinned digest the tag no longer names is reported as drift. Any offender fails the task, and since a failed task's returns are not kept, its error names them. Nothing is written.",
		Params: []collection.Param{
			{Name: paramFiles, Type: "list of string", Required: true, Description: filesParamDoc},
			{Name: paramResolve, Type: "bool", Default: "false", Description: "Also resolve each pinned reference's tag through the device's container engine and report drift. The device must have its engine onboarded."},
			hostKeyParam,
		},
		Returns: []collection.ReturnField{
			{Name: statReferences, Type: "list of dict", Returned: "on success", Description: "Every reference found: file, line, reference, where (FROM, COPY --from, RUN --mount, ARG NAME, image), pinned, problem (empty when there is none), and current (the digest its tag names now, when it drifted)."},
			{Name: statOffenders, Type: "int", Returned: "on success", Description: "How many references are offenders, which is zero for a task that succeeded."},
		},
		Fragments: []string{"container_engine"},
		Examples: []collection.Example{
			{Name: "Check a project's images are pinned", RunbookYAML: "- name: Check image pins\n  build.digests.check:\n    files: [/srv/app/Dockerfile, /srv/app/compose.yaml, /srv/app/k8s/deploy.yaml]\n"},
		},
		SeeAlso: []string{"build.digests.refresh", "build.lock.verify"},
	}
}

// refreshDoc is build.digests.refresh's reference documentation.
func refreshDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Pins every container image reference in a build's files to the digest its tag names now.",
		Description: "Reads the named files on the device, resolves each reference's tag to the digest it names now through the device's container engine (for a multi-platform image the index's digest, which pins every platform, never one platform's), and rewrites each reference that changes as name:tag@sha256:..., keeping its own spelling of the name. Every reference is resolved before anything is written, so a registry refusing one (gone, rate limited, unauthorized) leaves every file as it was. A reference with no tag, or one built from a build argument with no default, is refused, since nothing says what to refresh it to. Each file is rewritten once, only if it still holds what was read, and keeps its mode, owner and group: the new content is written beside it and renamed over it, or, when a new file cannot be given its owner and group by this login, copied into it in place (named in warnings); a write that fails puts back every file it had rewritten. Each change is reported as file, line, from and to. With set, the given rewrites are made instead of resolving any: each names a file, a line, the reference there now and the one to write. Before writing anything, a run keeps the references it replaces in an undo record on the device, in the login's own state directory (${XDG_STATE_HOME:-~/.local/state}/pleiades/build-digests, private to it, records older than 90 days removed by the next run), and its recorded undo is this method with undo naming that record, which writes each reference back only where the file still holds what the run wrote. A check resolves every reference as a run does and reports the changes it would make, writing nothing.",
		Params: []collection.Param{
			{Name: paramFiles, Type: "list of string", Description: "Required unless undo is given. " + filesParamDoc},
			{Name: paramSet, Type: "list of dict", Description: "Exact rewrites to make instead of resolving: each a mapping of file, line, from (the reference written there now) and to (the reference to write, which must parse). A file must also be in files. An entry whose from is not on that line refuses the task."},
			{Name: paramUndo, Type: "string", Description: "The id of an undo record an earlier run of this method kept on the device: the references it names are written back as they were, and files, when not given, are the files it names. Cannot be combined with set. A run that writes keeps a record of its own, and the record it replayed is removed."},
			hostKeyParam,
		},
		Returns: []collection.ReturnField{
			{Name: statChanges, Type: "list of dict", Returned: "always", Description: "Each reference changed, or that a check would change: file, line, from and to."},
			{Name: sdk.StatWarnings, Type: "list of string", Returned: "when a file was written in place", Description: "Each file written in place rather than renamed over, and why."},
		},
		Fragments: []string{"container_engine"},
		Examples: []collection.Example{
			{Name: "Move a project's image pins forward", RunbookYAML: "- name: Refresh image pins\n  build.digests.refresh:\n    files: [/srv/app/Dockerfile, /srv/app/compose.yaml]\n"},
		},
		SeeAlso: []string{"build.digests.check"},
	}
}
