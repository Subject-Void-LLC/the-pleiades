`make pr-check` starts every other machine's check at once, before the fast pass: the Windows ARM VM, the Mac (its
prepare phase, govulncheck, then its tests and its Podman container half together) and the FreeBSD then Windows lab
VMs. Each local pass prints `pr-check: the <name> pass begins`, and the monitor reads it, so a local pass shows as
running while a machine check runs beside it, and follows the Mac's container half in `containers-mac.log`.
