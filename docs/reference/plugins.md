---
status: beta
---

# Sync plugins

Every registered inventory sync plugin, read from the live registry `pleiades inventory sync` resolves `--plugin` against. Run `pleiades inventory plugins` for the same list from a live binary. A `declared` plugin is registered but its methods return a not-implemented error; only an `implemented` plugin has run against its real upstream system.

| Name | Description | Read-only | Status | Needs | Origin |
| --- | --- | --- | --- | --- | --- |
| `aws` | reads EC2 instances from an AWS account/region | yes | `implemented` | a credential; `--set region=...` (required, the AWS region to read EC2 instances from, for example us-east-1) | generated |
| `catalyst_center` | reads managed network devices from a Cisco Catalyst Center | yes | `implemented` | a credential | generated |
| `static_yaml` | reads a static hosts.yaml inventory file | no | `implemented` | nothing beyond `--endpoint` | hand-written, predates the Forge |
| `vbox` | reads the VMs a VirtualBox host runs, over WinRM, as the devices that reach their guests | yes | `implemented` | a credential; `--set ca_file=...` (optional, a PEM file with the authority the host's WinRM certificate is checked against, when the system does not trust it); `--set server_name=...` (optional, the name the host's WinRM certificate is checked against, when it does not name the endpoint's host); `--set vboxmanage=...` (optional, where VBoxManage is on the host; default C:\Program Files\Oracle\VirtualBox\VBoxManage.exe) | hand-written |

4 sync plugins registered.

The **Needs** column is what a run has to supply beyond `--plugin`. A credential is resolved from the project credential store under the plugin's own name unless `--credential` names another. Everything else is a declared setting, passed as `--set key=value`, and `pleiades inventory sync` refuses to start without a required one rather than failing partway through a connection.
