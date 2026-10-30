// Package clispec declares the pleiades CLI's command tree once, as data,
// rather than scattering it across one hand-written usage string per
// print*Usage function in cmd/pleiades. Two consumers read this same
// tree: cmd/pleiades's own --help text at every level (main.go, forge.go,
// inventory.go), and tools/gendocs' generated docs/reference/cli.md.
// A command gains --help coverage and generated-reference coverage
// together the moment it is added to Root, the same property this
// project's other completeness gates (checkTaskKeysComplete,
// checkRunbookSchemaComplete) already hold for runbook keys and JSON
// Schema properties, applied here to the command tree instead.
//
// This is deliberately not a parser: flag.FlagSet still does the actual
// argument parsing in each command's own file under cmd/pleiades (init.go,
// run.go, and so on), and per-flag --help output for a single command
// still comes from flag.FlagSet's own PrintDefaults, driven by the same
// fs.String/fs.Bool calls those files already had. Root exists so the
// command *tree* -- names, one-line synopses, and the flags each command
// accepts -- has exactly one written-down shape, matching this project's
// own "generate from a table" convention (pkg/collection.Manifest,
// internal/api.Route) rather than inventing a fourth one for the CLI.
//
// Living under internal/ rather than inside cmd/pleiades itself is what
// lets tools/gendocs (a separate main package) read it too: two
// independent binaries sharing one internal/ package within the same
// module is the ordinary case that visibility rule exists for.
package clispec

import (
	"fmt"
	"strings"
)

// Command is one CLI command or subcommand's declarative shape.
type Command struct {
	// Name is this command's own word, e.g. "run" or "new-collection",
	// never the full path: a parent joins names when rendering a
	// subcommand's full invocation.
	Name string

	// Positional documents this command's own positional argument, e.g.
	// "<runbook.yaml>" or "[runbook.yaml]". Empty if it takes none.
	Positional string

	// Synopsis is one line: shown in a parent's own "commands:" table and
	// as this command's summary on its generated reference page.
	Synopsis string

	// Flags documents every flag.FlagSet entry this command's handler
	// parses, in the same order that handler's own fs.String/fs.Bool
	// calls declare them.
	Flags []Flag

	// Examples are paste-ready shell invocations, shown only on the
	// generated reference page (kept out of terminal --help, which stays
	// as terse as it already was).
	Examples []string

	// Subcommands nests one level of dispatch, mirroring cmd/pleiades's
	// own commandFunc map shape (main.go's commands, forge.go's
	// forgeCommands, inventory.go's inventoryCommands).
	Subcommands []Command
}

// Flag documents one flag a command's handler parses.
type Flag struct {
	Name    string
	Type    string
	Default string
	Doc     string
}

