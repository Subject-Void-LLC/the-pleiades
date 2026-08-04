# Imaginary Deployment Scenario: The Installation Process

**The Scenario:** Before Customer X could upgrade their Cisco Lab, they had to install Pleiades from scratch. They have a central AWS environment where they want the Control Plane, and an on-premise "Lab" network where the switches live.

Here is what Day 0 (Installation & Bootstrapping) looked like.

---

### Phase 1: The Control Plane (Central AWS)
Customer X's infrastructure team spins up the core backend.
1. **Database:** They provision an Amazon RDS PostgreSQL instance to act as the State Store and Lock Manager.
2. **Controller Deployment:** They deploy the `pleiades-controller` using a standard Helm chart into their EKS cluster (or Docker Compose on a VM). 
3. **Bootstrapping:** The Controller starts up, automatically runs DB migrations to create the inventory schemas, and outputs a one-time `Root Admin Password`.
4. **Security Initialization:** They inject a `MASTER_ENCRYPTION_KEY` into the Controller's environment to securely encrypt the Postgres DB at rest (Section 17).
5. **Initial Setup:** Customer X logs in using the Root Admin, configures a WebAuthn Passkey (Section 18), and generates a **Runner Provisioning Token**.

### Phase 2: Wiring up GitOps (Section 10)
Pleiades enforces infrastructure-as-code from Day 1.
1. Customer X creates a blank Git repository called `pleiades-config` in their corporate GitHub.
2. In the Controller UI, they configure the "Config Repo" by providing a read-only Deploy Key for that GitHub repository.
3. The Controller immediately syncs the repository, establishing the baseline pipeline for all future runbooks and triggers.

### Phase 3: The Data Plane (On-Premise Lab Runner)
Customer X needs a Runner in their isolated Lab network to actually talk to the Cisco switches. They provision a basic Ubuntu Linux VM in the Lab VLAN.
1. **No Heavy Dependencies:** Because the Runner is just a compiled Go binary, they don't need to install Docker, Podman, Python, or Ansible. 
2. **Download:** They simply run:
   ```bash
   curl -O https://controller.customerx.com/downloads/pleiades-runner-linux-amd64
   chmod +x pleiades-runner-linux-amd64
   ```
3. **Configuration:** They create a simple `/etc/pleiades/runner.yaml`:
   ```yaml
   controller_url: "grpcs://controller.customerx.com:443"
   provisioning_token: "tok_12345ABCDEF"
   runner_tags:
     - "location:lab-dmz"
     - "group:lab"
   ```
4. **Start:** They configure a `systemd` service and start the Runner.
5. **Registration:** The Runner connects outbound over port 443 to the Controller, authenticates using the token, and registers itself as available to execute tasks for `group:lab`.

### Phase 4: Bootstrapping Discovery (Section 11)
The Controller is running, and the Runner is waiting. Now they need to discover the switches.
1. Customer X uses NetBox as their source of truth.
2. They navigate to the **Sync Plugins** page in the Controller UI.
3. They configure the `NetBox` plugin, providing their NetBox API URL and token.
4. They click "Run Sync".
5. The Controller queries NetBox, parses the results, and the **InventoryFactory** mints the 10 Cisco switches into the State Store, automatically assigning them to `group:lab`.

### Phase 5: Identity & Observability (Sections 18, 19)
Before letting the team loose, the infrastructure team locks down the platform.
1. **SSO Integration:** They configure the SAML 2.0 provider to point to Okta. They set the mapping so that anyone in the Okta `NetworkAdmins` group is automatically mapped to the Pleiades `Team:NetworkAdmins`.
2. **RBAC Assignment:** They grant `Team:NetworkAdmins` the `Executor` role scoped strictly to `group:lab`. 
3. **Observability:** They point the built-in OTEL exporter to their Datadog/Honeycomb APM, ensuring every API call, Event Bus message, and Runner execution generates a single distributed trace.

**Result:** Customer X goes from zero to a fully governed, GitOps-backed automation platform with a secure, outbound-only execution mesh, centralized SAML authentication, and end-to-end tracing in under an hour. They are now ready to write their `upgrade_cisco_ios.yaml` runbook.
