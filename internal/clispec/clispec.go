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
	Synopsis: "Walk tier: no server, no database, no broker.",
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
			Synopsis:   "store an encrypted SSH credential for a device",
			Flags: []Flag{
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
				{Name: "username", Type: "string", Default: "", Doc: "account name to authenticate as"},
				{Name: "password", Type: "string", Default: "", Doc: "password to authenticate with (prompted interactively if --key is also absent and this is empty)"},
				{Name: "key", Type: "string", Default: "", Doc: "path to a PEM private key file to authenticate with"},
				{Name: "passphrase", Type: "bool", Default: "false", Doc: "prompt for the private key's passphrase (only meaningful with --key)"},
			},
			Examples: []string{"pleiades add-credential web01 --username admin"},
		},
		{
			Name:       "validate",
			Positional: "[runbook.yaml]",
			Synopsis:   "check a runbook against the inventory",
			Flags: []Flag{
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
			},
			Examples: []string{"pleiades validate runbooks/site.yaml"},
		},
		{
			Name:       "run",
			Positional: "<runbook.yaml>",
			Synopsis:   "build, validate, and run a runbook",
			Flags: []Flag{
				{Name: "dir", Type: "string", Default: ".", Doc: "project directory"},
			},
			Examples: []string{"pleiades run runbooks/site.yaml"},
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
					},
					Examples: []string{"pleiades inventory sync --plugin catalystcenter --endpoint https://dnac.example.com"},
				},
				{
					Name:     "plugins",
					Synopsis: "list the available inventory sync plugins",
					Examples: []string{"pleiades inventory plugins"},
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
					},
					Examples: []string{"pleiades forge new-collection net.junos.config --capabilities SSHTransportCapable --transports ssh"},
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