// Root is the full pleiades command tree. Every entry's Name, Positional
// and Flags are transcribed from the flag.FlagSet calls and usage strings
// already in cmd/pleiades's own command files; none of it is invented
// here. Each command's own file remains the source of truth for what it
// actually parses; this tree is where the same information is recorded a
// second time, in one written-down shape, for --help and the generated
// reference page to both read.
var Root = Command{
	Name:     "pleiades",
	Synopsis: "Crawl tier: no server, no database, no broker.",
	Subcommands: []Command{
		{
			Name:     "init",
			Synopsis: "scaffold a new project in the current directory",
			Flags: []Flag{
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory to scaffold, made when it does not exist"},
			},
			Examples: []string{"pleiades init"},
		},
		{
			Name:       "add-host",
			Positional: "<name>",
			Synopsis:   "add a host to the static inventory",
			Flags: []Flag{
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
				{Name: "type", Type: "string", Default: "", Doc: "device type (e.g. linux_server, cisco_router)"},
				{Name: "classify", Type: "string", Default: "", Doc: "comma-separated classification path (e.g. linux_server,debian_family,ubuntu), an alternative to --type"},
				{Name: "tags", Type: "string", Default: "", Doc: "comma-separated tags (repeatable); --tag is the same flag"},
				{Name: "tag", Type: "string", Default: "", Doc: "a tag, or several separated by commas (repeatable); --tags is the same flag"},
				{Name: "set", Type: "key=value", Default: "", Doc: "device property as key=value (repeatable)"},
			},
			Examples: []string{"pleiades add-host web01 --type linux_server --tags prod,web"},
		},
		{
			Name:       "set-host",
			Positional: "<name>",
			Synopsis:   "change an existing host's properties and tags",
			Flags: []Flag{
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
				{Name: "set", Type: "key=value", Default: "", Doc: "device property to add or replace, as key=value (repeatable)"},
				{Name: "unset", Type: "string", Default: "", Doc: "device property to remove (repeatable)"},
				{Name: "tag", Type: "string", Default: "", Doc: "tag to add, or several separated by commas (repeatable)"},
				{Name: "tags", Type: "string", Default: "", Doc: "same as --tag"},
				{Name: "untag", Type: "string", Default: "", Doc: "tag to remove, or several separated by commas (repeatable)"},
			},
			Examples: []string{
				"pleiades set-host web01 --set port=2222",
				"pleiades set-host r1 --tag core --untag staging",
				`pleiades set-host win01 --set "tls_ca_pem=$(cat ca.pem)"`,
			},
		},
		{
			Name:       "grant-capability",
			Positional: "<device> <capability>",
			Synopsis:   "assert that a device holds a capability an external program defines, with a reason",
			Flags: []Flag{
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
				{Name: "reason", Type: "string", Default: "", Doc: "why this device holds the capability, recorded with the grant (required)"},
				{Name: "set", Type: "key=value", Default: "", Doc: "grant property as key=value (repeatable)"},
			},
			Examples: []string{
				`pleiades grant-capability tenancy oci.tenancy_api --set tenancy_ocid=ocid1.tenancy.oc1..aaa --set region=us-ashburn-1 --reason "the lab tenancy"`,
			},
		},
		{
			Name:       "revoke-capability",
			Positional: "<device> <capability>",
			Synopsis:   "take back a device's grant of a capability an external program defines",
			Flags: []Flag{
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
			},
			Examples: []string{"pleiades revoke-capability tenancy oci.tenancy_api"},
		},
		{
			Name:       "trust-host",
			Positional: "<device>",
			Synopsis:   "trust a device's SSH host keys, read from its VM's console or taken on first connect",
			Flags: []Flag{
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
				{Name: "from-console", Type: "string", Default: "", Doc: "the VirtualBox host running the device's VM, whose console log holds its host keys"},
				{Name: "vm", Type: "string", Default: "", Doc: "with --from-console, the VM's name on the host, when it is not the device's"},
				{Name: "first-connect", Type: "bool", Default: "false", Doc: "trust whatever host keys answer at the device's address, unverified; refused for a device not flagged ephemeral unless --reason says why"},
				{Name: "reason", Type: "string", Default: "", Doc: "with --first-connect on a device not flagged ephemeral (a lab node an import flags), why its keys are trusted unverified; recorded on the device"},
				{Name: "replace", Type: "bool", Default: "false", Doc: "drop keys already trusted for the device's address that differ, as after a VM is made again"},
				{Name: "timeout", Type: "duration", Default: "5m0s", Doc: "how long to wait for the keys"},
			},
			Examples: []string{
				"pleiades trust-host ubuntu-lab --from-console vengeance",
				"pleiades trust-host lab-switch --first-connect",
			},
		},
		{
			Name:       "add-credential",
			Positional: "<device>",
			Synopsis:   "store an encrypted credential for a device",
			Flags: []Flag{
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
				{Name: "username", Type: "string", Default: "", Doc: "account name to authenticate as"},
				{Name: "password", Type: "string", Default: "", Doc: "password to authenticate with (prompted interactively if --key is also absent and this is empty)"},
				{Name: "password-stdin", Type: "bool", Default: "false", Doc: "read the password to authenticate with as one line on standard input"},
				{Name: "password-file", Type: "string", Default: "", Doc: "path to a file `pleiades seal` wrote holding the password, opened with this project's seal key or, sealed to a passphrase, with --passphrase or --passphrase-stdin"},
				{Name: "key", Type: "string", Default: "", Doc: "path to a PEM private key file to authenticate with"},
				{Name: "certificate", Type: "string", Default: "", Doc: "path to a PEM client certificate to present, which requires --key"},
				{Name: "pfx", Type: "string", Default: "", Doc: "path to a PKCS#12 (.pfx/.p12) bundle holding a certificate and its key"},
				{Name: "passphrase", Type: "bool", Default: "false", Doc: "prompt for the passphrase of the private key, the bundle or a passphrase-sealed --password-file"},
				{Name: "passphrase-stdin", Type: "bool", Default: "false", Doc: "read the passphrase of the private key, the bundle or a passphrase-sealed --password-file as one line on standard input"},
				{Name: "enable-password", Type: "bool", Default: "false", Doc: "prompt for the secret a network device asks for to enter privileged mode (Cisco's enable), when this account lands in user mode"},
				{Name: "enable-password-stdin", Type: "bool", Default: "false", Doc: "read that enable secret as one line on standard input; with --password-stdin it is the second line"},
				{Name: "generate", Type: "bool", Default: "false", Doc: "generate a new random ed25519 key and password, for a machine Pleiades will create; prints only the public key"},
				{Name: "replace", Type: "bool", Default: "false", Doc: "with --generate, replace a credential already stored for the device"},
				{Name: "token-stdin", Type: "bool", Default: "false", Doc: "read a bearer token as one line on standard input, for a device whose API takes one (a Kubernetes cluster, a language model provider's API key)"},
			},
			Examples: []string{
				"pleiades add-credential web01 --username admin",
				"pleiades add-credential win01 --certificate client.pem --key client.key",
				"pleiades add-credential cluster1 --token-stdin < token",
				"pleiades add-credential ubuntu-lab --username root --generate",
				"pleiades add-credential web01 --username root --password-file web01.sealed",
			},
		},
		{
			Name:     "seal-key",
			Synopsis: "print this project's public seal key, making the key pair on first use",
			Flags: []Flag{
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
				{Name: "json", Type: "bool", Default: "false", Doc: "print one JSON document instead of text"},
			},
			Examples: []string{"pleiades seal-key"},
		},
		{
			Name:     "seal",
			Synopsis: "seal a secret into a file only one project's key, or a passphrase, opens",
			Flags: []Flag{
				{Name: "to", Type: "string", Default: "", Doc: "the public seal key to seal to, as `pleiades seal-key` prints it, or @file holding it"},
				{Name: "passphrase", Type: "bool", Default: "false", Doc: "seal to a passphrase, typed at a prompt, instead of a key"},
				{Name: "passphrase-stdin", Type: "bool", Default: "false", Doc: "seal to a passphrase read as one line on standard input"},
				{Name: "secret-stdin", Type: "bool", Default: "false", Doc: "read the secret as one line on standard input instead of a prompt"},
				{Name: "purpose", Type: "string", Default: "password", Doc: "what the secret is for"},
				{Name: "out", Type: "string", Default: "", Doc: "the file to write, which must not exist"},
			},
			Examples: []string{
				`pleiades seal --to "$(pleiades seal-key --dir web-proj)" --out web01.sealed`,
				"pleiades seal --to @web-proj.sealkey --out web01.sealed",
				"pleiades seal --passphrase --out web01.sealed",
			},
		},
		{
			Name:       "add-registry-credential",
			Positional: "<registry host>",
			Synopsis:   "store an encrypted account for one container registry, given only to pulls and pushes on that registry",
			Flags: []Flag{
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
				{Name: "username", Type: "string", Default: "", Doc: "the registry account"},
				{Name: "password-stdin", Type: "bool", Default: "false", Doc: "read the password or token as one line on standard input"},
			},
			Examples: []string{
				"pleiades add-registry-credential ghcr.io --username octocat --password-stdin",
				"pleiades add-registry-credential registry.example.com:5000 --username deploy",
			},
		},
		{
			Name:       "add-database-connection",
			Positional: "<name>",
			Synopsis:   "store a named PostgreSQL connection, its password encrypted, given only to a db.pg task that names it",
			Flags: []Flag{
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
				{Name: "host", Type: "string", Default: "", Doc: "the server as the device a task runs on sees it: a name, an address, or its Unix socket directory"},
				{Name: "port", Type: "int", Default: "5432", Doc: "the server's port"},
				{Name: "user", Type: "string", Default: "", Doc: "the login"},
				{Name: "password-stdin", Type: "bool", Default: "false", Doc: "read the password as one line on standard input"},
				{Name: "no-password", Type: "bool", Default: "false", Doc: "send no password (peer or trust authentication)"},
				{Name: "sslmode", Type: "string", Default: "verify-full", Doc: "verify-full, or disable for a local server (loopback or a socket)"},
				{Name: "ca-file", Type: "string", Default: "", Doc: "a PEM file of the authority the server's certificate must chain to (default: the system's roots)"},
				{Name: "server-name", Type: "string", Default: "", Doc: "the name the server's certificate is checked against, when it is not --host"},
				{Name: "insecure-transport", Type: "bool", Default: "false", Doc: "allow --sslmode disable to a server that is not local; the password and every row cross that network in the clear"},
				{Name: "live", Type: "string", Default: "", Doc: "comma-separated databases on this server a restore refuses unless the task overrides it"},
			},
			Examples: []string{
				"pleiades add-database-connection appdb --host db.internal --user app --ca-file db-ca.pem --live app --password-stdin",
				"pleiades add-database-connection local --host /var/run/postgresql --user postgres --sslmode disable --no-password",
			},
		},
		{
			Name:     "list-database-connections",
			Synopsis: "list the stored database connections, never a password",
			Flags: []Flag{
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
				{Name: "json", Type: "bool", Default: "false", Doc: "print one JSON document instead of text"},
			},
			Examples: []string{"pleiades list-database-connections --json"},
		},
		{
			Name:       "remove-database-connection",
			Positional: "<name>",
			Synopsis:   "remove a stored database connection",
			Flags: []Flag{
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
			},
			Examples: []string{"pleiades remove-database-connection appdb"},
		},
		{
			Name:       "add-secret",
			Positional: "<name>",
			Synopsis:   "store one field of a named secret, encrypted, for a runbook to name as {{ secret.<name>.<field> }} in a parameter its method resolves",
			Flags: []Flag{
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
				{Name: "field", Type: "string", Default: "", Doc: "the field of the secret to store, a lowercase identifier"},
				{Name: "value-stdin", Type: "bool", Default: "false", Doc: "read the value as one line on standard input"},
				{Name: "value-file", Type: "string", Default: "", Doc: "a file `pleiades seal` wrote holding the value"},
				{Name: "passphrase", Type: "bool", Default: "false", Doc: "prompt for the passphrase of a passphrase-sealed --value-file"},
				{Name: "passphrase-stdin", Type: "bool", Default: "false", Doc: "read the passphrase of a passphrase-sealed --value-file as one line on standard input"},
				{Name: "generate", Type: "int", Default: "0", Doc: "generate a random value of this many letters and digits (8 to 128) instead of reading one"},
				{Name: "replace", Type: "bool", Default: "false", Doc: "replace a value already stored for the field"},
			},
			Examples: []string{
				"pleiades add-secret dmvpn --field psk --generate 32",
				"pleiades add-secret dmvpn --field psk --value-file psk.sealed",
			},
		},
		{
			Name:     "list-secrets",
			Synopsis: "list the stored named secrets and their fields, never a value",
			Flags: []Flag{
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
				{Name: "json", Type: "bool", Default: "false", Doc: "print one JSON document instead of text"},
			},
			Examples: []string{"pleiades list-secrets --json"},
		},
		{
			Name:       "remove-secret",
			Positional: "<name>",
			Synopsis:   "remove a stored named secret, or one of its fields",
			Flags: []Flag{
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
				{Name: "field", Type: "string", Default: "", Doc: "remove only this field; the whole secret when empty"},
			},
			Examples: []string{"pleiades remove-secret dmvpn", "pleiades remove-secret dmvpn --field psk"},
		},
		{
			Name:       "remove-host",
			Positional: "<name>",
			Synopsis:   "remove a host from the inventory, and with flags its credential and the known_hosts lines trusting its address; the journal keeps its runs",
			Flags: []Flag{
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
				{Name: "credential", Type: "bool", Default: "false", Doc: "also remove the host's stored credential"},
				{Name: "forget-host-keys", Type: "bool", Default: "false", Doc: "also remove the known_hosts lines trusting the host's SSH address, unless another device is reached there"},
				{Name: "leave-references", Type: "bool", Default: "false", Doc: "remove the host even when another host names it in a property (a console node's console_server)"},
				{Name: "json", Type: "bool", Default: "false", Doc: "print one JSON document instead of text"},
			},
			Examples: []string{"pleiades remove-host br4 --credential --forget-host-keys", "pleiades remove-host cml --credential --json"},
		},
		{
			Name:       "remove-credential",
			Positional: "<device>",
			Synopsis:   "remove a device's stored credential, leaving the device in the inventory",
			Flags: []Flag{
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
			},
			Examples: []string{"pleiades remove-credential br4"},
		},
		{
			Name:       "import-kubeconfig",
			Positional: "<device>",
			Synopsis:   "make one kubeconfig context a kubernetes_cluster device and its stored credential, refusing anything in it that would run a program or read a file",
			Flags: []Flag{
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
				{Name: "kubeconfig", Type: "string", Default: "", Doc: "the kubeconfig to read, or - for standard input; never a default location"},
				{Name: "context", Type: "string", Default: "", Doc: "the context to import; the kubeconfig's current context when empty"},
				{Name: "replace", Type: "bool", Default: "false", Doc: "replace the connection settings and credential of a cluster already in the inventory"},
				{Name: "json", Type: "bool", Default: "false", Doc: "print the result as one JSON document"},
			},
			Examples: []string{
				"pleiades import-kubeconfig desktop --kubeconfig ~/.kube/config --context docker-desktop",
				"pleiades import-kubeconfig prod --kubeconfig prod.yaml --replace --json",
			},
		},
		{
			Name:     "import-containerlab",
			Synopsis: "make a containerlab lab's nodes generic_ssh devices, from the topology-data.json it wrote on deploy, refusing any node that is not the named lab's",
			Flags: []Flag{
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
				{Name: "topology", Type: "string", Default: "", Doc: "containerlab's topology-data.json for the lab, written on deploy"},
				{Name: "api", Type: "string", Default: "", Doc: "instead of --topology: the inventory device of a containerlab API server (onboarded, with its login stored), whose inspect of --lab is read"},
				{Name: "lab", Type: "string", Default: "", Doc: "the lab's name, which the file must be for"},
				{Name: "nodes", Type: "string", Default: "", Doc: "comma-separated nodes to import; every node when empty"},
				{Name: "username", Type: "string", Default: "", Doc: "with --key, the account every node is reached as"},
				{Name: "key", Type: "string", Default: "", Doc: "a private key file stored as every imported node's credential"},
				{Name: "trust-first-connect", Type: "bool", Default: "false", Doc: "trust whatever host keys answer at each node's address, unverified, as trust-host --first-connect does"},
				{Name: "onboard", Type: "bool", Default: "false", Doc: "onboard each node once it is imported (needs a credential and trusted host keys)"},
				{Name: "replace", Type: "bool", Default: "false", Doc: "replace the address of a node already in the inventory as a generic_ssh device"},
				{Name: "json", Type: "bool", Default: "false", Doc: "print the result as one JSON document"},
			},
			Examples: []string{
				"pleiades import-containerlab --topology clab-routing-lab/topology-data.json --lab routing-lab --username netops --key keys/id_ed25519 --trust-first-connect --onboard",
				"pleiades import-containerlab --topology clab-routing-lab/topology-data.json --lab routing-lab --nodes r1,r2 --replace --json",
				"pleiades import-containerlab --api clab-api --lab routing-lab --username netops --key keys/id_ed25519 --onboard",
			},
		},
		{
			Name:     "import-cml",
			Synopsis: "make a Cisco Modeling Labs lab's nodes generic_ssh devices at their management addresses, read from the controller's API; an address is the one the controller observed or one --address gives, never one guessed from a configuration",
			Flags: []Flag{
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
				{Name: "server", Type: "string", Default: "", Doc: "the CML controller's base URL, e.g. https://cml.lab"},
				{Name: "lab", Type: "string", Default: "", Doc: "the lab to import, by title or id"},
				{Name: "nodes", Type: "string", Default: "", Doc: "comma-separated nodes to import; every node when empty"},
				{Name: "address", Type: "string", Default: "", Doc: "NODE=IP: the node's management address, where the controller observed none or several (repeatable)"},
				{Name: "controller", Type: "string", Default: "", Doc: "a cml_controller device of this project: its api_base, TLS settings and stored login, in place of --server, --tls-*, --username and the password"},
				{Name: "username", Type: "string", Default: "", Doc: "the controller account to log in as"},
				{Name: "password-stdin", Type: "bool", Default: "false", Doc: "read the password from the first line of standard input, instead of PLEIADES_CML_PASSWORD"},
				{Name: "tls-fingerprint", Type: "string", Default: "", Doc: "trust only the controller certificate with this SHA-256 fingerprint (sha256:<hex>), whatever its issuer or dates"},
				{Name: "tls-ca", Type: "string", Default: "", Doc: "a PEM file of the CAs the controller's certificate must chain to"},
				{Name: "allow-plaintext", Type: "bool", Default: "false", Doc: "send the password over plain HTTP to a controller that is not this machine"},
				{Name: "via", Type: "string", Default: "ssh", Doc: "how the nodes are reached: ssh; telnet for a lab whose nodes run no SSH server (a login crossing in cleartext); or console, through the --controller device's console server, for nodes with no address reachable from here"},
				{Name: "trust-first-connect", Type: "bool", Default: "false", Doc: "trust whatever host keys answer at each node's address, unverified, as trust-host --first-connect does"},
				{Name: "onboard", Type: "bool", Default: "false", Doc: "onboard each node once it is imported (needs a credential and trusted host keys)"},
				{Name: "replace", Type: "bool", Default: "false", Doc: "replace the address of a node already in the inventory as the same device type"},
				{Name: "json", Type: "bool", Default: "false", Doc: "print the result as one JSON document"},
			},
			Examples: []string{
				"pleiades import-cml --server https://10.10.20.161 --lab default-lab --username developer --password-stdin --tls-fingerprint sha256:8e57dd2f... < cml.pw",
				"pleiades import-cml --server https://cml.lab --lab routing --username admin --tls-ca cml-ca.pem --address R1=10.10.20.171 --nodes R1 --json",
			},
		},
		{
			Name:     "import-gns3",
			Synopsis: "make a GNS3 project's nodes console_device devices at their telnet consoles, read from the server's own API (3.x or 2.2), skipping any node without a telnet console",
			Flags: []Flag{
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
				{Name: "server", Type: "string", Default: "", Doc: "the GNS3 server's base URL, e.g. http://gns3.lab:3080"},
				{Name: "project", Type: "string", Default: "", Doc: "the project to import, by name or id"},
				{Name: "nodes", Type: "string", Default: "", Doc: "comma-separated nodes to import; every node when empty"},
				{Name: "username", Type: "string", Default: "", Doc: "the account to log in as (a 3.x server needs one; a 2.2 server takes it as basic auth)"},
				{Name: "password-stdin", Type: "bool", Default: "false", Doc: "read the password from the first line of standard input, instead of PLEIADES_GNS3_PASSWORD"},
				{Name: "allow-plaintext", Type: "bool", Default: "false", Doc: "send the password over plain HTTP to a server that is not this machine"},
				{Name: "replace", Type: "bool", Default: "false", Doc: "replace the console address of a node already in the inventory as a console_device"},
				{Name: "json", Type: "bool", Default: "false", Doc: "print the result as one JSON document"},
			},
			Examples: []string{
				"pleiades import-gns3 --server http://127.0.0.1:3080 --project routing-lab --username admin --password-stdin < gns3.pw",
				"pleiades import-gns3 --server https://gns3.lab:3080 --project routing-lab --nodes r1,r2 --replace --json",
			},
		},
		{
			Name:       "onboard",
			Positional: "<device>",
			Synopsis:   "probe a device over its protocol and record what it proved: a generic device's capabilities, the container engines a host runs, or what a Kubernetes cluster serves",
			Flags: []Flag{
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
				{Name: "json", Type: "bool", Default: "false", Doc: "print the result as JSON, in the same model the text shows"},
				{Name: "timeout", Type: "duration", Default: "30s", Doc: "how long the probe may take"},
			},
			Examples: []string{
				"pleiades onboard edge01",
				"pleiades onboard api01 --json",
			},
		},
		{
			Name:       "validate",
			Positional: "[runbook.yaml ...]",
			Synopsis:   "check runbooks against the inventory; with none named, every one in runbooks/",
			Flags: []Flag{
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
				{Name: "tags", Type: "string", Default: "", Doc: "run only the tasks carrying one of these tags (comma-separated, repeatable); all, tagged, untagged, always and never keep Ansible's meanings, and a task tagged never runs only when named"},
				{Name: "skip-tags", Type: "string", Default: "", Doc: "leave out the tasks carrying one of these tags, even ones --tags selects (comma-separated, repeatable)"},
				{Name: "extra-vars", Type: "string", Default: "", Doc: "a variable the run will start with, read exactly as run reads it (key=value, key:=yaml or @file.yaml, repeatable); a parameter reading vars.<name> that none of these gives, with no | default(...), is reported"},
				{Name: "e", Type: "string", Default: "", Doc: "shorthand for --extra-vars"},
			},
			Examples: []string{"pleiades validate", "pleiades validate runbooks/site.yaml", "pleiades validate runbooks/*.yaml --tags web", "pleiades validate runbooks/change.yaml -e candidate=configs/r3.conf"},
		},
		{
			Name:       "run",
			Positional: "<runbook.yaml>",
			Synopsis:   "build, validate, and run a runbook",
			Flags: []Flag{
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
				{Name: "mode", Type: "string", Default: "execute", Doc: "execute applies changes; check reports what each task would change and changes nothing"},
				{Name: "verbose", Type: "bool", Default: "false", Doc: "print each task's own output (stdout, exit status, diffs), not just whether it changed"},
				{Name: "v", Type: "bool", Default: "false", Doc: "shorthand for --verbose"},
				{Name: "json", Type: "bool", Default: "false", Doc: "print the run as one JSON document on standard output, with every task's output whether or not --verbose is given; the exit status is the text view's"},
				{Name: "allow-unchecked", Type: "string", Default: "", Doc: "a method whose tasks may go unchecked without making the check incomplete (repeatable); the tasks are still listed"},
				{Name: "tags", Type: "string", Default: "", Doc: "run only the tasks carrying one of these tags (comma-separated, repeatable); all, tagged, untagged, always and never keep Ansible's meanings, and a task tagged never runs only when named"},
				{Name: "skip-tags", Type: "string", Default: "", Doc: "leave out the tasks carrying one of these tags, even ones --tags selects (comma-separated, repeatable)"},
				{Name: "forks", Type: "int", Default: "5", Doc: "how many devices are worked on at once, 1 to 1000; the default is Ansible's own"},
				{Name: "persist-connections", Type: "bool", Default: "true", Doc: "keep one SSH connection per device open between its tasks; --persist-connections=false logs in afresh for every task, and a device, group or inventory setting persist_connections: false turns it off for its devices whatever this says"},
				{Name: "extra-vars", Type: "string", Default: "", Doc: "a variable the run starts with, as key=value, key:=yaml or @file.yaml holding a mapping (repeatable); a runbook reads it as vars.<name>, a name given twice is refused, and a runbook reading one none of these gives is refused before any task runs"},
				{Name: "e", Type: "string", Default: "", Doc: "shorthand for --extra-vars"},
			},
			Examples: []string{
				"pleiades run runbooks/site.yaml",
				"pleiades run runbooks/ticket.yaml -e ticket=INC0010001",
				"pleiades run runbooks/site.yaml --verbose",
				"pleiades run runbooks/site.yaml --mode check --verbose",
				"pleiades run runbooks/site.yaml --mode check --allow-unchecked exec.command",
				"pleiades run runbooks/site.yaml --tags web --skip-tags slow",
				"pleiades run runbooks/site.yaml --persist-connections=false",
				"pleiades run runbooks/site.yaml --json",
			},
		},
		{
			Name:       "adhoc",
			Positional: "<hosts | a,b,c | -> <method> [key=value | key:=yaml ...]",
			Synopsis:   "run one method against a device, a tag or a comma list of devices, or with - (or no hosts at all) one that needs no device, with no runbook written",
			Flags: []Flag{
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
				{Name: "mode", Type: "string", Default: "execute", Doc: "execute applies changes; check reports what the method would change and changes nothing"},
				{Name: "verbose", Type: "bool", Default: "false", Doc: "print the method's own output (stdout, exit status, diffs), not just whether it changed"},
				{Name: "v", Type: "bool", Default: "false", Doc: "shorthand for --verbose"},
				{Name: "json", Type: "bool", Default: "false", Doc: "print the run as one JSON document on standard output, with the method's output; the exit status is the text view's"},
				{Name: "allow-unchecked", Type: "string", Default: "", Doc: "with --mode check, a method that may go unchecked without making the check incomplete"},
				{Name: "forks", Type: "int", Default: "5", Doc: "how many devices are worked on at once, 1 to 1000"},
				{Name: "persist-connections", Type: "bool", Default: "true", Doc: "keep one SSH connection per device open; =false logs in afresh"},
				{Name: "timeout", Type: "seconds", Default: "0", Doc: "how long the call may run on each device before it is stopped and fails, as a task's timeout key: whole seconds (40) or a duration (40s, 5m), up to 86400 seconds (0: no bound)"},
			},
			Examples: []string{
				"pleiades adhoc web01 facts.gather",
				"pleiades adhoc build01 exec.command argv:='[make, test]' --timeout 1800",
				"pleiades adhoc web exec.command cmd=uptime --verbose",
				"pleiades adhoc win01 exec.winrm.shell shell=powershell command='Get-Service WinRM' --json",
				"pleiades adhoc web pkg.apt.install name=nginx --mode check",
				"pleiades adhoc lab exec.command argv:='[cat, /etc/os-release]'",
				"pleiades adhoc br1,br2 net.ios.command command='show crypto ikev2 sa' --json",
				"pleiades adhoc net.pcap.summary src=captures/core1-wan.pcap --json",
			},
		},
		{
			Name:       "rollback",
			Positional: "<run-id>",
			Synopsis:   "undo a run from its journal: every device it changed, newest change first",
			Flags: []Flag{
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
				{Name: "mode", Type: "string", Default: "execute", Doc: "execute undoes the changes; check reports what each undo would change and changes nothing"},
				{Name: "runbook", Type: "string", Default: "", Doc: "the runbook the run ran, when a task's undo is its rollback: list; found in DIR/runbooks when unset"},
				{Name: "leave", Type: "string", Default: "", Doc: "a node whose change to leave in place rather than undo (repeatable)"},
				{Name: "allow-partial", Type: "string", Default: "", Doc: "a node whose partial undo to run anyway (repeatable)"},
				{Name: "allow-unknown", Type: "string", Default: "", Doc: "a node whose unknown effect to accept, or unsealed for a journal with no seal (repeatable)"},
				{Name: "despite-run", Type: "string", Default: "", Doc: "a later run whose changes to the same devices to undo beneath (repeatable)"},
				{Name: "verbose", Type: "bool", Default: "false", Doc: "print each undo's own output"},
				{Name: "v", Type: "bool", Default: "false", Doc: "shorthand for --verbose"},
				{Name: "json", Type: "bool", Default: "false", Doc: "print the rollback as one JSON document, with its plan, or its refusal and why"},
				{Name: "allow-unchecked", Type: "string", Default: "", Doc: "with --mode check, a method that may go unchecked without making the check incomplete"},
				{Name: "forks", Type: "int", Default: "5", Doc: "how many devices are worked on at once, 1 to 1000"},
				{Name: "persist-connections", Type: "bool", Default: "true", Doc: "keep one SSH connection per device open; =false logs in afresh"},
			},
			Examples: []string{
				"pleiades journal list",
				"pleiades rollback 063e6492-c80b-4739-9f04-516fce16f706 --mode check",
				"pleiades rollback 063e6492-c80b-4739-9f04-516fce16f706",
				"pleiades rollback 063e6492-c80b-4739-9f04-516fce16f706 --leave 'tasks[3]' --json",
			},
		},
		{
			Name:     "journal",
			Synopsis: "list this project's runs, or show one run's journal",
			Subcommands: []Command{
				{
					Name:     "list",
					Synopsis: "every run recorded in this project, oldest first, with what it changed and whether it was rolled back",
					Flags: []Flag{
						{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
						{Name: "since", Type: "string", Default: "", Doc: "only runs that began this long ago or later (2h, 30m), or at or after this RFC 3339 time"},
						{Name: "changed", Type: "bool", Default: "false", Doc: "only runs that changed something: the ones a rollback could undo"},
						{Name: "json", Type: "bool", Default: "false", Doc: "print one JSON document instead of text"},
					},
					Examples: []string{"pleiades journal list --changed --since 4h --json"},
				},
				{
					Name:       "show",
					Positional: "<run-id>",
					Synopsis:   "one run's journal: each task, its device, how it ended, and the undo it recorded",
					Flags: []Flag{
						{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
						{Name: "json", Type: "bool", Default: "false", Doc: "print one JSON document instead of text"},
					},
				},
			},
		},
		{
			Name:     "inventory",
			Synopsis: "sync devices from an external source (see 'pleiades inventory --help')",
			Subcommands: []Command{
				{
					Name:     "sync",
					Synopsis: "pull devices from an external source into the local inventory",
					Flags: []Flag{
						{Name: "dir", Type: "string", Default: ".", Doc: "project directory holding inventory.yaml"},
						{Name: "plugin", Type: "string", Default: "", Doc: "sync plugin to run (see 'pleiades inventory plugins')"},
						{Name: "endpoint", Type: "string", Default: "", Doc: "upstream base URL, overriding the plugin's default"},
						{Name: "credential", Type: "string", Default: "", Doc: "credential store entry to authenticate with, defaulting to the plugin name"},
						{Name: "page-size", Type: "int", Default: "0", Doc: "how many records to request per upstream page"},
						{Name: "read-only", Type: "bool", Default: "false", Doc: "refuse every write to the local inventory, reporting what would have changed"},
						{Name: "insecure-skip-verify", Type: "bool", Default: "false", Doc: "skip TLS certificate verification against the upstream system"},
						{Name: "set", Type: "string", Default: "", Doc: "a plugin-specific setting as key=value, repeatable (see 'pleiades inventory plugins')"},
					},
					Examples: []string{
						"pleiades inventory sync --plugin catalyst_center --endpoint https://dnac.example.com",
						"pleiades inventory sync --plugin aws --set region=us-east-1",
					},
				},
				{
					Name:     "plugins",
					Synopsis: "list the available inventory sync plugins",
					Examples: []string{"pleiades inventory plugins"},
				},
			},
		},
		{
			Name:     "collection",
			Synopsis: "approve, revoke and list the external Collection builds allowed to run (see 'pleiades collection --help')",
			Subcommands: []Command{
				{
					Name:       "approve",
					Positional: "<program>",
					Synopsis:   "approve a build of a program in PLEIADES_COLLECTIONS_DIR, after showing what it says it provides",
					Flags: []Flag{
						{Name: "digest", Type: "string", Default: "", Doc: "approve this build (sha256:<hex>) without inspecting the program, for an image build or a rolling upgrade"},
						{Name: "yes", Type: "bool", Default: "false", Doc: "approve without asking, after still showing what the program provides"},
					},
					Examples: []string{
						"pleiades collection approve note",
						"pleiades collection approve note --digest sha256:<64 hex digits>",
					},
				},
				{
					Name:       "revoke",
					Positional: "<program>",
					Synopsis:   "withdraw a program's approvals, so it stops running from its next call",
					Flags: []Flag{
						{Name: "digest", Type: "string", Default: "", Doc: "withdraw only this build's approval (default: every build of the program)"},
					},
					Examples: []string{"pleiades collection revoke note"},
				},
				{
					Name:     "list",
					Synopsis: "list every approved build, who approved it and when",
					Examples: []string{"pleiades collection list"},
				},
			},
		},
		{
			Name:     "ai",
			Synopsis: "sign prompts and decide answers a run held for review: what the ai collection needs a person for (see 'pleiades ai --help')",
			Subcommands: []Command{
				{
					Name:     "prompt",
					Synopsis: "make a prompt signing key, and sign a prompt bundle with it, at a terminal",
					Subcommands: []Command{
						{
							Name:     "keygen",
							Synopsis: "write an Ed25519 signing key outside the project and print the trusted keys entry for it; needs a person at a terminal",
							Flags: []Flag{
								{Name: "out", Type: "string", Default: "", Doc: "the private key file to write (mode 0600), outside the project"},
								{Name: "uses", Type: "string", Default: "", Doc: "the uses the key may sign for, comma separated, for the printed entry"},
								{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
							},
							Examples: []string{"pleiades ai prompt keygen --out ~/.config/pleiades/prompts.key --uses triage"},
						},
						{
							Name:       "sign",
							Positional: "<bundle.json>",
							Synopsis:   "show a prompt bundle, parameter set or routing table and, once you type sign, write its detached signature as <bundle.json>.sig; needs a person at a terminal",
							Flags: []Flag{
								{Name: "key", Type: "string", Default: "", Doc: "the private key file, as keygen writes it; refused when others can read it"},
								{Name: "dir", Type: "string", Default: ".", Doc: "project directory, whose ai/trusted-keys.yaml is checked for the key"},
							},
							Examples: []string{"pleiades ai prompt sign ai/prompts/triage.json --key ~/.config/pleiades/prompts.key"},
						},
					},
				},
				{
					Name:     "review",
					Synopsis: "list, show, approve and deny the answers runs held for review (ai/review)",
					Subcommands: []Command{
						{
							Name:     "list",
							Synopsis: "list every review, newest held first, and any file that fails its own checks",
							Flags: []Flag{
								{Name: "json", Type: "bool", Default: "false", Doc: "print JSON"},
								{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
							},
							Examples: []string{"pleiades ai review list", "pleiades ai review list --json"},
						},
						{
							Name:       "show",
							Positional: "<key>",
							Synopsis:   "show one review whole: what was asked of whom, why it was held, and the answer, escaped",
							Flags: []Flag{
								{Name: "json", Type: "bool", Default: "false", Doc: "print JSON"},
								{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
							},
							Examples: []string{"pleiades ai review show 3f2a9c1e"},
						},
						{
							Name:       "approve",
							Positional: "<key>",
							Synopsis:   "show a pending review and, once you type approve, release its answer to the next run that asks the same thing; needs a person at a terminal",
							Flags: []Flag{
								{Name: "comment", Type: "string", Default: "", Doc: "why, recorded with the decision"},
								{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
							},
							Examples: []string{"pleiades ai review approve 3f2a9c1e --comment \"checked against the ticket\""},
						},
						{
							Name:       "deny",
							Positional: "<key>",
							Synopsis:   "show a pending review and, once you type deny, fail every run that asks the same thing; needs a person at a terminal",
							Flags: []Flag{
								{Name: "comment", Type: "string", Default: "", Doc: "why, recorded with the decision"},
								{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
							},
							Examples: []string{"pleiades ai review deny 3f2a9c1e"},
						},
					},
				},
			},
		},
		{
			Name:     "import",
			Synopsis: "read another platform's export and report what this one would do with it (see 'pleiades import --help')",
			Subcommands: []Command{
				{
					Name:       "awx-credential-types",
					Positional: "<export.json>",
					Synopsis:   "check an AWX credential type export against this platform, before a migration",
					Flags: []Flag{
						{Name: "out", Type: "string", Default: "", Doc: "directory to write each importable type into as JSON, ready to POST to /credential-types"},
						{Name: "quiet", Type: "bool", Default: "false", Doc: "report only the types this platform would not import"},
					},
					Examples: []string{
						"pleiades import awx-credential-types credential_types.json",
						"pleiades import awx-credential-types --out ./types credential_types.json",
					},
				},
			},
		},
		{
			Name:     "forge",
			Synopsis: "authoring and migration tooling (see 'pleiades forge --help')",
			Subcommands: []Command{
				{
					Name:       "new-device",
					Positional: "<vendor>",
					Synopsis:   "generate a new vendor device-type package",
					Flags: []Flag{
						{Name: "dir", Type: "string", Default: ".", Doc: "repository directory to write the generated package into"},
						{Name: "type", Type: "string", Default: "", Doc: "the full record.RegisterType key (e.g. cisco_router)"},
						{Name: "capabilities", Type: "string", Default: "", Doc: "comma-separated vendor baseline capability names (e.g. AptCapable,SSHTransportCapable)"},
						{Name: "skip-existing", Type: "bool", Default: "false", Doc: "leave an already-generated entry alone instead of refusing, for regenerating a catalog in place"},
					},
				},
				{
					Name:       "new-collection",
					Positional: "<namespace.method>",
					Synopsis:   "generate a new namespaced Collection method package",
					Flags: []Flag{
						{Name: "dir", Type: "string", Default: ".", Doc: "repository directory to write the generated package into"},
						{Name: "capabilities", Type: "string", Default: "", Doc: "comma-separated required capability names (e.g. AptCapable)"},
						{Name: "transports", Type: "string", Default: "", Doc: "comma-separated supported transport names (e.g. ssh)"},
						{Name: "requires-elevation", Type: "bool", Default: "false", Doc: "whether this method needs elevated privileges on the target device"},
						{Name: "site", Type: "string", Default: "target", Doc: "where the method's code runs: target (on or against the device), controller (in the host process) or hybrid"},
						{Name: "device", Type: "string", Default: "required", Doc: "whether the method acts on a device: required, optional (decided per call by a DeviceCall the generated file stubs) or none"},
						{Name: "engine-version", Type: "string", Default: "", Doc: "minimum core engine version constraint (unparsed, e.g. >=1.0.0)"},
						{Name: "doc-json", Type: "string", Default: "", Doc: "reference documentation as a JSON pkg/collection.Doc object, or @path to read it from a file"},
						{Name: "skip-existing", Type: "bool", Default: "false", Doc: "leave an already-generated entry alone instead of refusing, for regenerating a catalog in place"},
					},
					Examples: []string{
						"pleiades forge new-collection net.junos.config --capabilities SSHTransportCapable --transports ssh",
						"pleiades forge new-collection net.junos.config --doc-json @junos-config-doc.json",
					},
				},
				{
					Name:       "new-plugin",
					Positional: "<name>",
					Synopsis:   "generate a new inventory sync plugin package",
					Flags: []Flag{
						{Name: "dir", Type: "string", Default: ".", Doc: "repository directory to write the generated package into"},
						{Name: "description", Type: "string", Default: "", Doc: "one-line help text describing the upstream system this plugin reads"},
						{Name: "endpoint", Type: "string", Default: "", Doc: "default upstream base URL (e.g. https://sandboxdnac.cisco.com)"},
						{Name: "read-only", Type: "bool", Default: "false", Doc: "declare the upstream authoritative and never written back"},
						{Name: "requires-credentials", Type: "bool", Default: "false", Doc: "declare that Connect resolves a credential, so the plugin is built with the project credential store"},
						{Name: "settings-json", Type: "string", Default: "", Doc: "per-deployment settings as a JSON list of syncplugin.SettingSpec objects, or @path to read it from a file"},
						{Name: "skip-existing", Type: "bool", Default: "false", Doc: "leave an already-generated entry alone instead of refusing, for regenerating a catalog in place"},
					},
					Examples: []string{
						"pleiades forge new-plugin netbox --description \"reads devices from a NetBox instance\" --requires-credentials",
						"pleiades forge new-plugin gcp --description \"reads Compute Engine instances\" --requires-credentials --settings-json '[{\"name\":\"project\",\"description\":\"the GCP project to read from\",\"required\":true}]'",
					},
				},
				{
					Name:       "new-view",
					Positional: "<name>",
					Synopsis:   "generate a new web UI view resource package",
					Flags: []Flag{
						{Name: "dir", Type: "string", Default: ".", Doc: "repository directory to write the generated package into"},
						{Name: "title", Type: "string", Default: "", Doc: "page heading and document title for this view"},
						{Name: "summary", Type: "string", Default: "", Doc: "one line rendered under the heading"},
						{Name: "nav-label", Type: "string", Default: "", Doc: "sidebar text (defaults to the uppercased title)"},
						{Name: "nav-order", Type: "int", Default: "70", Doc: "sidebar position; built-in views use 10 through 60"},
					},
					Examples: []string{"pleiades forge new-view access-reviews --title \"Access Reviews\" --summary \"Who approved what, and when.\""},
				},
				{
					Name:       "new-external",
					Positional: "<namespace.method>",
					Synopsis:   "generate a buildable external Collection program providing one method",
					Flags: []Flag{
						{Name: "dir", Type: "string", Default: "", Doc: "directory to write the program into (default: the method name with its dots as hyphens)"},
						{Name: "no-go-mod", Type: "bool", Default: "false", Doc: "write no go.mod, so the program joins the Go module around it instead of being a module of its own"},
						{Name: "capability", Type: "string", Default: "", Doc: "a capability the program defines as data, <namespace>.<name> in the method's namespace: its definition and a probe that grants it are written, and the method requires it"},
					},
					Examples: []string{"pleiades forge new-external acme.motd.read", "pleiades forge new-external acme.motd.read --capability acme.motd_host"},
				},
				{
					Name:       "new-filter",
					Positional: "<GoName>",
					Synopsis:   "generate a new pkg/filters function and its starter test",
					Flags: []Flag{
						{Name: "dir", Type: "string", Default: ".", Doc: "repository directory to write the generated files into"},
						{Name: "cel-name", Type: "string", Default: "", Doc: "the bare name after \"filters.\" in a runbook condition, e.g. cidrToNetmask"},
						{Name: "category", Type: "string", Default: "", Doc: "the filter category this belongs to (network, structured data, string/encoding/path, and so on), e.g. network"},
						{Name: "summary", Type: "string", Default: "", Doc: "one sentence describing what this filter does"},
						{Name: "param", Type: "string", Default: "", Doc: "one argument: name:goType, or name:goType:celType for a type filterscaffold does not know; repeatable"},
						{Name: "return", Type: "string", Default: "", Doc: "this filter's result: goType, or goType:celType for a type filterscaffold does not know"},
						{Name: "skip-existing", Type: "bool", Default: "false", Doc: "leave an already-generated entry alone instead of refusing, for regenerating a catalog in place"},
					},
					Examples: []string{
						"pleiades forge new-filter CIDRToNetmask --cel-name cidrToNetmask --category network --summary \"converts a CIDR prefix length to its dotted-decimal netmask.\" --param cidr:string --return string",
					},
				},
				{
					Name:       "migrate-playbook",
					Positional: "<playbook.yml>",
					Synopsis:   "convert an Ansible playbook into native runbooks, with a report of everything a person must finish; exits 3 when anything needs one",
					Flags: []Flag{
						{Name: "out", Type: "string", Default: "runbooks", Doc: "directory to write the converted runbooks into, created when missing"},
						{Name: "json", Type: "bool", Default: "false", Doc: "print the migration report as JSON, the same model the text view renders"},
						{Name: "force", Type: "bool", Default: "false", Doc: "replace runbooks an earlier conversion wrote"},
					},
					Examples: []string{
						"pleiades forge migrate-playbook site.yml",
						"pleiades forge migrate-playbook playbooks/web.yml --out runbooks --json",
					},
				},
			},
		},
		{
			Name:       "doc",
			Positional: "[fqcn | device type]",
			Synopsis:   "look up a Collection method's or a device type's reference from the live registry (see 'pleiades doc --help')",
			Flags: []Flag{
				{Name: "list", Type: "bool", Default: "false", Doc: "list every registered FQCN, or those in one namespace if fqcn is given as a prefix"},
				{Name: "snippet", Type: "bool", Default: "false", Doc: "print a paste-ready runbook task stanza for fqcn"},
				{Name: "json", Type: "bool", Default: "false", Doc: "print the full catalog, or one fqcn's Manifest, as JSON"},
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory whose external Collections are read, as run --dir"},
				{Name: "create-skill", Type: "bool", Default: "false", Doc: "create SKILL.md in the current directory, a skill an AI agent reads to learn The Pleiades: its commands, how a runbook is written, and every built-in method"},
			},
			Examples: []string{
				"pleiades doc --create-skill",
				"pleiades doc --list",
				"pleiades doc --list net.catalyst",
				"pleiades doc net.catalyst.device_facts",
				"pleiades doc --snippet net.catalyst.device_facts",
				"pleiades doc --json net.catalyst.device_facts",
				"pleiades doc cisco_router",
			},
		},
		{
			Name:     "version",
			Synopsis: "print the pleiades version",
			Examples: []string{"pleiades version"},
		},
	},
}

// Find returns spec's own direct child named name, so a print*Usage
// function (or the reference generator) can locate the Command matching
// the dispatch table it renders for, without hand-copying that subtree a
// second time.
func Find(spec Command, name string) (Command, bool) {
	for _, c := range spec.Subcommands {
		if c.Name == name {
			return c, true
		}
	}
	return Command{}, false
}

// RenderList renders subs as the "commands:" table every print*Usage
// function shows: a two-space indent, names padded to the longest one in
// this list, and its Synopsis after two more spaces.
func RenderList(subs []Command) string {
	width := 0
	for _, c := range subs {
		if len(c.Name) > width {
			width = len(c.Name)
		}
	}
	var b strings.Builder
	for _, c := range subs {
		fmt.Fprintf(&b, "  %-*s  %s\n", width, c.Name, c.Synopsis)
	}
	return b.String()
}
