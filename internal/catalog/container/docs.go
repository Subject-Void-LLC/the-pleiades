// The container.* methods' documentation, kept equal to
// internal/forge/catalogdata's copy by internal/archtest's
// TestCatalogDataDocsMatchTheRegistry.
package container

import (
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
)

// Shared parameters and returns.
var (
	nameParam = collection.Param{Name: paramName, Type: "string", Required: true, Description: "The container's name."}
	idParam   = collection.Param{Name: paramID, Type: "string", Description: "The container's ID, which the container named must still have: an undo recorded against one container never acts on another that took its name since."}

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
		Description: "Makes the container named name exist from image as the task declares it, and run. An absent container is created and started; a stopped one that matches is started; a running one that matches is left alone. A container that differs from the declaration (its image by digest, command, entrypoint, the declared environment and labels, user, workdir, network, ports, mounts, devices, restart policy, privilege, added capabilities, read-only root, no-new-privileges) is refused, naming each difference and container.recreate, which replaces it: nothing is ever replaced here. A paused container is refused. Safe by default: no network, read-only bind mounts, restart no, no-new-privileges, and the image pulled and run by digest. A create whose answer was lost is found by a label that create alone set, never by name, so another run's container of the same name is left alone; a start that fails removes what the create made. A check makes every read and refusal a run makes, resolving the image's digest and reading the container, and pulls, creates and starts nothing. With wait, the container is a one-shot: a fresh container (a name in use is refused) runs to its exit, and the task returns its exit code and output (together at most 1 MiB, refused past it), removes it afterwards unless remove is false, and fails on an exit code accept_exit_codes does not name or on its timeout, removing it then whatever remove says, since a failed task records no undo; a one-shot runs its command, so it cannot be checked, and check_mode on one is refused at validation." + containerengine.ReachNote,
		Params: []collection.Param{
			{Name: paramWait, Type: "bool", Default: "false", Description: "Run the container to its exit, as a command: a one-shot."},
			{Name: paramRemove, Type: "bool", Default: "true", Description: "With wait, remove the container once it exits. A failed one-shot is removed whatever this says, since a failed task records no undo to remove it by."},
			{Name: paramWaitTimeout, Type: "int", Default: "3600", Description: "With wait, seconds the container may run before it is killed and the task fails, up to 86400."},
			{Name: paramAcceptExit, Type: "list", Default: "[0]", Description: "With wait, the exit codes that are success."},
			containerengine.HostKeyParam,
		},
		Fragments: []string{"container_spec", "container_engine"},
		Returns: append(append([]collection.ReturnField{nameReturn, idReturn, stateReturn}, imageReturns...),
			collection.ReturnField{Name: statExitCode, Type: "int", Returned: "with wait", Description: "The container's exit status."},
			collection.ReturnField{Name: statTimedOut, Type: "bool", Returned: "with wait", Description: "Whether it ran past wait_timeout and was killed."},
			collection.ReturnField{Name: statStdout, Type: "string", Returned: "with wait", Description: "What it wrote to standard output."},
			collection.ReturnField{Name: statStderr, Type: "string", Returned: "with wait", Description: "What it wrote to standard error."},
		),
		Examples: []collection.Example{
			{Name: "Run a migration image to its exit", RunbookYAML: "- name: Migrate\n  container.run:\n    name: migrate-{{ vars.release }}\n    image: registry.example/app-migrate:2.4.1\n    network: appnet\n    wait: true\n"},
			{Name: "Run a web server on a network of its own", RunbookYAML: "- name: Web\n  container.run:\n    name: web\n    image: nginx:1.27\n    network: appnet\n    ports: [\"127.0.0.1:8080:80\"]\n    volumes: [\"/srv/web/conf:/etc/nginx/conf.d\"]\n    restart_policy: unless-stopped\n"},
		},
		SeeAlso: []string{"container.create", "container.recreate", "container.stop", "container.remove"},
	}
}

func createDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Makes a container exist as declared, without starting it.",
		Description: "Makes the container named name exist from image as the task declares it, without starting it: container.run's declaration, comparison and refusals, with no start. An existing container that matches is left as it is, running or not; one that differs is refused, naming container.recreate. A check pulls and creates nothing." + containerengine.ReachNote,
		Params:      []collection.Param{containerengine.HostKeyParam},
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
			Description: "Starts the container named name when it is created or exited; a running one is left alone, and a paused or dead one is refused. With id, a container that no longer has that ID is refused, so an undo never starts another container that took the name. Records stopping it as the undo. A check reads the container and starts nothing." + containerengine.ReachNote,
			Params:      []collection.Param{nameParam, idParam, containerengine.HostKeyParam},
			Fragments:   []string{"container_engine"},
			Returns:     []collection.ReturnField{nameReturn, idReturn, stateReturn},
			Examples:    []collection.Example{{Name: "Start the backup job", RunbookYAML: "- name: Start backup\n  container.start:\n    name: backup\n"}},
			SeeAlso:     []string{"container.stop", "container.run"},
		}
	case "container.stop":
		return collection.Doc{
			Summary:     "Stops a running container.",
			Description: "Stops the container named name when it is running, restarting or paused: SIGTERM, then SIGKILL after timeout seconds. A stopped one is left alone. With id, a container that no longer has that ID is refused. Records starting it as the undo. A check stops nothing." + containerengine.ReachNote,
			Params:      []collection.Param{nameParam, idParam, timeout, containerengine.HostKeyParam},
			Fragments:   []string{"container_engine"},
			Returns:     []collection.ReturnField{nameReturn, idReturn, stateReturn},
			Examples:    []collection.Example{{Name: "Stop the web server", RunbookYAML: "- name: Stop web\n  container.stop:\n    name: web\n    timeout: 30\n"}},
			SeeAlso:     []string{"container.start", "container.remove"},
		}
	case "container.restart":
		return collection.Doc{
			Summary:     "Restarts a container.",
			Description: "Restarts the container named name, running or stopped: SIGTERM, SIGKILL after timeout seconds, then a start. A paused or dead one is refused. With id, a container that no longer has that ID is refused. A restart has no undo. A check restarts nothing." + containerengine.ReachNote,
			Params:      []collection.Param{nameParam, idParam, timeout, containerengine.HostKeyParam},
			Fragments:   []string{"container_engine"},
			Returns:     []collection.ReturnField{nameReturn, idReturn, stateReturn},
			Examples:    []collection.Example{{Name: "Restart after a configuration change", RunbookYAML: "- name: Restart web\n  container.restart:\n    name: web\n"}},
			SeeAlso:     []string{"container.stop", "container.start"},
		}
	}
	return collection.Doc{
		Summary:     "Removes a container.",
		Description: "Removes the container named name; an absent one is no change. A running, restarting or paused container is refused unless force is set, which kills it first. remove_volumes also removes the anonymous volumes the container's image declared; named volumes are never removed here. With id, a container that no longer has that ID is refused, so an undo never removes another container that took the name. A removal has no undo. A check removes nothing." + containerengine.ReachNote,
		Params: []collection.Param{
			nameParam, idParam,
			{Name: paramForce, Type: "bool", Default: "false", Description: "Remove a running container, killing it first."},
			{Name: paramVolumes, Type: "bool", Default: "false", Description: "Also remove the anonymous volumes the container's image declared."},
			containerengine.HostKeyParam,
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
			Description: "Reports the container named name: whether it exists, and if so everything the engine's inspect answer says that a runbook decides on: its ID, state, image, command and entrypoint, user, working directory, hostname, labels, network mode and each attached network with its address, published ports, restart policy and count, privilege, read-only root, added capabilities, memory limit, mounts, last exit code, start and finish times, whether it was killed for memory, process ID, health and the engine's last start error, and its environment variables by name only, since a value may be a credential (anyone with access to the engine can still read them by inspecting the container). An absent container is reported as exists false, not as a failure, so a runbook can branch on it. It changes nothing, so a check is the same read." + containerengine.ReachNote,
			Params:      []collection.Param{nameParam, containerengine.HostKeyParam},
			Fragments:   []string{"container_engine"},
			Returns: []collection.ReturnField{
				{Name: "exists", Type: "bool", Returned: "always", Description: "Whether a container by that name exists."},
				{Name: "name", Type: "string", Returned: "when it exists", Description: "The container's name."},
				{Name: "id", Type: "string", Returned: "when it exists", Description: "The container's ID."},
				{Name: "state", Type: "string", Returned: "when it exists", Description: "Its state as the engine names it (created, running, paused, restarting, exited, dead)."},
				{Name: "image_id", Type: "string", Returned: "when it exists", Description: "The engine's ID for the image it runs."},
				{Name: "image", Type: "string", Returned: "when it exists", Description: "The image reference a container.* method recorded on it, or empty for a container something else made."},
				{Name: "created", Type: "string", Returned: "when it exists", Description: "When it was created, as the engine writes the time."},
				{Name: "platform", Type: "string", Returned: "when it exists", Description: "The platform the engine reports for it, such as linux."},
				{Name: "restart_count", Type: "int", Returned: "when it exists", Description: "How many times the engine has restarted it."},
				{Name: "labels", Type: "dict", Returned: "when it exists", Description: "Its labels."},
				{Name: "command", Type: "list of string", Returned: "when it exists", Description: "Its command."},
				{Name: "entrypoint", Type: "list of string", Returned: "when it exists", Description: "Its entrypoint."},
				{Name: "user", Type: "string", Returned: "when it exists", Description: "The user it runs as, or empty for the image's."},
				{Name: "working_dir", Type: "string", Returned: "when it exists", Description: "Its working directory, or empty for the image's."},
				{Name: "hostname", Type: "string", Returned: "when it exists", Description: "Its hostname."},
				{Name: "env_names", Type: "list of string", Returned: "when it exists", Description: "Its environment variables, by name only."},
				{Name: "network", Type: "string", Returned: "when it exists", Description: "Its network mode: none, bridge, host, or a network's name."},
				{Name: "networks", Type: "list of dict", Returned: "when it exists", Description: "Each network it is attached to, sorted by name: name and network_id, and ip_address, gateway, mac_address and aliases when the engine gives them (a stopped container has no address)."},
				{Name: "ports", Type: "list of string", Returned: "when it exists", Description: "Each port it publishes, as docker run writes one (127.0.0.1:8080:80/tcp, the host address left out when it is every address), sorted."},
				{Name: "restart_policy", Type: "string", Returned: "when it exists", Description: "Its restart policy, or empty for none."},
				{Name: "privileged", Type: "bool", Returned: "when it exists", Description: "Whether it runs privileged."},
				{Name: "read_only", Type: "bool", Returned: "when it exists", Description: "Whether its root filesystem is read-only."},
				{Name: "cap_add", Type: "list of string", Returned: "when it exists", Description: "The capabilities added beyond the engine's default set."},
				{Name: "memory", Type: "int", Returned: "when it exists", Description: "Its memory limit in bytes, 0 for none."},
				{Name: "volumes", Type: "list of string", Returned: "when it exists", Description: "Each mount as source:destination:mode, a volume by its name and a bind by its host path."},
				{Name: "exit_code", Type: "int", Returned: "when it exists", Description: "Its last exit status, 0 when it has never exited."},
				{Name: "started_at", Type: "string", Returned: "when it exists", Description: "When it last started, as the engine writes the time."},
				{Name: "finished_at", Type: "string", Returned: "when it exists", Description: "When it last exited, as the engine writes the time."},
				{Name: "oom_killed", Type: "bool", Returned: "when it exists", Description: "Whether the kernel killed it for running out of memory."},
				{Name: "pid", Type: "int", Returned: "when it exists", Description: "Its main process's ID on the engine's host, 0 when it is not running."},
				{Name: "health", Type: "string", Returned: "when the container has a health check", Description: "Its health as the engine names it: starting, healthy or unhealthy."},
				{Name: "state_error", Type: "string", Returned: "when the engine recorded why it last failed to start", Description: "The engine's own words for it, cut to 1 KiB."},
			},
			Examples: []collection.Example{{Name: "Is the web server running?", RunbookYAML: "- name: Web state\n  container.info:\n    name: web\n  register: web\n"}},
			SeeAlso:  []string{"container.list", "container.logs"},
		}
	case "container.list":
		return collection.Doc{
			Summary:     "Lists the engine's containers.",
			Description: "Lists every container on the engine, stopped ones included, or only the running ones with all false, optionally only those whose name matches a glob, sorted by name: each one's name, ID, state, the engine's status line, creation time, command, ports, image ID, the image reference a container.* method recorded, and labels. More than 1000 containers is refused rather than cut; narrow it with match. It changes nothing, so a check is the same read." + containerengine.ReachNote,
			Params: []collection.Param{
				{Name: paramAll, Type: "bool", Default: "true", Description: "List stopped containers too, the default; false lists only the running ones."},
				{Name: paramMatch, Type: "string", Description: "Only containers whose name matches this glob (web-*)."},
				containerengine.HostKeyParam,
			},
			Fragments: []string{"container_engine"},
			Returns:   []collection.ReturnField{{Name: statContainers, Type: "list of dict", Returned: "always", Description: "The containers, each with name, id, state, status (the engine's own line, such as Up 3 minutes), created (Unix seconds), command, ports (127.0.0.1:8080->80/tcp, or 80/tcp when not published), image_id, image and labels."}},
			Examples:  []collection.Example{{Name: "Every web container, stopped ones too", RunbookYAML: "- name: Web containers\n  container.list:\n    match: web-*\n  register: webs\n"}},
			SeeAlso:   []string{"container.info"},
		}
	}
	return collection.Doc{
		Summary:     "Returns a container's last lines of output.",
		Description: "Returns the last tail lines the container named name wrote to standard output and standard error, optionally only since a timestamp or a duration (10m). More than 1 MiB together is refused rather than cut; ask for fewer lines. A container run with a terminal has one stream, returned as stdout. A container that does not exist is not a failure: exists is false and no output is reported, so a later task can decide what to do. It changes nothing, so a check is the same read, an absent container included." + containerengine.ReachNote,
		Params: []collection.Param{
			nameParam,
			{Name: paramTail, Type: "int", Default: "100", Description: "How many of the last lines to return, from 1 to 10000."},
			{Name: paramSince, Type: "string", Description: "Only lines since this RFC 3339 timestamp, Unix time, or duration (10m)."},
			containerengine.HostKeyParam,
		},
		Fragments: []string{"container_engine"},
		Returns: []collection.ReturnField{
			nameReturn,
			{Name: statExists, Type: "bool", Returned: "always", Description: "Whether a container by that name exists."},
			idReturn,
			{Name: statStdout, Type: "string", Returned: "when the container exists", Description: "What it wrote to standard output."},
			{Name: statStderr, Type: "string", Returned: "when the container exists", Description: "What it wrote to standard error."},
		},
		Examples: []collection.Example{{Name: "Why did the job fail?", RunbookYAML: "- name: Job output\n  container.logs:\n    name: backup\n    tail: 50\n  register: out\n"}},
		SeeAlso:  []string{"container.info", "container.exec"},
	}
}

func execDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Runs one command inside a running container.",
		Description: "Runs cmd inside the running container named name, through the container's own /bin/sh -c, on whichever engine the device runs, and reports its real exit code, stdout and stderr (together at most 1 MiB, refused past it). A non-zero exit is an error, after the output is recorded, the line exec.command draws. The container must be running. It cannot be checked: what a command changes is known only once it has run." + containerengine.ReachNote,
		Params: []collection.Param{
			nameParam,
			{Name: paramCmd, Type: "string", Required: true, Description: "The command line, run inside the container's /bin/sh -c.", Format: collection.ParamFormatCommand},
			containerengine.HostKeyParam,
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
		Description: "Makes the container named name match its declaration by replacing it: an absent one is created and started; one that matches is left alone (started if stopped) unless force is set; one that differs, or any one with force, is removed (forced) and created again from the declaration, then started. The differences found are returned. The old container's writable layer and anonymous volumes are gone, so a recreate has no undo. With id, a container that no longer has that ID is refused. A check reads, resolves and compares, and removes, pulls and creates nothing." + containerengine.ReachNote,
		Params: []collection.Param{
			idParam,
			{Name: paramForce, Type: "bool", Default: "false", Description: "Replace the container even when it matches the declaration."},
			containerengine.HostKeyParam,
		},
		Fragments: []string{"container_spec", "container_engine"},
		Returns: append([]collection.ReturnField{nameReturn, idReturn, stateReturn,
			{Name: "differences", Type: "list of string", Returned: "when the container existed", Description: "How the old container differed from the declaration, environment values never named."},
		}, imageReturns...),
		Examples: []collection.Example{{Name: "Roll the web server onto a new image", RunbookYAML: "- name: Web on 1.28\n  container.recreate:\n    name: web\n    image: nginx:1.28\n    network: appnet\n    ports: [\"127.0.0.1:8080:80\"]\n"}},
		SeeAlso:  []string{"container.run", "container.remove"},
	}
}

func pruneDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Removes unused containers, images, volumes or networks.",
		Description: "Removes the unused objects of the kind what names: stopped containers (created, exited or dead), dangling images, anonymous volumes no container uses (a named volume holds data someone named, and is never pruned), or networks no container uses (the engine's own bridge, host and none never). labels narrows it to objects carrying each label (key or key=value). The objects are listed first and removed by the IDs that listing found, never forced, so a run removes only what it listed (a check lists the same way, but at its own moment: what is unused can change between the two), and one that came into use since its listing is refused by the engine and reported. More than 500 is refused rather than cut; narrow it with labels. A prune has no undo. A check lists and removes nothing." + containerengine.ReachNote,
		Params: []collection.Param{
			{Name: paramWhat, Type: "string", Required: true, Choices: []string{"containers", "images", "volumes", "networks"}, Description: "What to prune."},
			{Name: paramLabels, Type: "list", Description: "Only objects carrying all of these labels, key or key=value."},
			containerengine.HostKeyParam,
		},
		Fragments: []string{"container_engine"},
		Returns:   []collection.ReturnField{{Name: statPruned, Type: "list of string", Returned: "always", Description: "The names (or IDs) removed, or that would be."}},
		Examples:  []collection.Example{{Name: "Clear finished job containers", RunbookYAML: "- name: Old job containers\n  container.prune:\n    what: containers\n    labels: [\"role=job\"]\n"}},
		SeeAlso:   []string{"container.remove"},
	}
}

func copyInDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Puts one file into a container.",
		Description: "Makes the file at dest inside the container named name hold content, or the bytes of src, a file in the project directory, with mode. The file is read first and written only when its bytes or mode differ, so a second run with the same bytes writes nothing. src is read by the command line, which holds the project (relative, inside it, through no symbolic link, never .pleiades, at most 64 MiB); a Walk-tier Runner cannot see the project, so there src is refused and content is the way. The container may be running or stopped. The bytes the file held before are not kept, so the write has no undo. A check reads and compares, and writes nothing." + containerengine.ReachNote,
		Params: []collection.Param{
			nameParam,
			{Name: paramDest, Type: "string", Required: true, Description: "The file's absolute path inside the container; its directory must exist."},
			{Name: paramContent, Type: "string", Description: "The bytes the file should hold. Set content or src, not both."},
			{Name: paramSrc, Type: "string", Description: "A file in the project directory whose bytes the file should hold, relative to it."},
			{Name: paramMode, Type: "string", Default: "0644", Description: "The file's mode, in octal."},
			containerengine.HostKeyParam,
		},
		Fragments: []string{"container_engine"},
		Returns: []collection.ReturnField{
			nameReturn,
			{Name: paramDest, Type: "string", Returned: "always", Description: "The file's path inside the container."},
			{Name: statChecksum, Type: "string", Returned: "always", Description: "The SHA-256 of the bytes it holds."},
			{Name: statSize, Type: "int", Returned: "always", Description: "Their size."},
		},
		Examples: []collection.Example{{Name: "Drop a configuration file into a running container", RunbookYAML: "- name: Web config\n  container.copy_in:\n    name: web\n    dest: /etc/nginx/conf.d/site.conf\n    src: files/site.conf\n"}},
		SeeAlso:  []string{"container.copy_out", "file.copy"},
	}
}

func copyOutDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Reads one text file out of a container.",
		Description: "Returns the text of the file at src inside the container named name, running or stopped, with its size and SHA-256. A file over max_bytes, or one that is not valid UTF-8 text, is refused rather than cut or mangled. A file that is not there, in a container or because the container is not there, is not a failure: exists is false and no text is reported, so a later task can decide what to do (container.info says which). It changes nothing, so a check is the same read, an absent file included." + containerengine.ReachNote,
		Params: []collection.Param{
			nameParam,
			{Name: paramSrc, Type: "string", Required: true, Description: "The file's absolute path inside the container."},
			{Name: paramMax, Type: "int", Default: "1048576", Description: "The largest file read, in bytes, up to 16 MiB."},
			containerengine.HostKeyParam,
		},
		Fragments: []string{"container_engine"},
		Returns: []collection.ReturnField{
			nameReturn,
			{Name: paramSrc, Type: "string", Returned: "always", Description: "The path read, as src gave it."},
			{Name: statExists, Type: "bool", Returned: "always", Description: "Whether the file is there to read."},
			{Name: statText, Type: "string", Returned: "when exists", Description: "The file's text."},
			{Name: statSize, Type: "int", Returned: "when exists", Description: "Its size in bytes."},
			{Name: statChecksum, Type: "string", Returned: "when exists", Description: "Its SHA-256."},
		},
		Examples: []collection.Example{{Name: "Read the app's version file", RunbookYAML: "- name: Version\n  container.copy_out:\n    name: web\n    src: /opt/app/VERSION\n  register: version\n"}},
		SeeAlso:  []string{"container.copy_in", "container.exec"},
	}
}
