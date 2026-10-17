// Package fragment holds the shared parameter sets ("fragments") a
// built-in Collection method's Doc.Fragments names: the analogue of
// Ansible's extends_documentation_fragment, where a repeated parameter is
// written once and every method sharing it points here.
//
// It is a leaf package, importing only pkg/collection, so everything that
// needs to know a method's full parameter list can reach it: the
// documentation generator and `pleiades doc`, which render it, and
// internal/validate, which refuses a task parameter no method declares.
// It used to live in internal/forge/catalogdata, which pulls in the
// generation tooling and the whole data layer with it, so the one place
// that decides whether a runbook is valid could not see these names.
package fragment

import (
	"slices"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// Builtin names every reusable collection.Fragment a built-in method's
// Doc.Fragments entry can reference.
var Builtin = map[string]collection.Fragment{
	// catalyst_connection covers the TLS-verification param every
	// net.catalyst.* method reads through the shared clientForDevice
	// helper (internal/catalog/net/catalyst/client.go). The username
	// and password themselves are never a param: they come from the
	// device's own stored credential via sdk.RunbookContext.InjectSecrets,
	// which is why this fragment names insecure_skip_verify only.
	"catalyst_connection": {
		Params: []collection.Param{
			{Name: "insecure_skip_verify", Type: "bool", Default: "false", Description: "Skip TLS certificate verification for this call. For a lab or sandbox controller with a self-signed certificate only; never set true against a production controller."},
		},
	},

	// container_engine covers the engine a method opens through
	// containerengine.Open when it does not fix one itself (gate.run,
	// build.digests.check with resolve, build.digests.refresh). On a
	// device running both Docker and Podman, Open refuses to guess and
	// asks for this parameter, so a method that opens an engine that way
	// must accept it, or the advice it gives cannot be followed.
	"container_engine": {
		Params: []collection.Param{
			{Name: "engine", Type: "string", Choices: []string{"docker", "podman"}, Description: "Which container engine to use on a device that runs more than one. Needed only there: on a device with one engine, that engine is used. An engine the device does not run is refused."},
		},
	},

	// container_spec covers a container as container.run, container.create
	// and container.recreate declare it (pkg/containerengine.ParseSpec),
	// with its safe defaults stated where a reader meets them.
	"container_spec": {
		Params: []collection.Param{
			{Name: "name", Type: "string", Required: true, Description: "The container's name, which every container.* method addresses it by."},
			{Name: "image", Type: "string", Required: true, Description: "The image to run. A tag is resolved through its registry to the digest it names now (a multi-platform image's index), and the container runs that digest, which the task returns; an image named by digest is run as named. An absent image is pulled by digest, with the registry credential bound for its host when there is one."},
			{Name: "pull", Type: "string", Default: "missing", Choices: []string{"missing", "never"}, Description: "How the image is found. missing: a tag is resolved through its registry to a digest, pulled by that digest when absent. never: the image the engine already holds under the reference as written (a loaded or air-gapped image), refused when absent and never pulled; its ID is what runs, and the digest returned is the one the engine recorded from a registry, when it has one."},
			{Name: "command", Type: "list", Description: "The command, as a list of arguments, replacing the image's. Never run through a shell unless the list names one ([/bin/sh, -c, \"...\"])."},
			{Name: "entrypoint", Type: "list", Description: "The entrypoint, as a list of arguments, replacing the image's."},
			{Name: "env", Type: "dict", Description: "Environment variables by name. Values are never reported (results, diffs and errors name the variable only), since one may come from a credential; anyone with access to the engine can still read them by inspecting the container."},
			{Name: "network", Type: "string", Default: "none", Description: "The network the container joins: none (the default: no network at all), bridge, host, or a network's name. Reaching a network is always a declared decision."},
			{Name: "ports", Type: "list", Description: "Published ports, [host_ip:]host_port:container_port[/tcp|udp|sctp]. Refused on network none or host, where nothing is published."},
			{Name: "volumes", Type: "list", Description: "Mounts, source:target[:ro|rw]: a source beginning with / is a host path (absolute and clean, never the engine's own socket), anything else a named volume. Read-only unless the mount says rw.", Risk: collection.ParamRiskHostEscape},
			{Name: "restart_policy", Type: "string", Default: "no", Choices: []string{"no", "on-failure", "always", "unless-stopped"}, Description: "The engine's restart policy for the container."},
			{Name: "labels", Type: "dict", Description: "Labels on the container. org.pleiades.image, which records the image the task named, is this platform's own."},
			{Name: "workdir", Type: "string", Description: "The process's working directory, an absolute path inside the container."},
			{Name: "user", Type: "string", Description: "The user, or user:group, the process runs as."},
			{Name: "read_only", Type: "bool", Default: "false", Description: "Give the container a read-only root filesystem."},
			{Name: "privileged", Type: "bool", Default: "false", Description: "Run the container privileged: every capability and every device of the host, which is root on the host for whoever controls the container.", Risk: collection.ParamRiskHostEscape},
			{Name: "cap_add", Type: "list", Description: "Kernel capabilities to add, by name (NET_ADMIN, SYS_TIME).", Risk: collection.ParamRiskHostEscape},
			{Name: "devices", Type: "list", Description: "Host devices to give the container, /dev/path[:/dev/path-inside].", Risk: collection.ParamRiskHostEscape},
		},
	},

	// catalyst_pagination covers the one tuning knob the two
	// EachDevice-based methods (device_facts, reachability) read.
	"catalyst_pagination": {
		Params: []collection.Param{
			{Name: "page_size", Type: "int", Default: "500", Description: "How many devices to request per page from the Catalyst Center API. A tuning knob, not a correctness one: an invalid or non-positive value silently falls back to the default."},
		},
	},
}

// Params returns every parameter the named fragments add, in the order
// named, and, sorted, every name Builtin does not know. A caller deciding
// what a method accepts treats an unknown name as adding nothing, since
// nothing documents what it would add.
func Params(names []string) (params []collection.Param, unknown []string) {
	for _, name := range names {
		frag, ok := Builtin[name]
		if !ok {
			unknown = append(unknown, name)
			continue
		}
		params = append(params, frag.Params...)
	}
	slices.Sort(unknown)
	return params, unknown
}

// Declared returns the set of parameter names d's own documentation
// declares: its Doc.Params plus every param its named fragments add.
func Declared(d collection.Descriptor) map[string]bool {
	declared := make(map[string]bool, len(d.Manifest.Doc.Params))
	for _, p := range d.Manifest.Doc.Params {
		declared[p.Name] = true
	}
	shared, _ := Params(d.Manifest.Doc.Fragments)
	for _, p := range shared {
		declared[p.Name] = true
	}
	return declared
}
