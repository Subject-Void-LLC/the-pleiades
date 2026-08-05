# The Forge of Hephaestus (Design Note)

**Status: partly real.** Writing a runbook and linting one already work today. The Collection registry
(`pkg/collection`), the capability vocabulary and its hierarchy (`pkg/capability`), and generators for
both the device type pattern and the Collection method pattern are built (Phases 30 through 33).
Playbook migration, collection migration, the actual module catalog, and the IDE plugin are not built.
This document specifies all of it, and `.SPECIFICATION/IMPLEMENTATION.md` Part VII (Phases 30 through
38) builds it.

## What this is

The Forge of Hephaestus is the name for everything a user does *before* a runbook runs. Authoring,
converting, checking, and extending. It is a brand and a command namespace, not a new runtime
subsystem. Every workflow under it has its own real Go package, and none of them touch the execution
engine.

The name exists because these seven jobs were scattered across the specification with no shared
identity, and two of them were being confused with unrelated features. Giving them one name makes the
boundaries statable.

## The seven workflows

| # | Workflow | Status today | Command |
|---|----------|--------------|---------|
| 1 | Write a native runbook | Works. `WorkflowDef`, `Task`, `Conditional` in `internal/engine` | `pleiades init` |
| 2 | Migrate an Ansible playbook file | Not built. Only a rejection message exists | `pleiades forge migrate-playbook` |
| 3 | Lint a runbook | Works. Rule registry in `internal/validate` | `pleiades validate` |
| 4 | Use an IDE plugin | Not built. No language server code anywhere | `pleiades-lsp` |
| 5 | Create a namespaced Collection | Registry, manifest, and generator all built (Phases 31, 33); no real Collection registered yet | `pleiades forge new-collection` |
| 6 | Create an inventory device type | Pattern and generator both built (Phase 33) | `pleiades forge new-device` |
| 7 | Migrate an Ansible Galaxy collection | Not built | `pleiades forge migrate-collection` |

## What the Forge is not

Two other things in this project sound like "Ansible migration." They are different jobs with
different inputs and different outputs. Confusing them has already cost review time, so the boundary
is written down here.

| Concept | Input | Output | Specified in |
|---------|-------|--------|--------------|
| **Legacy interoperability** | An unconverted playbook, marked `type: ansible` | A running job, executed as Ansible inside a container | `PLAN.md` Section 23 |
| **The Forge** | Ansible source files on disk | Native Pleiades runbooks and Collections | This document |

Run Ansible as Ansible is Section 23. The playbook's own bytes never change and it never becomes a
runbook. Convert Ansible into native format is the Forge. The two can be used together during a
migration, but they answer different questions.

### On AWX

An AWX server importer was previously specified as `pleiades migrate awx`. It has been removed. See
`AWX_PARITY.md` Section 4.11 for the full reasoning. The short version:

- **Inventory** comes from a sync plugin (`PLAN.md` Section 6a) pointed at the real upstream source,
  such as NetBox, Nautobot, AWS, or VMware. An AWX inventory was always a copy of something else.
  Copying the copy is the wrong migration.
- **Playbooks, roles, and collections** come through the Forge.
- **Secrets** are re-entered by a human who is authorized to hold them. No tool migrates them.

Nothing was left for a server importer to do.

## The bootstrap principle

**The Forge is built first. Then the Forge builds everything else.**

This project commits to covering thirty six foundational Ansible modules, which is roughly eighty
percent of what real playbooks use. The obvious way to deliver that is to hand write thirty six Go
packages. That is the wrong way, and avoiding it is the main reason to build the Forge before the
catalog.

The order is:

1. Build the tooling. The Collection registry and manifest (the target format), the `new-collection`
   and `new-device` generators, and the playbook translator.
2. Then use `forge new-collection` and `forge new-device` to generate the catalog and the device
   types it needs.

The catalog is output of the tooling, not input to it. Three consequences follow:

