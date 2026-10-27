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
- `vmrun start` (and `reset`) fork `vmware-vmx`, which inherits vmrun's standard output and holds it for the VM's
  life: read through a pipe, the output never ends. A start piped into grep hung for thirteen minutes. Start and
  reset therefore run through `detachScript`, which sends the output to a file first.
- A UEFI Windows install disc asks "press any key to boot from CD" for about five seconds after power-on;
  unanswered, the VM falls through to the firmware's Boot Manager. `vmcli MKS sendKeySequence a` every half second
  for 25 seconds after a hard reset answered it.
- After a resume from suspend, `getGuestIPAddress` kept saying "The VMware Tools are not running" for minutes while
  `checkToolsState` printed `running` and `getGuestIPAddress -wait` printed the address at once (2026-10-27).
- A `vmcli` process killed while it held the VM left `<name>.vmx.lck/M<pid>.lck` behind; every later start failed with
  "The file is already in use" although no VMware process was running. Removing that lock, once its process was
  confirmed gone, let the VM start.
