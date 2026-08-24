# Pleiades

An object-oriented, strongly typed automation mesh that also runs Ansible. Ansible
support is the migration on-ramp, not the destination: a workload lands unchanged,
then converts to native typed collections at its own pace.

**Status: pre-1.0, actively built.** The parts described below as real are real and
tested today; nothing here is aspirational. Read this section before the rest.

- **The Crawl tier is real.** `pleiades` is a single, self-contained binary: no server,
  no database, no message broker. It links its own inventory, engine, and validation
  packages directly, connects over a real SSH transport, and genuinely executes
  `ssh_exec` tasks against real devices. This was proven again while writing this
  README, against three live Ubuntu containers (`examples/webserver_lab/`).
- **The control plane is real and tested.** The data layer, event bus, distributed
  locking, leader election, envelope encryption, inventory factory, RBAC, the CEL
  conditional engine, the workflow DAG builder, the HATEOAS API gateway, and the job
  dispatcher are all built, with real integration tests, not mocks.
- **The distributed execution plane reaches real devices.** A job dispatched through
  the Controller and picked up by a `runner` over NATS runs the runbook against the
  device it names, over the same real SSH transport the CLI uses, with each Collection
  method executing in its own child process so credentials cross on standard input
  rather than through argv or the environment. Two limits: a device's credential rides
  the dispatch message, so it sits in the broker's storage until that message ages out,
  and credential storage is still an encrypted local file with no rotation or Vault
  support.
- **The module catalog has 78 registered methods; 74 are implemented.** Every
  `<namespace>.<method>` collection name is registered and
  reachable through the real dispatcher, but a `declared` method refuses to run with
  an explicit "not implemented" error rather than pretending to succeed. Only four are
  in that state: `file.template`, `net.netconf.config`, `net.junos.config` and
  `net.eos.config`. Everything else is real today, across 16 namespaces, including
  `exec.command` and `exec.shell` on any SSH-reachable device, `net.cli.command` and
  `net.ios.config` on a real network CLI, the four `net.catalyst.*` methods against
  Cisco Catalyst Center, the `svc.*` service managers, and the `pkg.*`, `file.*`,
  `identity.*`, `container.*` and `cloud.aws.*` families. Every
  implemented method also answers whether it can be undone, and a run that changes
  something records the instruction that would reverse it.

## What makes this different

- **Type safety moves left.** Capabilities, transports and conditionals are declared
  as typed data, so `pleiades validate` catches a bad FQCN or an uncompilable
  condition before a run starts. Device capability matching is not there yet: it
  covers only two legacy action names, so a module pointed at a device that cannot
  run it still fails during the run. See `docs/01-start-here.md`.
- **Inventory is a versioned artifact**, not a row in a table: every device carries a
  lifecycle state, a source, and a history.
- **CEL conditionals**, compiled before execution, including `when_or` and a raw-CEL
  escape hatch (`when_cel`) for logic a bare `when:` cannot express.
- **Per-device locks** with a configurable acquisition policy, not a single coarse
  job-level lock. The distributed lock manager exists but only the `controller` and
  `runner` binaries use it: the CLI's locking is in-process only and does not exclude
  a second `pleiades run`. See `docs/10-running-in-production.md`.
- **A single static Go binary**, not a Python virtualenv or a container execution
  environment.
- **A strict superset of Ansible's own vocabulary**: `hosts:`, `block`,
  `register`, and `when:` all mean what they already mean, with native extensions
  added alongside them, never renamed out from under a migrating user.

## Try it

```bash
go build -o pleiades ./cmd/pleiades
mkdir my-project && cd my-project
../pleiades init
../pleiades add-host web1 --type linux_server --set host=<ip>
../pleiades add-credential web1 --username <user>
../pleiades validate
../pleiades run runbooks/sample.yaml
```

## Documentation

- [`docs/01-start-here.md`](docs/01-start-here.md): what Pleiades is, the Crawl/Walk/Run
  tiers, and the implementation status summary.
- [`docs/02-get-started.md`](docs/02-get-started.md): the Crawl-tier and Walk-tier
  quickstarts, with real captured command output.
- [`docs/03-migrating-from-ansible.md`](docs/03-migrating-from-ansible.md): the keyword,
  module-to-FQCN, and AWX/AAP object maps for a real migration.
- [`docs/09-control-plane-and-api.md`](docs/09-control-plane-and-api.md): the API route
  table, authorization, errors, hypermedia, and the SSE job log stream contract.
- [`docs/10-running-in-production.md`](docs/10-running-in-production.md): failure
  semantics, safety, security, credentials, and what data is and is not encrypted.
- [`docs/11-extending-pleiades.md`](docs/11-extending-pleiades.md): the Forge commands,
  the Collection method and sync plugin contracts, their conformance suite, and a
  step-by-step worked example that takes one real module from naming it to running
  it against a real device.
- [`docs/12-web-ui.md`](docs/12-web-ui.md): the web UI the controller serves itself —
  signing in, what each view does and does not do, the environment banner, mobile,
  accessibility (including the manual verification script), and adding a view.
- [`docs/13-releases-and-stability.md`](docs/13-releases-and-stability.md) and
  [`docs/14-project.md`](docs/14-project.md): changelog, versioning, contributing,
  license, and where each of those actually lives today.
- [`docs/reference/`](docs/reference/index.md): the generated reference: module catalog,
  capability vocabulary, device types, sync plugins, runbook and task keys, the CLI
  ([`docs/reference/cli.md`](docs/reference/cli.md)), JSON Schemas, the OpenAPI document
  (also served live at `/api/v1/openapi.json`), and the full declared-vs-implemented
  matrix.
- [`examples/upgrade_ios/`](examples/upgrade_ios/): a real side-by-side Ansible vs.
  Pleiades comparison, migrating a Cisco IOS-XE upgrade playbook.
- [`examples/webserver_lab/`](examples/webserver_lab/): three real Ubuntu targets used
  to capture genuine `pleiades` output for documentation.

More is being written; see [`CHANGELOG.md`](CHANGELOG.md) for what has landed most
recently.

## Contributing and security

See [`CONTRIBUTING.md`](CONTRIBUTING.md) for the development workflow, and
[`SECURITY.md`](SECURITY.md) to report a vulnerability.

## License

[GNU GPLv3](LICENSE).