- **The module mapping table is a work list, not a lookup table.** It is the translator's target, the
  source of the capability vocabulary, and the literal queue of generator invocations.
- **The translator can emit a name before the collection behind it exists.** Mapping
  `ansible.builtin.apt` to `pkg.apt.install` is a naming commitment. The package implementing it is
  built afterward. Neither side blocks the other. They meet at the table.
- **The catalog is the Forge's own test.** If generating the catalog with the scaffold is painful,
  the scaffold is wrong, and we find that out on our own code before an outside author does. That is
  a better release gate than any unit test.

## The native catalog and its naming rule

### The rule

A native name is `<namespace>.<method>`. The namespace is the domain, plus an implementation segment
when a domain has more than one implementation. The method is the action.

```
pkg.apt.install        namespace "pkg.apt",   method "install"
svc.systemd.restart    namespace "svc.systemd", method "restart"
net.ios.config         namespace "net.ios",   method "config"
exec.command           namespace "exec",      method "command"
```

Names are never bare. `PLAN.md` Section 2 forbids it, and `pkg/collection` enforces it at
registration time.

The name states what capability it needs. `pkg.apt.install` obviously requires `AptCapable`. This is
what makes a linter error readable, and it puts the capability hierarchy in the runbook text itself.

### Why the generic name and the specific name both exist

The naming rule carries the capability hierarchy from `PLAN.md` Section 8 directly in the name:

```
pkg.install          requires PackageManagerCapable   (resolves down at plan time)
  pkg.apt.install    requires AptCapable              (explicit)
  pkg.dnf.install    requires DnfCapable              (explicit)

svc.restart          requires ServiceManagerCapable   (resolves down at plan time)
  svc.systemd.restart requires SystemdCapable         (explicit)
  svc.windows.restart requires WindowsServiceCapable  (explicit)

net.cli.config       requires NetworkCLICapable       (resolves down at plan time)
  net.ios.config     requires CiscoIOSCapable         (explicit)
  net.junos.config   requires JunosCapable            (explicit)
  net.eos.config     requires AristaEOSCapable        (explicit)
```

An author writes the broadest name that works. A task calling `pkg.install` against a device
declaring `AptCapable` resolves to the apt implementation at plan time. If nothing matches, planning
fails with an error naming both the task and the device, before anything runs.

Two facts about the ground this stood on before Phase 32, kept here because both were easy to get
wrong and the history is worth keeping:

- **`capability.Descriptor` already had a `Parent` field, and nothing read it.** Phase 32 made
  `capability.Resolves` walk it (a device that only declares the narrower `AptCapable` also resolves
  the broader `PackageManagerCapable` it descends from), and retrofitted `Parent: NetworkCLICapable`
  onto the pre-existing `CiscoIOSCapable` so this section's own `net.cli.config -> net.ios.config`
  worked example is real, not aspirational: `cisco.Router` also structurally implements
  `NetworkCLICapable`'s `CLIPrompt()`, so the resolution holds on both the data and structural sides.
- **Exactly three capabilities existed in code before Phase 32:** `SSHTransportCapable`,
  `CiscoIOSCapable`, and `LinuxCapable`. Phase 32 added the other ~23 named throughout this document
  (`PackageManagerCapable`/`AptCapable`/`DnfCapable`, `ServiceManagerCapable`/`SystemdCapable`/
  `FirewalldCapable`/`WindowsServiceCapable`, `NetworkCLICapable`/`NetconfCapable`/`JunosCapable`/
  `AristaEOSCapable`, and the rest of the catalog table below) as real Go interfaces in
  `pkg/capability`, split across several files by domain. None has a concrete device type
  implementing it yet beyond the three originals plus `NetworkCLICapable` (via `Router`) -- that is
  Phase 33/34's job, not this one's.

### A naming drift that Phase 32 settled

The same capability used to be spelled three different ways across this project:

| Source | Spelling |
|--------|----------|
| `pkg/capability/capabilities.go` (the code) | `SSHTransportCapable` |
| `PLAN.md` Section 14 transports table | `SSHCapable` |
| `CODE_SCAFFOLD.md` | `SSHCapable`, with a different method set |

