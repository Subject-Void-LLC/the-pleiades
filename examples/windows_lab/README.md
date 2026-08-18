# Windows lab: a real Windows Server target over WinRM

This directory is the worked example for the WinRM transport: a real Windows Server 2025
host, reached by the real `pleiades` CLI over real WinRM with NTLM and SPNEGO message
encryption. Like `examples/webserver_lab/`, it is documentation infrastructure rather
than part of the shipped product.

```
examples/windows_lab/
  pleiades/
    inventory.yaml          the lab host, twice: once over IPv4 and once over IPv6
    runbooks/
      win_facts.yaml        read-only: identify the host and its network state
      win_static_ip.yaml    convert an adapter from DHCP to a static address
      win_verify_ip.yaml    check the change from a connection opened afterward
      win_revert.yaml       put it back on DHCP, over IPv6, when IPv4 is gone
  captures/                 real captured output from real runs against this host
```

## Why an address change, of all the things a Windows box can do

Because it is the hardest shape of remote change there is, and everything easier is
covered by the container gates. Reconfiguring an adapter's address destroys the WinRM
connection carrying the instruction, so the command's own success cannot be reported
over the channel it just tore down. A runbook that only ran `hostname` would prove the
transport connects and nothing about whether this platform can perform a change that
costs it its own transport.

It is also the example that broke this lab machine twice, and the comments in
`win_static_ip.yaml` are what those outages bought.

## What actually goes wrong, in the order you will hit it

**`shell: powershell` is required, not decorative.** The WinRM transport refuses
`shell: none`. That mode means running a program directly with an argument vector
nothing parses, and the WS-Man option deciding between direct execution and `cmd.exe`
is pinned to `cmd.exe` by the underlying library, so the transport will not claim a
command runs verbatim when it would actually be parsed by a shell. `shell: cmd` is the
other supported value.

**Run these with `--verbose`.** `pleiades run` prints only whether each task changed
something unless you ask for more, and every runbook here exists to read state back off
the device, so `pleiades run runbooks/win_facts.yaml --verbose` is the form to use.

**Windows re-identifies the network as Public.** Changing the address makes Network
Location Awareness treat the result as a new network, and with no domain controller it
classifies it Public. `WINRM-HTTP-In-TCP` covers Domain and Private only, so WinRM stops
answering unless `WINRM-HTTP-In-TCP-PUBLIC` is enabled too. `win_facts.yaml` reports
both before you change anything.

**Never convert to the address the adapter already holds by DHCP.** This is the one that
actually cost the machine. `netsh ... set address ... static <same-ip>` disables DHCP and
then binds the address, and when that address is the one the interface currently holds
from its own lease, the bind can fail after the DHCP disable has already taken. What
remains is an adapter with DHCP off and no address, falling back to APIPA
(`169.254.x.x`): unreachable on IPv4, change half applied.

It does not fail every time, which is worse than failing every time. The first
conversion in a session tends to work. One run immediately after reverting to DHCP tends
not to, because the lease has just been reissued.

**IPv6 is the way back in.** A Windows host configures IPv4 and IPv6 independently and
WinRM listens on both, so a host that has lost IPv4 usually still answers on 5985 over
IPv6. That is why `inventory.yaml` carries the same machine twice and why
`win_revert.yaml` targets the IPv6 entry. It is the difference between a runbook fix and
a trip to the console. Note the square brackets around the address, which the URL the
transport builds requires.

## Running it

```bash
pleiades run runbooks/win_facts.yaml --verbose      # always start here
pleiades run runbooks/win_static_ip.yaml           # expect a transport error even on success
pleiades run runbooks/win_verify_ip.yaml --verbose # the real proof, on a fresh connection
pleiades run runbooks/win_revert.yaml              # over IPv6, when IPv4 is gone
```

Point this at a lab machine you can reach at a console, and take a snapshot first. Both
outages in this lab's history ended with a snapshot rollback.

## The automated version

`cmd/pleiades/winrm_static_ip_release_gate_test.go` is this example as a Release Gate. It
skips unless `PLEIADES_WINRM_HOST` and friends are set, refuses to convert unless the
Public WinRM firewall rule is enabled, and registers the revert as cleanup *before* the
change so the adapter is restored even if an assertion fails or the test panics.

It converts exactly once per run, for the reason above: back-to-back conversions against
the same address are what break this.
