`virt.fusion.vm.info`, `virt.fusion.vm.start` and `virt.fusion.vm.stop` read, start and stop or suspend a VM on a Mac
running VMware Fusion, over the Mac's own SSH connection, through `vmrun` and `vmcli` (`pkg/vmrun`). Onboarding grants
`VMwareFusionCapable` to a `generic_ssh` Mac only when Fusion's `vmrun` answers as the connecting account. A start holds
the Mac awake for as long as the VM's process runs, because macOS throttles a VM started from an SSH session, and reads
`vmrun`'s output from a file, because the VM process keeps that output open for its whole life.
