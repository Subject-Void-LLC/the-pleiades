// The container.* methods' documentation, kept equal to
// internal/forge/catalogdata's copy by internal/archtest's
// TestCatalogDataDocsMatchTheRegistry.
package container

import (
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// reachNote is how every container.* method reaches its engine.
const reachNote = " The engine is reached the first way onboarding proved for the device (this machine's socket, an SSH channel to the engine's socket, the engine's own dial-stdio over SSH, or mutual TLS), its identity checked against the one onboarding recorded, and every request outside the operations this method declares is refused before it reaches the engine."

// Shared parameters and returns.
var (
	hostKeyParam = collection.Param{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task, when the engine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."}
	nameParam    = collection.Param{Name: paramName, Type: "string", Required: true, Description: "The container's name."}
	idParam      = collection.Param{Name: paramID, Type: "string", Description: "The container's ID, which the container named must still have: an undo recorded against one container never acts on another that took its name since."}

	idReturn    = collection.ReturnField{Name: statID, Type: "string", Returned: "when the container exists", Description: "The container's ID."}
	nameReturn  = collection.ReturnField{Name: statName, Type: "string", Returned: "always", Description: "The container's name."}
	stateReturn = collection.ReturnField{Name: statState, Type: "string", Returned: "when the container exists or would", Description: "The container's state as the engine names it (created, running, exited, paused), after the task."}
)

// imageReturns are what run and create report of the image.
var imageReturns = []collection.ReturnField{
	{Name: statImage, Type: "string", Returned: "always", Description: "The image by digest: the one the container runs, or would."},
	{Name: statDigest, Type: "string", Returned: "always", Description: "The image's digest."},
	{Name: statImageID, Type: "string", Returned: "when the image is on the engine", Description: "The engine's ID for the image."},
	{Name: statPulled, Type: "bool", Returned: "always", Description: "Whether the image had to be pulled (by a check, would be)."},
	{Name: statEnv, Type: "list of string", Returned: "always", Description: "The environment variables the task set, by name only."},
}

func runDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Makes a container exist as declared, and running.",
		Description: "Makes the container named name exist from image as the task declares it, and run. An absent container is created and started; a stopped one that matches is started; a running one that matches is left alone. A container that differs from the declaration (its image by digest, command, entrypoint, the declared environment and labels, network, ports, mounts, restart policy, privilege, added capabilities, read-only root) is refused, naming each difference and container.recreate, which replaces it: nothing is ever replaced here. A paused container is refused. Safe by default: no network, read-only bind mounts, restart no, no-new-privileges, and the image pulled and run by digest. A create whose answer was lost is found by name rather than sent twice, and a start that fails removes what the create made. A check makes every read and refusal a run makes, resolving the image's digest and reading the container, and pulls, creates and starts nothing." + reachNote,
		Params:      []collection.Param{hostKeyParam},
		Fragments:   []string{"container_spec", "container_engine"},
		Returns:     append([]collection.ReturnField{nameReturn, idReturn, stateReturn}, imageReturns...),
		Examples: []collection.Example{
			{Name: "Run a web server on a network of its own", RunbookYAML: "- name: Web\n  container.run:\n    name: web\n    image: nginx:1.27\n    network: appnet\n    ports: [\"127.0.0.1:8080:80\"]\n    volumes: [\"/srv/web/conf:/etc/nginx/conf.d\"]\n    restart_policy: unless-stopped\n"},
		},
		SeeAlso: []string{"container.create", "container.recreate", "container.stop", "container.remove"},
	}
}

func createDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Makes a container exist as declared, without starting it.",
		Description: "Makes the container named name exist from image as the task declares it, without starting it: container.run's declaration, comparison and refusals, with no start. An existing container that matches is left as it is, running or not; one that differs is refused, naming container.recreate. A check pulls and creates nothing." + reachNote,
		Params:      []collection.Param{hostKeyParam},
		Fragments:   []string{"container_spec", "container_engine"},
		Returns:     append([]collection.ReturnField{nameReturn, idReturn, stateReturn}, imageReturns...),
		Examples: []collection.Example{
			{Name: "Create a job container to start later", RunbookYAML: "- name: Backup job\n  container.create:\n    name: backup\n    image: restic/restic:0.17.3\n    command: [backup, /data]\n    volumes: [\"appdata:/data\"]\n"},
		},
		SeeAlso: []string{"container.run", "container.start"},
	}
}