**The code name won.** `SSHTransportCapable` is what exists and what `capability.Implements` checks.
Phase 32 reconciled both specification documents to match the code (spelling and method set alike)
rather than the other way around. Any table that emits capability names must use code spellings, or
the linter will reject names the specification told an author to use.

### Why renaming lowers friction rather than raising it

This project's stated goal is the lowest possible barrier for someone who already knows Ansible, so
renaming every module looks like the opposite of that. It is not, and the reason matters enough to
write down, because the obvious alternative (keep Ansible's names exactly) is a trap.

**A shared name is a promise of shared behavior.** If this platform shipped a module called
`ansible.builtin.apt`, an author would reasonably expect `deb`, `default_release`, `dpkg_options`,
`allow_downgrade`, and `fail_on_autoremove` to work, because they work in the module with that name.
A Go reimplementation will not support all of them on day one and realistically never supports all of
them. So the author reads real Ansible documentation, writes a parameter this platform ignores, and
the familiar name has cost them more than an unfamiliar one would have. `pkg.apt.install` promises
nothing it does not deliver, and it sends nobody to the wrong documentation.

**The ecosystem is not inherited either way.** Copying Ansible's names does not bring Ansible's
answers, tutorials, and forum posts with them. It brings the appearance of bringing them, which is
worse than plainly not having them.

**Ansible's own names are not a high bar.** `builtin` is a Python packaging detail showing through
into user-facing vocabulary. More importantly, `ansible.builtin.file` hides six unrelated operations
behind one name and a `state` parameter (`absent`, `directory`, `file`, `hard`, `link`, `touch`).
Remembering which `state` values each module accepts is itself a large part of the friction of
writing Ansible. Splitting them into `file.directory`, `file.symlink`, and `file.remove` is less to
learn per operation, not more.

Two further reasons hold, and both depend on the Forge existing.

1. **Nobody types these names during a migration.** `forge migrate-playbook` does the rename
   mechanically. Existing playbooks convert without the author ever learning `pkg.apt.install`. The
   cost is paid once, in a table, by the translator.
2. **Nobody memorizes them when authoring either.** The IDE plugin reads the Collection registry, so
   the catalog is discoverable instead of memorized. This is the "type safety moves left" promise
   applied to naming: names you are shown, not names you must recall.

So the *concepts* stay Ansible's. `when`, `block`, `rescue`, `always`, `pre_tasks`, `tasks`,
`post_tasks` are unchanged. Only the module *names* change, and tooling absorbs that change.

### Four of the thirty six are not collections at all

These interact with the orchestrator's own state and control flow. They are engine features wearing
a module's name, and scaffolding them as collections would be a mistake.

| Ansible module | What it becomes | Why |
|----------------|-----------------|-----|
| `ansible.builtin.set_fact` | An engine keyword | It writes to the run context. `WorkflowContext` and `sdk.RunbookContext` already own that. |
| `ansible.builtin.debug` | An engine keyword | It writes to the execution journal. See `docs/rollback_journal_design.md`. |
| `ansible.builtin.import_tasks` | An engine keyword, resolved at parse time | Static inclusion is compatible with building the DAG up front. |
| `ansible.builtin.include_tasks` | Unresolved. See the honest limits below | Runtime inclusion fights plan-time validation. |

### The catalog

Roughly twenty seven collections cover the thirty six modules. Method counts are approximate until
Phase 34 generates them.

**Execution.** `exec` requires `CommandExecCapable` or `ShellExecCapable`.

| Ansible | Native | Capability |
|---------|--------|------------|
| `ansible.builtin.command` | `exec.command` | `CommandExecCapable` |
| `ansible.builtin.shell` | `exec.shell` | `ShellExecCapable` |

**Packages.** All target side, over SSH.

| Ansible | Native | Capability |
|---------|--------|------------|
| `ansible.builtin.package` | `pkg.install`, `pkg.remove`, `pkg.upgrade` | `PackageManagerCapable` |
| `ansible.builtin.apt` | `pkg.apt.install`, `pkg.apt.remove`, `pkg.apt.upgrade` | `AptCapable` |
| `ansible.builtin.dnf` | `pkg.dnf.install`, `pkg.dnf.remove`, `pkg.dnf.upgrade` | `DnfCapable` |

**Services.**

| Ansible | Native | Capability |
|---------|--------|------------|
| `ansible.builtin.service` | `svc.start`, `svc.stop`, `svc.restart`, `svc.enable`, `svc.disable` | `ServiceManagerCapable` |
| `ansible.builtin.systemd` | `svc.systemd.*` plus `svc.systemd.daemon_reload` | `SystemdCapable` |
| `ansible.windows.win_service` | `svc.windows.*` | `WindowsServiceCapable` |

**Identity.**

| Ansible | Native | Capability |
|---------|--------|------------|
| `ansible.builtin.user` | `identity.user.create`, `.remove`, `.modify` | `PosixAccountCapable` |
| `ansible.builtin.group` | `identity.group.create`, `.remove`, `.modify` | `PosixAccountCapable` |

**Files and configuration.** This is where verb per action pays off most. Ansible's `file` module
takes a `state` parameter covering five unrelated operations. Here they are five methods.

| Ansible | Native | Capability |
|---------|--------|------------|
| `ansible.builtin.copy` | `file.copy` | `POSIXFileSystemCapable` |
| `ansible.builtin.template` | `file.template` | `POSIXFileSystemCapable` |
| `ansible.builtin.file` | `file.directory`, `file.symlink`, `file.remove`, `file.touch`, `file.permissions` | `POSIXFileSystemCapable` |
| `ansible.builtin.lineinfile` | `file.line.set`, `file.line.remove` | `POSIXFileSystemCapable` |
| `ansible.builtin.blockinfile` | `file.block.set`, `file.block.remove` | `POSIXFileSystemCapable` |

**Network devices.**

| Ansible | Native | Capability |
|---------|--------|------------|
| `ansible.netcommon.cli_command` | `net.cli.command` | `NetworkCLICapable` |
| `ansible.netcommon.cli_config` | `net.cli.config` | `NetworkCLICapable` |
| `ansible.netcommon.netconf_config` | `net.netconf.config` | `NetconfCapable` |
| `cisco.ios.ios_config` | `net.ios.config` | `CiscoIOSCapable` |
| `junipernetworks.junos.junos_config` | `net.junos.config` | `JunosCapable` |
| `arista.eos.eos_config` | `net.eos.config` | `AristaEOSCapable` |

**Extended infrastructure.**

| Ansible | Native | Capability | Context |
|---------|--------|------------|---------|
| `ansible.posix.firewalld` | `fw.firewalld.allow`, `.deny`, `.reload` | `FirewalldCapable` | target side |
| `ansible.posix.mount` | `fs.mount`, `fs.unmount` | `LinuxCapable` | target side |
| `ansible.windows.win_feature` | `win.feature.install`, `.remove` | `WindowsFeatureCapable` | target side |
| `community.general.archive` | `archive.create` | `POSIXFileSystemCapable` | target side |
| `community.general.unarchive` | `archive.extract` | `FileTransferCapable` | hybrid |
| `community.docker.docker_container` | `container.docker.run`, `.stop`, `.remove` | `DockerCapable` | hybrid |
| `amazon.aws.ec2_instance` | `cloud.aws.ec2.create`, `.terminate` | `AWSAPICapable` | controller side |
| `amazon.aws.s3_bucket` | `cloud.aws.s3.create_bucket`, `.delete_bucket` | `AWSAPICapable` | controller side |

The two cloud entries are the clearest controller side cases in the catalog. They run against an API,
not against an inventory device over SSH. Ansible needs `delegate_to: localhost` or
`connection: local` to express this. Here execution context is a first class field on the manifest,
so no hack is needed. This is `PLAN.md`'s execution context table earning its keep.

**Gating and facts.**

| Ansible | Native | Capability | Context |
|---------|--------|------------|---------|
| `ansible.builtin.uri` | `http.request` | none | controller side |
| `ansible.builtin.wait_for` | `wait.port`, `wait.path`, `wait.search` | `NetworkAddressableCapable` | hybrid |
| `ansible.builtin.setup` | `facts.gather` | `FactGathererCapable` | target side |

`facts.gather` connects to `PLAN.md` Section 22's `FactGatherer` capability and the
`device.facts_gathered` event. It is not a new mechanism.

### Declared is not implemented

Phase 34 generates all of the above as registered manifests with working stubs. Implementations come
later, module by module. That is a deliberate choice, and it is exactly the situation RULE 0 exists
to police. Three guardrails make it safe:

- **A stub returns an explicit `not implemented` error. It never returns success.** A collection
  named `pkg.apt.install` that silently does nothing is worse than one that does not exist, because
  it makes a failed runbook look like a successful one.
- **The manifest carries a status field**, `declared` or `implemented`, so the gap is data the
  tooling reads rather than knowledge people carry.
- **A validation rule flags any runbook calling a declared but unimplemented name.** The gap becomes
  a write time and pre commit error instead of a runtime surprise. An incomplete catalog stays safe
  because the linter tells the truth about what is real.

## State is a migration concern, not a parameter

Ansible's `state:` is desired-state semantics hiding inside a parameter. `state: present` is not an
instruction to install something. It is an assertion that the thing should exist, and the module
decides whether any action follows. Migrating that is not a rename, and treating it as one loses the
most important information in the playbook.

So the Forge classifies every task it converts, and reports the classification. This is a first class
output, not a footnote in a log.

| Class | What it means | Example |
|-------|---------------|---------|
| **Asserted** | A static desired state. Maps cleanly to a verb. | `apt: name=nginx state=present` becomes `pkg.apt.install` |
| **Computed** | A desired state resolved at run time. Cannot be mapped statically. | `apt: state="{{ nginx_state }}"` |
| **Imperative** | No desired state at all. The task is an action, and nothing can predict its effect. | `command: /usr/local/bin/rebuild.sh` |

### Why this classification is the useful part

It answers a question a migrating user cannot otherwise answer until something breaks: **how much of
this runbook will I be able to dry run?**

`PLAN.md` Section 9 promises `mode: simulate`, which shows what would happen without applying it. That
promise can only be kept for tasks with a knowable desired state. An **asserted** task can be compared
against the device and report whether it would change anything. An **imperative** task cannot, ever,
by anyone. Nobody can predict what an arbitrary shell script does without running it.

A playbook that is mostly asserted converts into a runbook you can plan. A playbook that is mostly
imperative converts into one you cannot, and the honest time to learn that is at migration, not at
task 47 of 200. So the migration report ends with a line like:

```
converted 38 tasks: 31 asserted, 3 computed, 4 imperative
  3 computed tasks need a human to choose a verb (see report)
  4 imperative tasks will not participate in `mode: simulate`
```

### The rule

Never infer a desired state that was not written down. A **computed** task is flagged for a human with
the variable named. An **imperative** task is recorded as imperative and never dressed up as an
assertion to make a summary look better. Both are cases where guessing produces something that looks
right and is wrong, which is what RULE 0 exists to prevent.

## The command surface

Four new verbs live under `pleiades forge`:

```bash
pleiades forge migrate-playbook   playbook.yml
pleiades forge migrate-collection ./my_galaxy_collection
pleiades forge new-collection     pkg.apt.install --capabilities AptCapable
pleiades forge new-device         juniper --type junos_router
```

**Correction (2026-08-05):** the flag was `--device-type` in an earlier revision of this document.
The real flag, and the pre-committed fuzz corpus seed in `cmd/pleiades/cli_fuzz_test.go` that predates
Phase 33's implementation, both say `--type`, matching `add-host --type`'s own established naming.
Code and its own committed test fixture win over prose; this line is corrected to match, the same
resolution `SSHCapable` vs `SSHTransportCapable` used. Also corrected: `new-collection`'s example is now
a real, three-segment namespaced method name (`pkg.apt.install`), not the two-segment `pkg.apt` shown
before Phase 33 existed to generate one for real — `pkg.apt` alone remains a legal registration (PLAN.md
Section 2 allows a bare-domain namespace), but a worked example should show the shape Phase 34 will
actually generate two dozen more of.

Two existing surfaces are branded as part of the Forge but do not move:

- **`pleiades validate` stays top level.** It already exists, it is already one of the three
  validation surfaces in `PLAN.md` Section 5, and moving it would break the existing end to end test.
  The Forge renames nothing here.
- **The IDE plugin is a separate process**, `pleiades-lsp`, not a `forge` subcommand. A language
  server speaking JSON-RPC over stdio has a different lifecycle than a one shot subcommand, so it
  gets its own composition root alongside `cmd/controller` and `cmd/runner`.

**Rejected:** flattening these into top level commands as `pleiades migrate-playbook` and so on.
`PLAN.md` Section 2 already made the "namespace everything, never bare" decision for collection
methods, for exactly the same collision and discoverability reasons. The Forge should not contradict
the principle its own `new-collection` scaffold teaches.

The dispatcher itself is `cmd/pleiades/forge.go` (Phase 30): a `forgeCommands` map mirroring
`main.go`'s own top-level dispatch, empty until the phases above populate it one subcommand at a
time.

## The workflows in detail

### Write a native runbook

Works today. `WorkflowDef` and `Task` in `internal/engine/dag.go` mirror Ansible's structure
(`pre_tasks`, `tasks`, `post_tasks`, `block`, `rescue`, `always`). Conditionals in
`internal/engine/conditional.go` provide `when`, plus `when_or` for OR semantics Ansible lacks, plus
`when_cel` as an escape hatch. `pleiades init` scaffolds a project. The Forge adds nothing here but a
name.

### Migrate an Ansible playbook

Not built. Today `BuildFromYAML` in `internal/engine/yaml.go` detects a playbook shaped file and
rejects it with a message pointing at Section 23. That detection is correct and stays. Phase 35 adds
one line to that message pointing at `forge migrate-playbook`, and nothing else about the detection
changes.

The translator reshapes plays into a `WorkflowDef` and renames modules through the catalog table
above. What it cannot convert, it reports. It never guesses.

### Lint a runbook

Works today. `internal/validate` is a rule registry where rules self register. `pleiades validate`
runs it. `PLAN.md` Section 5 promises one validation core behind three surfaces, and this is that
core. The IDE plugin adopts it rather than forking it.

### The IDE plugin

Not built. There is no language server code and no related dependency anywhere in the repository.
Phase 37 adds `cmd/pleiades-lsp` and `internal/lsp`, translating `validate.Finding` into an LSP
diagnostic. `internal/validate` gains no LSP specific type. Autocomplete over the catalog becomes
useful once Phase 34 exists, but diagnostics alone are the release gate, so the plugin never waits on
the catalog.

### Create a Collection

Built. `pkg/collection`'s `Manifest` and `Descriptor`/`Register`/`Lookup` (Phase 31), mirroring
`pkg/capability`'s naming and built on `pkg/registry`'s existing generic Registry (Phase 6), already
backing `pkg/capability`'s own capability vocabulary and `internal/inventory/record`'s device-type
table -- `pkg/collection` is that primitive's third consumer, not its first. `internal/forge/collectionscaffold`
(Phase 33) generates a new namespaced method package: a registration in `init()`, a stub built on
`pkg/sdk.RunbookContext`, and a starter table-driven test, driven by `pleiades forge new-collection`.

**A second honest limitation, alongside the device type one below.** `pkg/collection` is planning-time
metadata only. No dispatcher anywhere in this codebase yet consumes it, or `pkg/sdk.RunbookContext`, to
actually call a registered method's real implementation: `internal/engine/action.go`, the only real
action executor today, dispatches on a hardcoded switch over bare `task.FQCN` strings and touches
neither package. A generated stub is real, buildable, testable Go code, immediately registered and
resolvable through `collection.Lookup`, but it is not reachable from any execution path until a later
phase builds that dispatcher.

### Create an inventory device type

The pattern works today. `internal/inventory/devices/cisco` and `devices/linux` build on
`record.Base` and self-register via `record.RegisterType` in their own `init()`, triggered by
`internal/inventory/builtins.go`'s blank-import list. `internal/inventory/devicescaffold` (Phase 33)
generates a new vendor package mirroring this pattern, driven by `pleiades forge new-device`.

**One honest limitation.** `internal/inventory/builtins.go` blank-imports exactly two device packages
(`cisco`, `linux`), and that list, not `NewItemFactory()` itself, is what is closed:
`NewItemFactory()` (`internal/inventory/factory.go`) builds its constructor set from
`record.AllTypes()`, and `cmd/pleiades/load.go` calls that constructor directly. So a generated device
type is not reachable from the stock binary until a human adds a blank import of it to
`builtins.go` (or their own composition root) -- the one composition-root change this document's
earlier revisions described as needed against a different, now-retired API
(`ItemFactory.Register`, retired during the Phase 6 registry retrofit; the real registration call is
`record.RegisterType`). `record.RegisterType` itself is genuinely open. The closed part is the
built-in default set, not the mechanism.

**A second scope limitation, specific to the generator.** `pkg/capability.Descriptor`'s `Assert` is an
opaque function with no reflectable method-set metadata, so `devicescaffold` cannot mechanically know
that, say, `JunosCapable` implies a `JunosVersion() string` accessor, or what to name it. A generated
device type is therefore a structural skeleton (a `record.Base` embed, a capability-baseline
constructor, `HasCapability`, and a `var _ inventory.InventoryItem = (*T)(nil)` compile-time
assertion the hand-written packages lack) with no capability-specific accessor methods: immediately
after generation, `HasCapability` correctly returns `false` for every declared capability until a
human adds real accessor methods matching each one's interface.

### Migrate a Galaxy collection

Not built, and the hardest of the seven. A Galaxy collection holds roles (YAML, convertible) and
plugin modules (Python, not convertible).

**Rejected: transpiling Python to Go.** Arbitrary Python with dynamic behavior and arbitrary imports
has no mechanical Go equivalent. Attempting it would produce code that looks right and is wrong,
which is the inferred correctness RULE 0 forbids.

**Adopted:** convert what is mechanical, scaffold the rest, and flag every gap. Role tasks go through
the playbook translator. Each Python module produces a compiling Go stub carrying a
`// TODO(forge):` marker naming the source file and line, plus a report entry. Nothing is dropped
silently.

## Honest limits

A translator that converts all thirty six modules and chokes on `loop:` has not migrated anything
real. These are the known gaps, stated up front rather than discovered during a migration.

### Templating

`ansible.builtin.template` renders Jinja2. This engine uses CEL. CEL is an expression language: it
has no loops, no blocks, and no filters. It cannot render a Jinja2 template, and no amount of
mapping makes it able to.

So `file.template` needs a real template renderer, separate from CEL. `PLAN.md`'s shared primitives
table already names one ("Template renderer, one Jinja compatible engine, compile and cache"). The
two are not competitors. CEL evaluates conditions. The renderer renders files. `file.template` is
rated **manual** in the mapping until that renderer exists.

### A templated `state` cannot become a verb

This is the one real cost of splitting `state` into methods, and it is worth stating plainly because
the two-part alternative (`pkg.apt` carrying a `state` parameter) would not have it.

Ansible allows the state to be computed:

```yaml
- ansible.builtin.apt:
    name: nginx
    state: "{{ nginx_state }}"
```

There is no static mapping from that to `pkg.apt.install` or `pkg.apt.remove`, because which one it
is depends on a variable that is not known until run time. The verb is part of the name, so the name
cannot be chosen at translation time. This pattern is not rare: it is common in roles that expose
state through `defaults/main.yml`.

The translator handles it in one of two ways, and never by guessing:

1. If the variable has exactly one reachable value in the source (a `defaults` entry never
   overridden), resolve it and record the resolution in the report.
2. Otherwise, flag the task as **manual** with the variable named, and leave it for a human.

A related smaller case: Ansible's `state: latest` means "upgrade if installed, install if missing,"
which is not cleanly either `install` or `upgrade`. `pkg.apt.upgrade` must define which it is, and
say so in its manifest, rather than inheriting an ambiguity.

### Dynamic inclusion

`include_tasks` resolves at runtime. This engine compiles and validates a DAG before anything runs,
which is the whole "type safety moves left" thesis. A task list that does not exist until runtime
cannot be validated at plan time. These two facts are in genuine tension.

`import_tasks` (static, parse time) is compatible and converts cleanly. `include_tasks` does not, and
is rated **manual**. Resolving it properly is deferred rather than papered over.

### Everything that is not a module

A real playbook contains much more than module calls. The current `WorkflowDef` and `Task` schema
does not represent most of it. Each item below must be either supported or explicitly flagged by the
translator:

`loop` and `with_items`, `handlers` and `notify`, `vars` and `vars_files`, `tags`, `become`,
`delegate_to`, `run_once`, `serial`, `check_mode`, `register` followed by use of the registered
value, `ignore_errors`, `retries` and `until`, `changed_when` and `failed_when`, vault encrypted
values, and `group_vars` or `host_vars` from inventory.

This list is the real scope of playbook migration. The thirty six modules are the easy part.

Two of these deserve specific mention because the engine lacks the machinery, not just the field.
`loop` has no representation and there is no fan-out model either: a target resolving to many devices
is counted but never iterated. `handlers` and `notify` need a "changed" result state that does not
exist anywhere in the engine, plus a non-linear execution order, while `Adjacency` today is a single
linear chain. Neither is a matter of adding a key.

`register` is a third case worth naming: the field exists on `Task` and nothing reads it. There is no
result object and no fact store behind it yet.

### Two parser defects that must be fixed before any translator ships

Both were found while writing this document, not by a failing test, and both would quietly defeat the
guarantee in the next section.

**Unknown keys are silently dropped.** Neither decode path restricts unknown fields. A runbook
containing `become: true`, `loop:`, `tags:`, or `notify:` builds and validates exactly as if those
keys had never been written. So a translator could honestly report that it could not convert `loop:`
and the data would still be lost one layer downstream. A no-silent-drop promise is only as strong as
the parser that reads the output. See `FAILURE_PATTERNS.md` entry 10.

**A non-string `target` disables two validation rules.** Both the capability rule and the blast radius
rule read the target through an unchecked type assertion that yields an empty string on failure, then
skip. So `target: [web1, web2]` validates clean and checks nothing. A malformed target currently
produces more confidence than a well formed one. See `FAILURE_PATTERNS.md` entry 11.

Both are scheduled in Phase 35, ahead of the translator itself.

### No silent drops

Every conversion produces a report listing each source construct as converted or flagged. The
invariant is that a construct appears exactly once, never zero times and never twice. This is
asserted in tests, not assumed, and it depends on the two parser fixes above.

## Summary

Run Ansible as Ansible is `PLAN.md` Section 23. Convert Ansible into native format is the Forge.
There is no AWX server importer, because inventory belongs to a sync plugin, source belongs to the
Forge, and secrets belong to a human.

Native names are `<namespace>.<method>`, never bare, with the capability hierarchy visible in the
name. The tooling is built before the catalog it generates. A declared module that is not implemented
fails loudly and the linter catches it at write time.
