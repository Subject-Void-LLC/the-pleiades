These files are verbatim output of VMware Fusion 26.0.1 (vmrun 1.17.0.25689522) on an Apple Silicon Mac,
captured 2026-10-26 with `vmrun -T fusion` and `vmcli` against throwaway VMs. The parsers and the fake host
are held to them. `start_noise.txt` is `vmrun start`'s real diagnostics followed by a real `list` answer,
joined to show a header that is not on the first line.

Behaviours the captures showed, which the methods rely on (Fusion 26.0.1, 2026-10-26):
- every failure is `Error: <message>` with exit status 255; `-T bogus` is accepted without complaint;
- `stop soft`, `reset soft` and `getGuestIPAddress` on a running guest with no VMware Tools fail with
  "The VMware Tools are not running in the virtual machine", so a soft power change needs the tools;
- `start` on a suspended VM resumes it; `stop`, `suspend` and the guest commands on a VM that is off fail with
  "The virtual machine is not powered on";
- `vmrun list` reports a VM's real path (`/private/tmp/...` for a VM given as `/tmp/...`);
- `deleteVM` removes the vmx but leaves the .vmdk files, including the disk `vmcli VM Create` made;
- `vmrun start` prints "LocationGetRoot: Unable to determine the product location" to stdout when Fusion has
  never been opened, and still exits 0;
- `listSnapshots ... showTree` indents a child snapshot with a tab; without it the list is flat.