// lifecycleDoc is start's, stop's, restart's or remove's documentation.
func lifecycleDoc(fqcn string) collection.Doc {
	timeout := collection.Param{Name: paramTimeout, Type: "int", Default: "10", Description: "Seconds the container's process has to stop after SIGTERM before it is killed, up to 3600."}
	switch fqcn {
	case "container.start":
		return collection.Doc{
			Summary:     "Starts a stopped container.",
			Description: "Starts the container named name when it is created or exited; a running one is left alone, and a paused or dead one is refused. With id, a container that no longer has that ID is refused, so an undo never starts another container that took the name. Records stopping it as the undo. A check reads the container and starts nothing." + reachNote,
			Params:      []collection.Param{nameParam, idParam, hostKeyParam},
			Fragments:   []string{"container_engine"},
			Returns:     []collection.ReturnField{nameReturn, idReturn, stateReturn},
			Examples:    []collection.Example{{Name: "Start the backup job", RunbookYAML: "- name: Start backup\n  container.start:\n    name: backup\n"}},
			SeeAlso:     []string{"container.stop", "container.run"},
		}
	case "container.stop":
		return collection.Doc{
			Summary:     "Stops a running container.",
			Description: "Stops the container named name when it is running, restarting or paused: SIGTERM, then SIGKILL after timeout seconds. A stopped one is left alone. With id, a container that no longer has that ID is refused. Records starting it as the undo. A check stops nothing." + reachNote,
			Params:      []collection.Param{nameParam, idParam, timeout, hostKeyParam},
			Fragments:   []string{"container_engine"},
			Returns:     []collection.ReturnField{nameReturn, idReturn, stateReturn},
			Examples:    []collection.Example{{Name: "Stop the web server", RunbookYAML: "- name: Stop web\n  container.stop:\n    name: web\n    timeout: 30\n"}},
			SeeAlso:     []string{"container.start", "container.remove"},
		}
	case "container.restart":
		return collection.Doc{
			Summary:     "Restarts a container.",
			Description: "Restarts the container named name, running or stopped: SIGTERM, SIGKILL after timeout seconds, then a start. A paused or dead one is refused. With id, a container that no longer has that ID is refused. A restart has no undo. A check restarts nothing." + reachNote,
			Params:      []collection.Param{nameParam, idParam, timeout, hostKeyParam},
			Fragments:   []string{"container_engine"},
			Returns:     []collection.ReturnField{nameReturn, idReturn, stateReturn},
			Examples:    []collection.Example{{Name: "Restart after a configuration change", RunbookYAML: "- name: Restart web\n  container.restart:\n    name: web\n"}},
			SeeAlso:     []string{"container.stop", "container.start"},
		}
	}
	return collection.Doc{
		Summary:     "Removes a container.",
		Description: "Removes the container named name; an absent one is no change. A running, restarting or paused container is refused unless force is set, which kills it first. remove_volumes also removes the anonymous volumes the container's image declared; named volumes are never removed here. With id, a container that no longer has that ID is refused, so an undo never removes another container that took the name. A removal has no undo. A check removes nothing." + reachNote,
		Params: []collection.Param{
			nameParam, idParam,
			{Name: paramForce, Type: "bool", Default: "false", Description: "Remove a running container, killing it first."},
			{Name: paramVolumes, Type: "bool", Default: "false", Description: "Also remove the anonymous volumes the container's image declared."},
			hostKeyParam,
		},
		Fragments: []string{"container_engine"},
		Returns:   []collection.ReturnField{nameReturn, idReturn, stateReturn},
		Examples:  []collection.Example{{Name: "Remove an old container", RunbookYAML: "- name: Remove the old web\n  container.remove:\n    name: web-old\n    force: true\n"}},
		SeeAlso:   []string{"container.stop", "container.recreate"},
	}
}

