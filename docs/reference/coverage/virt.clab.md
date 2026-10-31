---
status: beta
---

# virt.clab: containerlab API server

Contract (OpenAPI document): <https://github.com/srl-labs/clab-api-server/blob/a70e099b481bb2be7c873cbad506bb40e234c43f/docs/swagger.json>, version `v0.9.0` (revision `a70e099b481bb2be7c873cbad506bb40e234c43f`), sha256 `81ef3ecc951051010a4ec6941d6b02f862f84e3a4e98e064871fd38475a3ba20`.

**98 operations: 44 mapped, 12 excluded, 42 not implemented. Mapped 51.2%, live 39.5% of the 86 in scope.**

## Operations

| Operation | Contract summary | Status | Method, or why excluded | Live |
| --- | --- | --- | --- | --- |
| `DELETE /api/v1/capture/wireshark-vnc-sessions` | Delete all Wireshark VNC sessions | unmapped | - | - |
| `DELETE /api/v1/capture/wireshark-vnc-sessions/{sessionId}` | Delete Wireshark VNC session | unmapped | - | - |
| `GET /api/v1/capture/wireshark-vnc-sessions/{sessionId}/ready` | Get Wireshark VNC session readiness | unmapped | - | - |
| `GET /api/v1/capture/wireshark-vnc-sessions/{sessionId}/vnc/{proxyPath}` | Proxy Wireshark VNC assets | excluded | proxies the noVNC page assets a browser loads for a Wireshark session; a runbook has no browser to load them into | - |
| `GET /api/v1/events` | Stream containerlab events | unmapped | - | - |
| `POST /api/v1/generate` | Generate containerlab topology | mapped | [virt.clab.topology.generate](../modules/virt/clab/topology/generate.md) | yes |
| `GET /api/v1/health/metrics` | Get system metrics | mapped | [virt.clab.server.metrics](../modules/virt/clab/server/metrics.md) | yes |
| `GET /api/v1/images` | List runtime images | mapped | [virt.clab.image.list](../modules/virt/clab/image/list.md) | yes |
| `DELETE /api/v1/images` | Remove a runtime image | mapped | [virt.clab.image.remove](../modules/virt/clab/image/remove.md) | no |
| `POST /api/v1/images/pull` | Pull a runtime image | mapped | [virt.clab.image.pull](../modules/virt/clab/image/pull.md) | yes |
| `GET /api/v1/labs` | List labs | mapped | [virt.clab.lab.list](../modules/virt/clab/lab/list.md) | yes |
| `POST /api/v1/labs` | Deploy lab | mapped | [virt.clab.lab.deploy](../modules/virt/clab/lab/deploy.md) | yes |
| `POST /api/v1/labs/archive` | Deploy lab from archive | unmapped | - | - |
| `GET /api/v1/labs/topology/files` | List editable lab topology files | unmapped | - | - |
| `POST /api/v1/labs/topology/import-from-url` | Import topology repository as undeployed lab | unmapped | - | - |
| `POST /api/v1/labs/workspace/directory` | Create lab workspace directory | unmapped | - | - |
| `GET /api/v1/labs/workspace/events` | Stream lab workspace file events | unmapped | - | - |
| `GET /api/v1/labs/workspace/file` | Read lab workspace file | unmapped | - | - |
| `PUT /api/v1/labs/workspace/file` | Write lab workspace file | unmapped | - | - |
| `DELETE /api/v1/labs/workspace/file` | Delete lab workspace file | unmapped | - | - |
| `POST /api/v1/labs/workspace/file/rename` | Rename lab workspace file | unmapped | - | - |
| `GET /api/v1/labs/workspace/tree` | List lab workspace files | unmapped | - | - |
| `GET /api/v1/labs/{labName}` | Inspect lab | mapped | [virt.clab.lab.info](../modules/virt/clab/lab/info.md) | yes |
| `PUT /api/v1/labs/{labName}` | Redeploy lab | mapped | [virt.clab.lab.redeploy](../modules/virt/clab/lab/redeploy.md) | no |
| `DELETE /api/v1/labs/{labName}` | Destroy lab | mapped | [virt.clab.lab.destroy](../modules/virt/clab/lab/destroy.md) | yes |
| `POST /api/v1/labs/{labName}/apply` | Apply on-disk topology for lab | mapped | [virt.clab.lab.apply](../modules/virt/clab/lab/apply.md) | yes |
| `POST /api/v1/labs/{labName}/capture/packetflix` | Build packetflix capture URI | mapped | [virt.clab.capture.packetflix](../modules/virt/clab/capture/packetflix.md) | yes |
| `POST /api/v1/labs/{labName}/capture/wireshark-vnc-sessions` | Create Wireshark VNC capture session(s) | unmapped | - | - |
| `POST /api/v1/labs/{labName}/deploy` | Deploy on-disk topology for lab | unmapped | - | - |
| `POST /api/v1/labs/{labName}/exec` | Execute command in lab | mapped | [virt.clab.node.exec](../modules/virt/clab/node/exec.md) | yes |
| `POST /api/v1/labs/{labName}/fcli` | Run fcli command | unmapped | - | - |
| `POST /api/v1/labs/{labName}/gotty/{action}` | GoTTY share action | unmapped | - | - |
| `POST /api/v1/labs/{labName}/graph/drawio` | Generate draw.io graph | unmapped | - | - |
| `GET /api/v1/labs/{labName}/interfaces` | List lab interfaces | mapped | [virt.clab.lab.interfaces](../modules/virt/clab/lab/interfaces.md) | yes |
| `GET /api/v1/labs/{labName}/links` | List lab links | mapped | [virt.clab.lab.links](../modules/virt/clab/lab/links.md) | yes |
| `GET /api/v1/labs/{labName}/nodes/{nodeName}/browser-ports` | Get node browser ports | mapped | [virt.clab.node.ports](../modules/virt/clab/node/ports.md) | yes |
| `GET /api/v1/labs/{labName}/nodes/{nodeName}/logs` | Get node logs | mapped | [virt.clab.node.logs](../modules/virt/clab/node/logs.md) | yes |
| `POST /api/v1/labs/{labName}/nodes/{nodeName}/pause` | Pause node | mapped | [virt.clab.node.pause](../modules/virt/clab/node/pause.md) | yes |
| `POST /api/v1/labs/{labName}/nodes/{nodeName}/restart` | Restart node | mapped | [virt.clab.node.restart](../modules/virt/clab/node/restart.md) | yes |
| `POST /api/v1/labs/{labName}/nodes/{nodeName}/ssh` | Request temporary SSH access details | mapped | [virt.clab.node.ssh_access](../modules/virt/clab/node/ssh_access.md) | yes |
| `POST /api/v1/labs/{labName}/nodes/{nodeName}/start` | Start node | mapped | [virt.clab.node.start](../modules/virt/clab/node/start.md) | no |
| `POST /api/v1/labs/{labName}/nodes/{nodeName}/stop` | Stop node | mapped | [virt.clab.node.stop](../modules/virt/clab/node/stop.md) | yes |
| `POST /api/v1/labs/{labName}/nodes/{nodeName}/terminal-sessions` | Create browser terminal session | unmapped | - | - |
| `POST /api/v1/labs/{labName}/nodes/{nodeName}/unpause` | Unpause node | mapped | [virt.clab.node.unpause](../modules/virt/clab/node/unpause.md) | yes |
| `POST /api/v1/labs/{labName}/restart` | Restart lab nodes | mapped | [virt.clab.lab.restart](../modules/virt/clab/lab/restart.md) | no |
| `POST /api/v1/labs/{labName}/save` | Save lab configuration | mapped | [virt.clab.lab.save](../modules/virt/clab/lab/save.md) | yes |
| `POST /api/v1/labs/{labName}/sshx/{action}` | SSHX share action | unmapped | - | - |
| `POST /api/v1/labs/{labName}/start` | Start lab nodes | mapped | [virt.clab.lab.start](../modules/virt/clab/lab/start.md) | yes |
| `POST /api/v1/labs/{labName}/stop` | Stop lab nodes | mapped | [virt.clab.lab.stop](../modules/virt/clab/lab/stop.md) | yes |
| `GET /api/v1/labs/{labName}/tailscale` | Get lab Tailscale status | unmapped | - | - |
| `GET /api/v1/labs/{labName}/topology/annotations` | Get lab annotations | unmapped | - | - |
| `PUT /api/v1/labs/{labName}/topology/annotations` | Update lab annotations | unmapped | - | - |
| `GET /api/v1/labs/{labName}/topology/events` | Stream topology document events | unmapped | - | - |
| `GET /api/v1/labs/{labName}/topology/file` | Read lab topology file | unmapped | - | - |
| `PUT /api/v1/labs/{labName}/topology/file` | Write lab topology file | unmapped | - | - |
| `DELETE /api/v1/labs/{labName}/topology/file` | Delete lab topology file | unmapped | - | - |
| `HEAD /api/v1/labs/{labName}/topology/file` | Check lab topology file existence | unmapped | - | - |
| `POST /api/v1/labs/{labName}/topology/file/rename` | Rename lab topology file | unmapped | - | - |
| `GET /api/v1/labs/{labName}/topology/yaml` | Get lab topology YAML | mapped | [virt.clab.lab.topology](../modules/virt/clab/lab/topology.md) | yes |
| `PUT /api/v1/labs/{labName}/topology/yaml` | Update lab topology YAML | unmapped | - | - |
| `GET /api/v1/labs/{labName}/ui/icons` | List lab custom icons | excluded | serves the containerlab web UI's own state (icons and node templates for its editor), not a lab, a node or the server a runbook acts on | - |
| `POST /api/v1/labs/{labName}/ui/icons/reconcile` | Reconcile lab custom icons | excluded | serves the containerlab web UI's own state (icons and node templates for its editor), not a lab, a node or the server a runbook acts on | - |
| `POST /api/v1/labs/{labName}/validate` | Validate on-disk topology for lab | mapped | [virt.clab.lab.validate](../modules/virt/clab/lab/validate.md) | yes |
| `GET /api/v1/ssh/sessions` | List SSH sessions | mapped | [virt.clab.ssh_session.list](../modules/virt/clab/ssh_session/list.md) | yes |
| `DELETE /api/v1/ssh/sessions/{port}` | Terminate SSH session | mapped | [virt.clab.ssh_session.terminate](../modules/virt/clab/ssh_session/terminate.md) | yes |
| `GET /api/v1/terminal-sessions/{sessionId}` | Get terminal session | unmapped | - | - |
| `DELETE /api/v1/terminal-sessions/{sessionId}` | Terminate terminal session | unmapped | - | - |
| `GET /api/v1/terminal-sessions/{sessionId}/stream` | Stream terminal session | unmapped | - | - |
| `POST /api/v1/tools/certs/ca` | Create certificate authority (CA) | unmapped | - | - |
| `POST /api/v1/tools/certs/sign` | Sign certificate | unmapped | - | - |
| `POST /api/v1/tools/disable-tx-offload` | Disable TX checksum offload | mapped | [virt.clab.node.disable_tx_offload](../modules/virt/clab/node/disable_tx_offload.md) | yes |
| `POST /api/v1/tools/edgeshark/install` | Install EdgeShark | mapped | [virt.clab.edgeshark.install](../modules/virt/clab/edgeshark/install.md) | no |
| `GET /api/v1/tools/edgeshark/status` | Get EdgeShark status | mapped | [virt.clab.edgeshark.status](../modules/virt/clab/edgeshark/status.md) | yes |
| `POST /api/v1/tools/edgeshark/uninstall` | Uninstall EdgeShark | mapped | [virt.clab.edgeshark.uninstall](../modules/virt/clab/edgeshark/uninstall.md) | no |
| `POST /api/v1/tools/netem/reset` | Reset link impairments (netem) | mapped | [virt.clab.netem.reset](../modules/virt/clab/netem/reset.md) | yes |
| `POST /api/v1/tools/netem/set` | Set link impairments (netem) | mapped | [virt.clab.netem.set](../modules/virt/clab/netem/set.md) | yes |
| `GET /api/v1/tools/netem/show` | Show link impairments (netem) | mapped | [virt.clab.netem.show](../modules/virt/clab/netem/show.md) | yes |
| `POST /api/v1/tools/veth` | Create vEth pair | mapped | [virt.clab.veth.create](../modules/virt/clab/veth/create.md) | no |
| `POST /api/v1/tools/vxlan` | Create VxLAN tunnel | mapped | [virt.clab.vxlan.create](../modules/virt/clab/vxlan/create.md) | no |
| `DELETE /api/v1/tools/vxlan` | Delete VxLAN tunnels by prefix | mapped | [virt.clab.vxlan.delete](../modules/virt/clab/vxlan/delete.md) | no |
| `GET /api/v1/ui/custom-nodes` | List custom node templates | excluded | serves the containerlab web UI's own state (icons and node templates for its editor), not a lab, a node or the server a runbook acts on | - |
| `PUT /api/v1/ui/custom-nodes` | Replace custom node templates | excluded | serves the containerlab web UI's own state (icons and node templates for its editor), not a lab, a node or the server a runbook acts on | - |
| `POST /api/v1/ui/custom-nodes` | Save custom node template | excluded | serves the containerlab web UI's own state (icons and node templates for its editor), not a lab, a node or the server a runbook acts on | - |
| `POST /api/v1/ui/custom-nodes/default` | Set default custom node template | excluded | serves the containerlab web UI's own state (icons and node templates for its editor), not a lab, a node or the server a runbook acts on | - |
| `DELETE /api/v1/ui/custom-nodes/{name}` | Delete custom node template | excluded | serves the containerlab web UI's own state (icons and node templates for its editor), not a lab, a node or the server a runbook acts on | - |
| `GET /api/v1/ui/icons` | List global custom icons | excluded | serves the containerlab web UI's own state (icons and node templates for its editor), not a lab, a node or the server a runbook acts on | - |
| `POST /api/v1/ui/icons` | Upload global custom icon | excluded | serves the containerlab web UI's own state (icons and node templates for its editor), not a lab, a node or the server a runbook acts on | - |
| `DELETE /api/v1/ui/icons/{iconName}` | Delete global custom icon | excluded | serves the containerlab web UI's own state (icons and node templates for its editor), not a lab, a node or the server a runbook acts on | - |
| `GET /api/v1/users` | List users | unmapped | - | - |
| `POST /api/v1/users` | Create user | unmapped | - | - |
| `GET /api/v1/users/{username}` | Get user details | unmapped | - | - |
| `PUT /api/v1/users/{username}` | Update user | unmapped | - | - |
| `DELETE /api/v1/users/{username}` | Delete user | unmapped | - | - |
| `PUT /api/v1/users/{username}/password` | Change user password | unmapped | - | - |
| `GET /api/v1/version` | Get containerlab version | mapped | [virt.clab.server.version](../modules/virt/clab/server/version.md) | yes |
| `GET /api/v1/version/check` | Check containerlab updates | mapped | [virt.clab.server.version_check](../modules/virt/clab/server/version_check.md) | no |
| `GET /health` | Get API server health | mapped | [virt.clab.server.health](../modules/virt/clab/server/health.md) | yes |
| `POST /login` | Log in | excluded | the session handshake every method makes before its call (pkg/clabapi.Client.Login), with the device's credential; not an operation a task asks for | - |

