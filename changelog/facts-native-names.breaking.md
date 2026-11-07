Fact names are native, dotted and typed. `facts.gather` reports `os.distribution` (the system's own lowercase
identifier, such as `ubuntu`, `rhel` or `windows`), `os.family`, `os.version`, `os.version_major` (an integer, absent
when the version has none), `os.codename`, `os.edition` (Windows), `os.kernel.name`, `os.kernel.release`, `host.name`,
`host.nodename`, `host.uptime_seconds`, `hardware.architecture`, `hardware.memory_mb`, `hardware.swap_mb` and
`hardware.cpu.sockets`, `.cores` and `.threads`, where 0.3.0 reported `ansible_distribution` and the rest;
`net.ios.facts` reports `host.name`, `os.distribution` (`ios` or `ios_xe`), `os.version`, `os.image`, `hardware.model`,
`hardware.serial`, `host.uptime` and `network.interfaces` where it reported `ansible_net_*`; and the Catalyst methods'
facts are `catalyst.*`. A registered result has the same names, read by index (`result.r1["os.distribution"]`).
Ansible's names are no longer stored; `pleiades forge migrate-playbook` flags a condition reading one for review, and
the migration guide's table gives the native name. Mapped back to Ansible's names, a distribution whose ID nobody has
measured against Ansible has no `ansible_distribution`, and a CentOS 8 has no release. Facts stored under the old names
stay as history and are not updated; delete `.pleiades/facts` in a project to drop them.

The result fields renamed, as the schema check names them:

- `return-removed module-catalog.json#/facts.gather/returns/ansible_architecture`
- `return-removed module-catalog.json#/facts.gather/returns/ansible_distribution`
- `return-removed module-catalog.json#/facts.gather/returns/ansible_distribution_major_version`
- `return-removed module-catalog.json#/facts.gather/returns/ansible_distribution_release`
- `return-removed module-catalog.json#/facts.gather/returns/ansible_distribution_version`
- `return-removed module-catalog.json#/facts.gather/returns/ansible_hostname`
- `return-removed module-catalog.json#/facts.gather/returns/ansible_kernel`
- `return-removed module-catalog.json#/facts.gather/returns/ansible_memtotal_mb`
- `return-removed module-catalog.json#/facts.gather/returns/ansible_nodename`
- `return-removed module-catalog.json#/facts.gather/returns/ansible_processor_cores`
- `return-removed module-catalog.json#/facts.gather/returns/ansible_processor_count`
- `return-removed module-catalog.json#/facts.gather/returns/ansible_processor_vcpus`
- `return-removed module-catalog.json#/facts.gather/returns/ansible_swaptotal_mb`
- `return-removed module-catalog.json#/facts.gather/returns/ansible_system`
- `return-removed module-catalog.json#/facts.gather/returns/ansible_uptime_seconds`
- `return-removed module-catalog.json#/net.catalyst.device_facts/returns/device_count`
- `return-removed module-catalog.json#/net.catalyst.device_facts/returns/devices`
- `return-removed module-catalog.json#/net.catalyst.reachability/returns/managed_count`
- `return-removed module-catalog.json#/net.catalyst.reachability/returns/reachability`
- `return-removed module-catalog.json#/net.catalyst.reachability/returns/reachable_count`
- `return-removed module-catalog.json#/net.catalyst.reachability/returns/total_device_count`
- `return-removed module-catalog.json#/net.catalyst.reachability/returns/unreachable`
- `return-removed module-catalog.json#/net.catalyst.site_facts/returns/site_count`
- `return-removed module-catalog.json#/net.catalyst.site_facts/returns/sites`
- `return-removed module-catalog.json#/net.catalyst.tag_facts/returns/operator_tags`
- `return-removed module-catalog.json#/net.catalyst.tag_facts/returns/tags`
- `return-removed module-catalog.json#/net.ios.facts/returns/ansible_net_hostname`
- `return-removed module-catalog.json#/net.ios.facts/returns/ansible_net_image`
- `return-removed module-catalog.json#/net.ios.facts/returns/ansible_net_interfaces`
- `return-removed module-catalog.json#/net.ios.facts/returns/ansible_net_model`
- `return-removed module-catalog.json#/net.ios.facts/returns/ansible_net_serialnum`
- `return-removed module-catalog.json#/net.ios.facts/returns/ansible_net_uptime`
- `return-removed module-catalog.json#/net.ios.facts/returns/ansible_net_version`
