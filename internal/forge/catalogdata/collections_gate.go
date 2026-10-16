// This file holds the gate section of the catalog: gate.run and gate.list,
// a project's gate manifest of named checks run or listed on a device. The
// Doc literals are copies of internal/catalog/gate's runDoc and listDoc,
// kept equal by internal/archtest's TestCatalogDataDocsMatchTheRegistry.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

var gateCollections = []collectionscaffold.Config{
	{
		Name:          "gate.list",
		Capabilities:  []capability.Name{capability.NameCommandExec},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Validates a project's gate manifest on the device and lists how each check is classified, running nothing.",
			Description: "Reads and validates the same manifest gate.run reads, under the same rules, and reports each check's name, kind, whether it is heavy or serial, its timeout, where it would run (a directory on the device or an image), its preconditions, and whether the task's selection would run it, with the reason when it would not. A check that needs a container engine on a device with none onboarded is reported as not runnable. Nothing in the manifest runs, so a check of this task is the task itself.",
			Params: []collection.Param{
				{Name: "manifest", Type: "string", Required: true, Description: "The gate manifest's path on the device."},
				{Name: "kinds", Type: "list of string", Description: "Mark as selected only checks of these kinds, as gate.run would select them."},
				{Name: "checks", Type: "list of string", Description: "Mark as selected only these checks, by name."},
				{Name: "heavy", Type: "string", Default: "include", Description: "include, exclude or only, as for gate.run."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "checks", Type: "list of dict", Returned: "always", Description: "One entry per check: name, kind, heavy, serial, timeout_s, runs (\"dir <path>\" or \"image <reference>\"), requires (the preconditions, as written), selected, reason (why it is not selected), and runnable (false, with the reason in reason, for a check the device cannot run)."},
				{Name: "kinds", Type: "list of dict", Returned: "always", Description: "Every kind the manifest may use: name, builtin and description."},
				{Name: "selected", Type: "int", Returned: "always", Description: "How many checks the selection would run."},
			},
			Examples: []collection.Example{
				{
					Name:        "Show how a project's checks are classified",
					RunbookYAML: "- name: List the gate\n  gate.list:\n    manifest: /srv/app/gates.yaml\n  register: gates\n",
				},
			},
			SeeAlso: []string{"gate.run"},
		},
	},
	{
		Name:          "gate.run",
		Capabilities:  []capability.Name{capability.NameCommandExec},
		Transports:    []string{"ssh"},
		EngineVersion: engineVersion,
		Doc: collection.Doc{
			Summary:     "Runs a project's gate manifest of named checks on the device and reports PASS, FAIL or SKIP for each.",
			Description: "Reads a gate manifest (YAML, at most 1 MiB, decoded strictly) from the device and runs the checks the task selects, one result per check. A check runs its command in a directory on the device over SSH, or in a fresh container from a digest-pinned image on the device's container engine, with no network unless it names one, a read-only root with a writable /tmp unless it says writable, and no new privileges; the image is pulled only when the check says pull. Every check has a timeout, its own or the manifest's default, and there is no implicit one; a directory check's is enforced on the device with the timeout command, which the device must have, and an image check's by killing its container. Checks run one at a time unless the manifest's defaults.parallelism or the task's parallelism raises it, at most one heavy check runs at a time, and a serial check runs with nothing else. A check skips, rather than runs to fail, when a precondition it declares is not met (a command on the PATH, an environment variable set, a file, the operating system, the container engine, a TCP endpoint the device reaches), or when it exits with a code its skip_exit lists; a check the task did not select, or that the task's deadline or a cancellation reached before it started, is skipped too, saying why. No exit code means skipped unless a check declares it, and a SKIP is never counted as a PASS. Any FAIL fails the task, and with fail_on_skip so does any SKIP. A failed task's error carries each failing check's reason and the end of its output, since a failed task's returns are not kept. The output kept is the last 40 lines and 8 KiB of a failing or skipping check, with anything a terminal would act on escaped. The manifest is refused whole, before anything runs, for a check with no kind, a kind neither built in (pure, db, network, model) nor declared, a check with no timeout, an image not pinned by digest, and every other rule it breaks. A check gets no environment and no credential from the platform. A check is not checkable: whether it passes is known only by running it, so a check of this task reads and validates the manifest and the selection, then reports the task unchecked.",
			Params: []collection.Param{
				{Name: "manifest", Type: "string", Required: true, Description: "The gate manifest's path on the device. A check's dir is relative to the manifest's own directory."},
				{Name: "kinds", Type: "list of string", Description: "Run only checks of these kinds. Every kind when absent. A kind the manifest does not have is refused."},
				{Name: "checks", Type: "list of string", Description: "Run only these checks, by name, among those the kinds select. A name the manifest does not have is refused."},
				{Name: "heavy", Type: "string", Default: "include", Description: "include runs heavy checks with the rest, exclude leaves them out, only runs nothing else. A selection that runs no check at all is refused, since it would read as a pass."},
				{Name: "parallelism", Type: "int", Description: "How many checks run at once, overriding the manifest's defaults.parallelism; 1 when neither sets it, at most 16. Heavy checks still run one at a time and serial checks alone."},
				{Name: "fail_on_skip", Type: "bool", Default: "false", Description: "Fail the task when any check is skipped, for a gate whose every check must run."},
				{Name: "insecure_skip_host_key_verify", Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."},
			},
			Returns: []collection.ReturnField{
				{Name: "checks", Type: "list of dict", Returned: "on success", Description: "One entry per check in the manifest, in its order: name, kind, status (PASS, FAIL or SKIP), reason, duration_ms, exit_code (-1 when it did not run to an exit status) and tail (the end of a failing or skipping check's output)."},
				{Name: "passed", Type: "int", Returned: "on success", Description: "How many checks passed."},
				{Name: "failed", Type: "int", Returned: "on success", Description: "How many checks failed, which is zero for a task that succeeded."},
				{Name: "skipped", Type: "int", Returned: "on success", Description: "How many checks were skipped, for any reason."},
				{Name: "summary", Type: "string", Returned: "on success", Description: "The counts for a person, for example \"12 passed, 0 failed, 3 skipped\"."},
				{Name: "ledger", Type: "string", Returned: "when a check was skipped", Description: "The skipped checks grouped by reason, largest group first."},
			},
			Fragments: []string{"container_engine"},
			Examples: []collection.Example{
				{
					Name:        "Run a project's pure and db checks",
					RunbookYAML: "- name: Gate the release\n  gate.run:\n    manifest: /srv/app/gates.yaml\n    kinds: [pure, db]\n",
				},
				{
					Name:        "Run only the heavy checks, two at a time",
					RunbookYAML: "- name: Run the slow suites\n  gate.run:\n    manifest: /srv/app/gates.yaml\n    heavy: only\n    parallelism: 2\n",
				},
			},
			SeeAlso: []string{"gate.list", "exec.command"},
		},
	},
}
