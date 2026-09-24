---
status: beta
---

# CLI reference

Every pleiades subcommand, generated from the same command tree `pleiades <command> --help` renders from (`internal/clispec`). Flags are listed in the order each command's own `flag.FlagSet` declares them; running `--help` against a real binary shows the identical set.

## pleiades init

scaffold a new project in the current directory

`pleiades init [flags]`

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| --dir | `string` | `.` | project directory to scaffold |

`pleiades init`

## pleiades add-host

add a host to the static inventory

`pleiades add-host <name> [flags]`

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| --dir | `string` | `.` | project directory |
| --type | `string` | - | device type (e.g. linux_server, cisco_router) |
| --classify | `string` | - | comma-separated classification path (e.g. linux_server,debian_family,ubuntu), an alternative to --type |
| --tags | `string` | - | comma-separated tags |
| --set | `key=value` | - | device property as key=value (repeatable) |

`pleiades add-host web01 --type linux_server --tags prod,web`

## pleiades add-credential

store an encrypted credential for a device

`pleiades add-credential <device> [flags]`

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| --dir | `string` | `.` | project directory |
| --username | `string` | - | account name to authenticate as |
| --password | `string` | - | password to authenticate with (prompted interactively if --key is also absent and this is empty) |
| --key | `string` | - | path to a PEM private key file to authenticate with |
| --certificate | `string` | - | path to a PEM client certificate to present, which requires --key |
| --pfx | `string` | - | path to a PKCS#12 (.pfx/.p12) bundle holding a certificate and its key |
| --passphrase | `bool` | `false` | prompt for the private key's or the bundle's passphrase |
| --passphrase-stdin | `bool` | `false` | read the private key's or the bundle's passphrase as one line on standard input |

`pleiades add-credential web01 --username admin`

`pleiades add-credential win01 --certificate client.pem --key client.key`

## pleiades validate

check a runbook against the inventory

`pleiades validate [runbook.yaml] [flags]`

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| --dir | `string` | `.` | project directory |
| --tags | `string` | - | run only the tasks carrying one of these tags (comma-separated, repeatable); all, tagged, untagged, always and never keep Ansible's meanings, and a task tagged never runs only when named |
| --skip-tags | `string` | - | leave out the tasks carrying one of these tags, even ones --tags selects (comma-separated, repeatable) |

`pleiades validate runbooks/site.yaml`

`pleiades validate runbooks/site.yaml --tags web`

## pleiades run

build, validate, and run a runbook

`pleiades run <runbook.yaml> [flags]`

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| --dir | `string` | `.` | project directory |
| --mode | `string` | `execute` | execute applies changes; check reports what each task would change and changes nothing |
| --verbose | `bool` | `false` | print each task's own output (stdout, exit status, diffs), not just whether it changed |
| --v | `bool` | `false` | shorthand for --verbose |
| --allow-unchecked | `string` | - | a method whose tasks may go unchecked without making the check incomplete (repeatable); the tasks are still listed |
| --tags | `string` | - | run only the tasks carrying one of these tags (comma-separated, repeatable); all, tagged, untagged, always and never keep Ansible's meanings, and a task tagged never runs only when named |
| --skip-tags | `string` | - | leave out the tasks carrying one of these tags, even ones --tags selects (comma-separated, repeatable) |

`pleiades run runbooks/site.yaml`

`pleiades run runbooks/site.yaml --verbose`

`pleiades run runbooks/site.yaml --mode check --verbose`

`pleiades run runbooks/site.yaml --mode check --allow-unchecked exec.command`

`pleiades run runbooks/site.yaml --tags web --skip-tags slow`

## pleiades inventory

sync devices from an external source (see 'pleiades inventory --help')

`pleiades inventory [flags]`

### pleiades inventory sync

pull devices from an external source into the local inventory

