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
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory to scaffold"},
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
				{Name: "tags", Type: "string", Default: "", Doc: "comma-separated tags"},
				{Name: "set", Type: "key=value", Default: "", Doc: "device property as key=value (repeatable)"},
			},
			Examples: []string{"pleiades add-host web01 --type linux_server --tags prod,web"},
		},
		{
			Name:       "add-credential",
			Positional: "<device>",
			Synopsis:   "store an encrypted credential for a device",
			Flags: []Flag{
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
				{Name: "username", Type: "string", Default: "", Doc: "account name to authenticate as"},
				{Name: "password", Type: "string", Default: "", Doc: "password to authenticate with (prompted interactively if --key is also absent and this is empty)"},
				{Name: "key", Type: "string", Default: "", Doc: "path to a PEM private key file to authenticate with"},
				{Name: "certificate", Type: "string", Default: "", Doc: "path to a PEM client certificate to present, which requires --key"},
				{Name: "pfx", Type: "string", Default: "", Doc: "path to a PKCS#12 (.pfx/.p12) bundle holding a certificate and its key"},
				{Name: "passphrase", Type: "bool", Default: "false", Doc: "prompt for the private key's or the bundle's passphrase"},
				{Name: "passphrase-stdin", Type: "bool", Default: "false", Doc: "read the private key's or the bundle's passphrase as one line on standard input"},
			},
			Examples: []string{
				"pleiades add-credential web01 --username admin",
				"pleiades add-credential win01 --certificate client.pem --key client.key",
			},
		},
		{
			Name:       "validate",
			Positional: "[runbook.yaml]",
			Synopsis:   "check a runbook against the inventory",
			Flags: []Flag{
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
				{Name: "tags", Type: "string", Default: "", Doc: "run only the tasks carrying one of these tags (comma-separated, repeatable); all, tagged, untagged, always and never keep Ansible's meanings, and a task tagged never runs only when named"},
				{Name: "skip-tags", Type: "string", Default: "", Doc: "leave out the tasks carrying one of these tags, even ones --tags selects (comma-separated, repeatable)"},
			},
			Examples: []string{"pleiades validate runbooks/site.yaml", "pleiades validate runbooks/site.yaml --tags web"},
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
				{Name: "allow-unchecked", Type: "string", Default: "", Doc: "a method whose tasks may go unchecked without making the check incomplete (repeatable); the tasks are still listed"},
				{Name: "tags", Type: "string", Default: "", Doc: "run only the tasks carrying one of these tags (comma-separated, repeatable); all, tagged, untagged, always and never keep Ansible's meanings, and a task tagged never runs only when named"},
				{Name: "skip-tags", Type: "string", Default: "", Doc: "leave out the tasks carrying one of these tags, even ones --tags selects (comma-separated, repeatable)"},
				{Name: "persist-connections", Type: "bool", Default: "true", Doc: "keep one SSH connection per device open between its tasks; --persist-connections=false logs in afresh for every task, and a device, group or inventory setting persist_connections: false turns it off for its devices whatever this says"},
			},
			Examples: []string{
				"pleiades run runbooks/site.yaml",
				"pleiades run runbooks/site.yaml --verbose",
				"pleiades run runbooks/site.yaml --mode check --verbose",
				"pleiades run runbooks/site.yaml --mode check --allow-unchecked exec.command",
				"pleiades run runbooks/site.yaml --tags web --skip-tags slow",
				"pleiades run runbooks/site.yaml --persist-connections=false",
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
					},
					Examples: []string{"pleiades forge new-external acme.motd.read"},
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
			Positional: "[fqcn]",
			Synopsis:   "look up a Collection method's reference from the live registry (see 'pleiades doc --help')",
			Flags: []Flag{
				{Name: "list", Type: "bool", Default: "false", Doc: "list every registered FQCN, or those in one namespace if fqcn is given as a prefix"},
				{Name: "snippet", Type: "bool", Default: "false", Doc: "print a paste-ready runbook task stanza for fqcn"},
				{Name: "json", Type: "bool", Default: "false", Doc: "print the full catalog, or one fqcn's Manifest, as JSON"},
			},
			Examples: []string{
				"pleiades doc --list",
				"pleiades doc --list net.catalyst",
				"pleiades doc net.catalyst.device_facts",
				"pleiades doc --snippet net.catalyst.device_facts",
				"pleiades doc --json net.catalyst.device_facts",
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
