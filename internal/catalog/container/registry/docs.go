// The container.registry.* methods' documentation, kept equal to
// internal/forge/catalogdata's copy by internal/archtest's
// TestCatalogDataDocsMatchTheRegistry.
package registry

import (
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// fileNote is where every container.registry.* method keeps a login.
const fileNote = " The login file is on the machine the engine runs on (this machine's own when the engine is reached through its local socket, otherwise the device's, over its SSH login): auth_file, or the engine's own default under the login's home, ~/.docker/config.json for Docker and ~/.config/containers/auth.json for Podman."

// Shared parameters.
var (
	hostKeyParam  = collection.Param{Name: sdk.ParamInsecureSkipHostKeyVerify, Type: "bool", Default: "false", Description: "Skip SSH host key verification for this task, when the engine or its machine is reached over SSH. This removes protection against a machine in the middle answering for the device, so set it only for a target you have decided does not need it."}
	authFileParam = collection.Param{Name: paramAuthFile, Type: "string", Description: "The login file, an absolute path on the engine's machine. Absent means the engine's own default."}
)

func loginDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Keeps the bound registry credential in the engine's login file.",
		Description: "Logs the engine's machine in to registry with the registry credential bound to the run for that host: never a task parameter, and refused when none is bound. The registry is first asked, through the engine, whether it accepts the account; only then is the login written to the engine's login file, mode 0600 from the moment it exists, under a directory made 0700 when missing, keeping the file's other logins and settings. The password stays on that machine, readable by its owner and by root, so every run warns, changed or not. A file that sends the registry's login to a credential helper is refused. A login already holding this account is no change. A run that added the login records removing it as the undo. The password reaches no result, warning, error or journal entry. A check asks the registry and reads the file, and writes nothing." + fileNote,
		Params: []collection.Param{
			{Name: paramRegistry, Type: "string", Required: true, Description: "The registry host, with its port when it has one (ghcr.io, registry.example.com:5000); docker.io for Docker Hub."},
			authFileParam,
			hostKeyParam,
		},
		Fragments: []string{"container_engine"},
		Returns: []collection.ReturnField{
			{Name: statRegistry, Type: "string", Returned: "always", Description: "The registry host."},
			{Name: statUsername, Type: "string", Returned: "always", Description: "The account logged in as."},
			{Name: statAuthFile, Type: "string", Returned: "always", Description: "The login file."},
			{Name: sdk.StatWarnings, Type: "list of string", Returned: "always", Description: "That the password is stored on the machine, and where."},
		},
		Examples: []collection.Example{{Name: "Let the engine pull from a private registry", RunbookYAML: "- name: Log in to the registry\n  container.registry.login:\n    registry: registry.example.com\n"}},
		SeeAlso:  []string{"container.registry.logout", "container.registry.info", "container.image.pull"},
	}
}

func logoutDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Removes a registry's login from the engine's login file.",
		Description: "Removes registry's login from the engine's login file, keeping its other logins and settings, and the file mode 0600. A registry with no login there is no change. A logout has no undo, since the password it removes is not kept. A check reads the file and writes nothing." + fileNote,
		Params: []collection.Param{
			{Name: paramRegistry, Type: "string", Required: true, Description: "The registry host, as container.registry.login named it."},
			authFileParam,
			hostKeyParam,
		},
		Fragments: []string{"container_engine"},
		Returns: []collection.ReturnField{
			{Name: statRegistry, Type: "string", Returned: "always", Description: "The registry host."},
			{Name: statAuthFile, Type: "string", Returned: "always", Description: "The login file."},
		},
		Examples: []collection.Example{{Name: "Remove a login", RunbookYAML: "- name: Log out of the registry\n  container.registry.logout:\n    registry: registry.example.com\n"}},
		SeeAlso:  []string{"container.registry.login"},
	}
}

func infoDoc() collection.Doc {
	return collection.Doc{
		Summary:     "Reports the registries the engine's login file holds a login for.",
		Description: "Reads the engine's login file and reports each registry it holds a login for, with the account name; with registry, also whether that one is logged in and as whom. A password is never read out. It changes nothing; a check is the same read." + fileNote,
		Params: []collection.Param{
			{Name: paramRegistry, Type: "string", Description: "A registry host to report on."},
			authFileParam,
			hostKeyParam,
		},
		Fragments: []string{"container_engine"},
		Returns: []collection.ReturnField{
			{Name: statAuthFile, Type: "string", Returned: "always", Description: "The login file."},
			{Name: statRegistries, Type: "list of dict", Returned: "always", Description: "One entry per login: registry and username."},
			{Name: statLoggedIn, Type: "bool", Returned: "with registry", Description: "Whether registry has a login."},
			{Name: statUsername, Type: "string", Returned: "with registry", Description: "The account registry is logged in as, empty when none."},
		},
		Examples: []collection.Example{{Name: "Is the engine logged in", RunbookYAML: "- name: Read the login\n  container.registry.info:\n    registry: registry.example.com\n  register: login\n"}},
		SeeAlso:  []string{"container.registry.login"},
	}
}
