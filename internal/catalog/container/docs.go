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