`pleiades inventory sync [flags]`

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| --dir | `string` | `.` | project directory holding inventory.yaml |
| --plugin | `string` | - | sync plugin to run (see 'pleiades inventory plugins') |
| --endpoint | `string` | - | upstream base URL, overriding the plugin's default |
| --credential | `string` | - | credential store entry to authenticate with, defaulting to the plugin name |
| --page-size | `int` | `0` | how many records to request per upstream page |
| --read-only | `bool` | `false` | refuse every write to the local inventory, reporting what would have changed |
| --insecure-skip-verify | `bool` | `false` | skip TLS certificate verification against the upstream system |
| --set | `string` | - | a plugin-specific setting as key=value, repeatable (see 'pleiades inventory plugins') |

`pleiades inventory sync --plugin catalyst_center --endpoint https://dnac.example.com`

`pleiades inventory sync --plugin aws --set region=us-east-1`

### pleiades inventory plugins

list the available inventory sync plugins

`pleiades inventory plugins [flags]`

`pleiades inventory plugins`

## pleiades collection

approve, revoke and list the external Collection builds allowed to run (see 'pleiades collection --help')

`pleiades collection [flags]`

### pleiades collection approve

approve a build of a program in PLEIADES_COLLECTIONS_DIR, after showing what it says it provides

`pleiades collection approve <program> [flags]`

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| --digest | `string` | - | approve this build (sha256:<hex>) without inspecting the program, for an image build or a rolling upgrade |
| --yes | `bool` | `false` | approve without asking, after still showing what the program provides |

`pleiades collection approve note`

`pleiades collection approve note --digest sha256:<64 hex digits>`

### pleiades collection revoke

withdraw a program's approvals, so it stops running from its next call

`pleiades collection revoke <program> [flags]`

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| --digest | `string` | - | withdraw only this build's approval (default: every build of the program) |

`pleiades collection revoke note`

### pleiades collection list

list every approved build, who approved it and when

`pleiades collection list [flags]`

`pleiades collection list`

## pleiades import

read another platform's export and report what this one would do with it (see 'pleiades import --help')

`pleiades import [flags]`

### pleiades import awx-credential-types

check an AWX credential type export against this platform, before a migration

`pleiades import awx-credential-types <export.json> [flags]`

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| --out | `string` | - | directory to write each importable type into as JSON, ready to POST to /credential-types |
| --quiet | `bool` | `false` | report only the types this platform would not import |

`pleiades import awx-credential-types credential_types.json`

`pleiades import awx-credential-types --out ./types credential_types.json`

## pleiades forge

authoring and migration tooling (see 'pleiades forge --help')

`pleiades forge [flags]`

### pleiades forge new-device

generate a new vendor device-type package

`pleiades forge new-device <vendor> [flags]`

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| --dir | `string` | `.` | repository directory to write the generated package into |
| --type | `string` | - | the full record.RegisterType key (e.g. cisco_router) |
| --capabilities | `string` | - | comma-separated vendor baseline capability names (e.g. AptCapable,SSHTransportCapable) |
| --skip-existing | `bool` | `false` | leave an already-generated entry alone instead of refusing, for regenerating a catalog in place |

### pleiades forge new-collection

generate a new namespaced Collection method package

`pleiades forge new-collection <namespace.method> [flags]`

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| --dir | `string` | `.` | repository directory to write the generated package into |
| --capabilities | `string` | - | comma-separated required capability names (e.g. AptCapable) |
| --transports | `string` | - | comma-separated supported transport names (e.g. ssh) |
| --requires-elevation | `bool` | `false` | whether this method needs elevated privileges on the target device |
| --engine-version | `string` | - | minimum core engine version constraint (unparsed, e.g. >=1.0.0) |
| --doc-json | `string` | - | reference documentation as a JSON pkg/collection.Doc object, or @path to read it from a file |
| --skip-existing | `bool` | `false` | leave an already-generated entry alone instead of refusing, for regenerating a catalog in place |

`pleiades forge new-collection net.junos.config --capabilities SSHTransportCapable --transports ssh`

`pleiades forge new-collection net.junos.config --doc-json @junos-config-doc.json`

### pleiades forge new-plugin

generate a new inventory sync plugin package