## Methods and their proof

Per OS: `live` is a test against the real thing, `fixture` one against a stand-in, `-` none.

| Method | Operations | linux | freebsd | darwin | windows |
| --- | --- | --- | --- | --- | --- |
| [virt.clab.capture.packetflix](../modules/virt/clab/capture/packetflix.md) | `POST /api/v1/labs/{labName}/capture/packetflix` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.edgeshark.install](../modules/virt/clab/edgeshark/install.md) | `POST /api/v1/tools/edgeshark/install` | fixture (`TestEdgeshark_InstallAndUninstallActOnlyOnTheStateTheyMove`) | - | - | - |
| [virt.clab.edgeshark.status](../modules/virt/clab/edgeshark/status.md) | `GET /api/v1/tools/edgeshark/status` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.edgeshark.uninstall](../modules/virt/clab/edgeshark/uninstall.md) | `POST /api/v1/tools/edgeshark/uninstall` | fixture (`TestEdgeshark_InstallAndUninstallActOnlyOnTheStateTheyMove`) | - | - | - |
| [virt.clab.image.list](../modules/virt/clab/image/list.md) | `GET /api/v1/images` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.image.pull](../modules/virt/clab/image/pull.md) | `POST /api/v1/images/pull` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.image.remove](../modules/virt/clab/image/remove.md) | `DELETE /api/v1/images` | - | - | - | - |
| [virt.clab.lab.apply](../modules/virt/clab/lab/apply.md) | `POST /api/v1/labs/{labName}/apply` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.lab.deploy](../modules/virt/clab/lab/deploy.md) | `POST /api/v1/labs` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.lab.destroy](../modules/virt/clab/lab/destroy.md) | `DELETE /api/v1/labs/{labName}` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.lab.info](../modules/virt/clab/lab/info.md) | `GET /api/v1/labs/{labName}` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.lab.interfaces](../modules/virt/clab/lab/interfaces.md) | `GET /api/v1/labs/{labName}/interfaces` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.lab.links](../modules/virt/clab/lab/links.md) | `GET /api/v1/labs/{labName}/links` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.lab.list](../modules/virt/clab/lab/list.md) | `GET /api/v1/labs` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.lab.redeploy](../modules/virt/clab/lab/redeploy.md) | `PUT /api/v1/labs/{labName}` | - | - | - | - |
| [virt.clab.lab.restart](../modules/virt/clab/lab/restart.md) | `POST /api/v1/labs/{labName}/restart` | - | - | - | - |
| [virt.clab.lab.save](../modules/virt/clab/lab/save.md) | `POST /api/v1/labs/{labName}/save` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.lab.start](../modules/virt/clab/lab/start.md) | `POST /api/v1/labs/{labName}/start` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.lab.stop](../modules/virt/clab/lab/stop.md) | `POST /api/v1/labs/{labName}/stop` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.lab.topology](../modules/virt/clab/lab/topology.md) | `GET /api/v1/labs/{labName}/topology/yaml` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.lab.validate](../modules/virt/clab/lab/validate.md) | `POST /api/v1/labs/{labName}/validate` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.netem.reset](../modules/virt/clab/netem/reset.md) | `POST /api/v1/tools/netem/reset` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.netem.set](../modules/virt/clab/netem/set.md) | `POST /api/v1/tools/netem/set` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.netem.show](../modules/virt/clab/netem/show.md) | `GET /api/v1/tools/netem/show` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.node.disable_tx_offload](../modules/virt/clab/node/disable_tx_offload.md) | `POST /api/v1/tools/disable-tx-offload` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.node.exec](../modules/virt/clab/node/exec.md) | `POST /api/v1/labs/{labName}/exec` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.node.logs](../modules/virt/clab/node/logs.md) | `GET /api/v1/labs/{labName}/nodes/{nodeName}/logs` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.node.pause](../modules/virt/clab/node/pause.md) | `POST /api/v1/labs/{labName}/nodes/{nodeName}/pause` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.node.ports](../modules/virt/clab/node/ports.md) | `GET /api/v1/labs/{labName}/nodes/{nodeName}/browser-ports` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.node.restart](../modules/virt/clab/node/restart.md) | `POST /api/v1/labs/{labName}/nodes/{nodeName}/restart` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.node.ssh_access](../modules/virt/clab/node/ssh_access.md) | `POST /api/v1/labs/{labName}/nodes/{nodeName}/ssh` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.node.start](../modules/virt/clab/node/start.md) | `POST /api/v1/labs/{labName}/nodes/{nodeName}/start` | fixture (`TestNodeState_ActsOnlyOnTheStateItMovesAndRecordsTheExactUndo`) | - | - | - |
| [virt.clab.node.stop](../modules/virt/clab/node/stop.md) | `POST /api/v1/labs/{labName}/nodes/{nodeName}/stop` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.node.unpause](../modules/virt/clab/node/unpause.md) | `POST /api/v1/labs/{labName}/nodes/{nodeName}/unpause` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.server.health](../modules/virt/clab/server/health.md) | `GET /health` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.server.metrics](../modules/virt/clab/server/metrics.md) | `GET /api/v1/health/metrics` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.server.version](../modules/virt/clab/server/version.md) | `GET /api/v1/version` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.server.version_check](../modules/virt/clab/server/version_check.md) | `GET /api/v1/version/check` | - | - | - | - |
| [virt.clab.ssh_session.list](../modules/virt/clab/ssh_session/list.md) | `GET /api/v1/ssh/sessions` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.ssh_session.terminate](../modules/virt/clab/ssh_session/terminate.md) | `DELETE /api/v1/ssh/sessions/{port}` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.topology.generate](../modules/virt/clab/topology/generate.md) | `POST /api/v1/generate` | live (`TestLive_ARealLabEndToEnd`) | - | - | - |
| [virt.clab.veth.create](../modules/virt/clab/veth/create.md) | `POST /api/v1/tools/veth` | - | - | - | - |
| [virt.clab.vxlan.create](../modules/virt/clab/vxlan/create.md) | `POST /api/v1/tools/vxlan` | - | - | - | - |
| [virt.clab.vxlan.delete](../modules/virt/clab/vxlan/delete.md) | `DELETE /api/v1/tools/vxlan` | - | - | - | - |
