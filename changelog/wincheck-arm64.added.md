`go run ./tools/wincheck -arm64` runs the branch's packages on a Windows 11 ARM VM on a Mac running VMware Fusion,
named in `tests/winarm/.env` (see `tests/winarm/.env.example`). It starts the VM through `virt.fusion.vm.start` and
suspends it afterwards. `make pr-check` runs it as the `winarm` leg after the Mac's own checks whenever that file names
a machine, and the gate monitor shows every OS leg.