// readDoc is info's, list's or logs' documentation.
func readDoc(fqcn string) collection.Doc {
	switch fqcn {
	case "container.info":
		return collection.Doc{
			Summary:     "Reports one container as the engine sees it.",
			Description: "Reports the container named name: whether it exists, and if so its ID, state, image, command, labels, network, restart policy, privilege, mounts, last exit code and start time, and its environment variables by name only, since a value may be a credential (anyone with access to the engine can still read them by inspecting the container). An absent container is reported as not existing, not as a failure. It changes nothing, so a check is the same read." + reachNote,
			Params:      []collection.Param{nameParam, hostKeyParam},
			Fragments:   []string{"container_engine"},
			Returns: []collection.ReturnField{
				{Name: statExists, Type: "bool", Returned: "always", Description: "Whether a container by that name exists."},
				{Name: statID, Type: "string", Returned: "when it exists", Description: "The container's ID."},
				{Name: statState, Type: "string", Returned: "when it exists", Description: "Its state as the engine names it."},
				{Name: statEnv, Type: "list of string", Returned: "when it exists", Description: "Its environment variables, by name only."},
			},
			Examples: []collection.Example{{Name: "Is the web server running?", RunbookYAML: "- name: Web state\n  container.info:\n    name: web\n  register: web\n"}},
			SeeAlso:  []string{"container.list", "container.logs"},
		}
	case "container.list":
		return collection.Doc{
			Summary:     "Lists the engine's containers.",
			Description: "Lists the engine's running containers, or every container with all, optionally only those whose name matches a glob, sorted by name: each one's name, ID, state, image ID, the image reference a container.* method recorded, and labels. More than 1000 containers is refused rather than cut; narrow it with match. It changes nothing, so a check is the same read." + reachNote,
			Params: []collection.Param{
				{Name: paramAll, Type: "bool", Default: "false", Description: "List stopped containers too."},
				{Name: paramMatch, Type: "string", Description: "Only containers whose name matches this glob (web-*)."},
				hostKeyParam,
			},
			Fragments: []string{"container_engine"},
			Returns:   []collection.ReturnField{{Name: statContainers, Type: "list of dict", Returned: "always", Description: "The containers, each with name, id, state, image_id, image and labels."}},
			Examples:  []collection.Example{{Name: "Every web container, stopped ones too", RunbookYAML: "- name: Web containers\n  container.list:\n    all: true\n    match: web-*\n  register: webs\n"}},
			SeeAlso:   []string{"container.info"},
		}
	}
	return collection.Doc{
		Summary:     "Returns a container's last lines of output.",
		Description: "Returns the last tail lines the container named name wrote to standard output and standard error, optionally only since a timestamp or a duration (10m). More than 1 MiB together is refused rather than cut; ask for fewer lines. A container run with a terminal has one stream, returned as stdout. It changes nothing, so a check is the same read." + reachNote,
		Params: []collection.Param{
			nameParam,
			{Name: paramTail, Type: "int", Default: "100", Description: "How many of the last lines to return, from 1 to 10000."},
			{Name: paramSince, Type: "string", Description: "Only lines since this RFC 3339 timestamp, Unix time, or duration (10m)."},
			hostKeyParam,
		},
		Fragments: []string{"container_engine"},
		Returns: []collection.ReturnField{
			nameReturn, idReturn,
			{Name: statStdout, Type: "string", Returned: "always", Description: "What it wrote to standard output."},
			{Name: statStderr, Type: "string", Returned: "always", Description: "What it wrote to standard error."},
		},
		Examples: []collection.Example{{Name: "Why did the job fail?", RunbookYAML: "- name: Job output\n  container.logs:\n    name: backup\n    tail: 50\n  register: out\n"}},
		SeeAlso:  []string{"container.info", "container.exec"},
	}
}

func execDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Runs one command inside a running container.",
		Description: "Runs cmd inside the running container named name, through the container's own /bin/sh -c, on whichever engine the device runs, and reports its real exit code, stdout and stderr (together at most 1 MiB, refused past it). A non-zero exit is an error, after the output is recorded, the line exec.command draws. The container must be running. It cannot be checked: what a command changes is known only once it has run." + reachNote,
		Params: []collection.Param{
			nameParam,
			{Name: paramCmd, Type: "string", Required: true, Description: "The command line, run inside the container's /bin/sh -c.", Format: collection.ParamFormatCommand},
			hostKeyParam,
		},
		Fragments: []string{"container_engine"},
		Returns: []collection.ReturnField{
			nameReturn,
			{Name: statExitCode, Type: "int", Returned: "always", Description: "The command's real exit status."},
			{Name: statStdout, Type: "string", Returned: "always", Description: "What it wrote to standard output."},
			{Name: statStderr, Type: "string", Returned: "always", Description: "What it wrote to standard error."},
		},
		Examples: []collection.Example{{Name: "Read the app's version inside its container", RunbookYAML: "- name: App version\n  container.exec:\n    name: web\n    cmd: cat /opt/app/VERSION\n  register: version\n"}},
		SeeAlso:  []string{"container.logs", "exec.command"},
	}
}

func recreateDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Replaces a container with its declaration.",
		Description: "Makes the container named name match its declaration by replacing it: an absent one is created and started; one that matches is left alone (started if stopped) unless force is set; one that differs, or any one with force, is removed (forced) and created again from the declaration, then started. The differences found are returned. The old container's writable layer and anonymous volumes are gone, so a recreate has no undo. With id, a container that no longer has that ID is refused. A check reads, resolves and compares, and removes, pulls and creates nothing." + reachNote,
		Params: []collection.Param{
			idParam,
			{Name: paramForce, Type: "bool", Default: "false", Description: "Replace the container even when it matches the declaration."},
			hostKeyParam,
		},
		Fragments: []string{"container_spec", "container_engine"},
		Returns: append([]collection.ReturnField{nameReturn, idReturn, stateReturn,
			{Name: "differences", Type: "list of string", Returned: "when the container existed", Description: "How the old container differed from the declaration, environment values never named."},
		}, imageReturns...),
		Examples: []collection.Example{{Name: "Roll the web server onto a new image", RunbookYAML: "- name: Web on 1.28\n  container.recreate:\n    name: web\n    image: nginx:1.28\n    network: appnet\n    ports: [\"127.0.0.1:8080:80\"]\n"}},
		SeeAlso:  []string{"container.run", "container.remove"},
	}
}
