# Imaginary Deployment Scenario: Cisco Lab Upgrade

**Internal design material, not user documentation.** This narrative describes an aspirational
architecture, not the product as built: the runbook schema shown below (`steps:`, `action:`,
`requires:`, `collection:`) has no relationship to the real one (`id`/`hosts`/`tasks`,
`fqcn`/`params`; see `internal/engine/task_syntax.go`), and features referenced here (Okta/SAML,
a NetBox plugin, a Module Registry serving compiled binaries, Postgres-backed locks) do not exist
in the codebase today. Kept for its architectural framing, not its specifics. Never publish this
file as user documentation; see the "Migrating from Ansible, AWX and AAP" book and the Crawl-tier
quickstart under `docs/` for what actually runs.

**The Scenario:** Customer X has just installed The Pleiades. They want to upgrade the firmware on 10 Cisco Catalyst switches in their "Lab" environment. 

Here is how the entire platform architecture (Sections 1 through 16) works together to execute this request safely and at scale.

---

### Step 1: Discovery & Onboarding (Sections 4, 11)
Customer X configures a Sync Plugin (e.g., NetBox or a simple CSV plugin). 
1. The Controller's discovery engine runs the plugin and finds the 10 switches.
2. The **InventoryFactory** processes the raw data and mints 10 concrete objects in the State Store.
3. The platform determines these are `NetworkDevice` types and assigns them the `CiscoIOSCapable` and `SSHTransportCapable` capability interfaces.
4. The devices are tagged and placed into `group:lab`.

### Step 2: Authoring the Runbook (Section 1, 14)
Customer X writes their first runbook, `upgrade_cisco_ios.yaml`.
1. In the runbook header, they specify `requires: [CiscoIOSCapable]`. This immediately guarantees the runbook can never be accidentally run against a Linux server or Juniper router. 
2. The runbook references a pre-built Collection: `pleiades/cisco-ios-utils`.
3. The steps are defined: `backup_config()`, `scp_image()`, `set_boot_var()`, `reload()`.

```yaml
name: Upgrade Cisco IOS
description: "Upgrades firmware on Catalyst switches to 17.03.04"
requires:
  - CiscoIOSCapable
  - SSHTransportCapable

collection: pleiades/cisco-ios-utils@v1.2.0

steps:
  - name: "Backup running config"
    action: backup_config
    params:
      destination: s3://cisco-backups/

  - name: "Transfer firmware image"
    action: scp_image
    params:
      source_uri: https://firmware-repo/cat9k_iosxe.17.03.04.SPA.bin
      verify_md5: true

  - name: "Set boot variable and reload"
    action: set_boot_var
    params:
      image: flash:cat9k_iosxe.17.03.04.SPA.bin
      reload: true
```

### Step 3: CI/CD Governance (Section 10)
Pleiades doesn't allow editing production runbooks in a Web UI. 
1. Customer X commits `upgrade_cisco_ios.yaml` to their internal `platform-config` Git repository.
2. The commit triggers the platform's CI/CD pipeline.
3. The linter validates the YAML, ensures the referenced Go collection exists, and verifies that `group:lab` actually contains devices with `CiscoIOSCapable`. Type-safety moves left.
4. The PR is approved and merged. The Controller automatically ingests the new runbook.

### Step 4: Dispatch & RBAC (Sections 15, 16, 18)
Customer X logs into the Web UI via Okta (SAML). Because they are in the Okta `NetworkAdmins` group, The Pleiades grants them the `Executor` role scoped strictly to `group:lab`.
1. Customer X clicks "Run" on the upgrade playbook.
2. The **Trigger Engine** receives the manual event.
3. It expands `group:lab` into 10 distinct device targets, verifies Customer X's RBAC scope allows this action on all 10 devices, and generates 10 task payloads.
4. It pushes these 10 tasks into the Event Bus.
5. **Observability (Section 19):** An OpenTelemetry (OTEL) Trace ID is generated the moment Customer X clicks "Run". This Trace ID is attached to the task payloads in the Event Bus.

### Step 5: Concurrency & Lock Management (Section 6, 13)
The system doesn't blast all 10 switches at once. 
1. Customer X previously configured a Hierarchical Policy on `group:lab` setting `max_concurrency: 2`.
2. The **Lock Manager** (backed by Postgres row-level locks) evaluates the queue.
3. It grants exclusive execution locks for `switch-1` and `switch-2`. The other 8 tasks remain queued.

### Step 6: Data Plane Execution & Secrets (Sections 14, 16, 17)
Two Runners sitting in the customer's on-prem DMZ are polling the Controller via mTLS-secured gRPC.
1. Runner A and Runner B see the tasks for `switch-1` and `switch-2`.
2. They ask the Controller's Module Registry for the `pleiades/cisco-ios-utils` binary. 
3. The Runners pull the 10MB compiled Go binary, cache it locally, and execute it in milliseconds.
4. **Just-In-Time Secrets:** The Controller fetches the Cisco SSH passwords from the encrypted State Store (or HashiCorp Vault) and attaches them to the payload. The Runner never writes these to disk. It injects the secret securely into the Go binary via `stdin` or unix socket, and the Go binary zeroes-out that memory slice the moment authentication completes.
5. The binary utilizes the **Transport Abstraction** using the injected credentials to proxy the SSH commands to the switch.

### Step 7: The "Weird Stuff" Edge Case (Section 16)
While `switch-1` is in the middle of transferring the 500MB firmware image, the corporate firewall temporarily drops the gRPC connection between Runner A and the Controller.
1. Runner A loses its heartbeat.
2. Because a firmware transfer is critical, the task was marked `interruptible: false`. 
3. Runner A ignores the lost heartbeat and finishes the SCP transfer. 
4. The Controller's lock TTL expires. The Controller marks `switch-1` as `Quarantined`. It does **not** give the task to another runner. 
5. Once the network recovers, Runner A dumps its Write-Ahead Log (WAL) to the Controller, proving the transfer succeeded. The Controller un-quarantines the device.

### Step 8: Completion & Observability (Sections 12, 19)
1. Both switches finish the upgrade and reload.
2. The Runners emit a `device.state_changed` event back to the Event Bus (carrying the original OTEL Trace ID).
3. The Lock Manager releases the locks.
4. The next 2 switches in the queue (`switch-3`, `switch-4`) are granted locks and the process continues until all 10 are upgraded.
5. Every single action is permanently recorded in the Postgres State Store as an idempotent audit trail.
6. The entire lifecycle (from the Okta login, to the Trigger Engine, to the Lock Manager, to the Runner execution) is stitched together via the OTEL Trace ID into a single, comprehensive APM dashboard in Apache E-charts for Customer X to review.

```json
{
  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
  "spans": [
    { "service": "api-gateway", "name": "POST /api/v1/runbooks/dispatch", "duration_ms": 45 },
    { "service": "trigger-engine", "name": "Expand Target Group [lab]", "duration_ms": 12 },
    { "service": "lock-manager", "name": "Acquire Lock [switch-1]", "duration_ms": 8 },
    { "service": "runner-mesh", "name": "Pull Collection [cisco-ios-utils]", "duration_ms": 110 },
    { "service": "runner-exec", "name": "Action: scp_image", "duration_ms": 45000 },
    { "service": "event-bus", "name": "Emit device.state_changed", "duration_ms": 5 }
  ]
}
```