`pleiades forge new-plugin <name> [flags]`

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| --dir | `string` | `.` | repository directory to write the generated package into |
| --description | `string` | - | one-line help text describing the upstream system this plugin reads |
| --endpoint | `string` | - | default upstream base URL (e.g. https://sandboxdnac.cisco.com) |
| --read-only | `bool` | `false` | declare the upstream authoritative and never written back |
| --requires-credentials | `bool` | `false` | declare that Connect resolves a credential, so the plugin is built with the project credential store |
| --settings-json | `string` | - | per-deployment settings as a JSON list of syncplugin.SettingSpec objects, or @path to read it from a file |
| --skip-existing | `bool` | `false` | leave an already-generated entry alone instead of refusing, for regenerating a catalog in place |

`pleiades forge new-plugin netbox --description "reads devices from a NetBox instance" --requires-credentials`

`pleiades forge new-plugin gcp --description "reads Compute Engine instances" --requires-credentials --settings-json '[{"name":"project","description":"the GCP project to read from","required":true}]'`

### pleiades forge new-view

generate a new web UI view resource package

`pleiades forge new-view <name> [flags]`

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| --dir | `string` | `.` | repository directory to write the generated package into |
| --title | `string` | - | page heading and document title for this view |
| --summary | `string` | - | one line rendered under the heading |
| --nav-label | `string` | - | sidebar text (defaults to the uppercased title) |
| --nav-order | `int` | `70` | sidebar position; built-in views use 10 through 60 |

`pleiades forge new-view access-reviews --title "Access Reviews" --summary "Who approved what, and when."`

### pleiades forge new-external

generate a buildable external Collection program providing one method

`pleiades forge new-external <namespace.method> [flags]`

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| --dir | `string` | - | directory to write the program into (default: the method name with its dots as hyphens) |
| --no-go-mod | `bool` | `false` | write no go.mod, so the program joins the Go module around it instead of being a module of its own |

`pleiades forge new-external acme.motd.read`

### pleiades forge new-filter

generate a new pkg/filters function and its starter test

`pleiades forge new-filter <GoName> [flags]`

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| --dir | `string` | `.` | repository directory to write the generated files into |
| --cel-name | `string` | - | the bare name after "filters." in a runbook condition, e.g. cidrToNetmask |
| --category | `string` | - | the filter category this belongs to (network, structured data, string/encoding/path, and so on), e.g. network |
| --summary | `string` | - | one sentence describing what this filter does |
| --param | `string` | - | one argument: name:goType, or name:goType:celType for a type filterscaffold does not know; repeatable |
| --return | `string` | - | this filter's result: goType, or goType:celType for a type filterscaffold does not know |
| --skip-existing | `bool` | `false` | leave an already-generated entry alone instead of refusing, for regenerating a catalog in place |

`pleiades forge new-filter CIDRToNetmask --cel-name cidrToNetmask --category network --summary "converts a CIDR prefix length to its dotted-decimal netmask." --param cidr:string --return string`

### pleiades forge migrate-playbook

convert an Ansible playbook into native runbooks, with a report of everything a person must finish; exits 3 when anything needs one

`pleiades forge migrate-playbook <playbook.yml> [flags]`

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| --out | `string` | `runbooks` | directory to write the converted runbooks into, created when missing |
| --json | `bool` | `false` | print the migration report as JSON, the same model the text view renders |
| --force | `bool` | `false` | replace runbooks an earlier conversion wrote |

`pleiades forge migrate-playbook site.yml`

`pleiades forge migrate-playbook playbooks/web.yml --out runbooks --json`

## pleiades doc

look up a Collection method's reference from the live registry (see 'pleiades doc --help')

`pleiades doc [fqcn] [flags]`

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| --list | `bool` | `false` | list every registered FQCN, or those in one namespace if fqcn is given as a prefix |
| --snippet | `bool` | `false` | print a paste-ready runbook task stanza for fqcn |
| --json | `bool` | `false` | print the full catalog, or one fqcn's Manifest, as JSON |

`pleiades doc --list`

`pleiades doc --list net.catalyst`

`pleiades doc net.catalyst.device_facts`

`pleiades doc --snippet net.catalyst.device_facts`

`pleiades doc --json net.catalyst.device_facts`

## pleiades version

print the pleiades version

`pleiades version [flags]`

`pleiades version`

