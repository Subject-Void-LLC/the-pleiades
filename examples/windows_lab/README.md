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

**Name the shell.** These runbooks use `shell: powershell`, because what they read and
change is PowerShell's to do. The other two modes exist too: `shell: cmd` runs one line
through `cmd.exe` for its builtins, and `shell: none` runs a program with its arguments
reaching it exactly as written. The WinRM service starts every command through `cmd.exe`
whatever it is asked, so Pleiades escapes each line until that `cmd.exe` passes it through
unchanged, and the parser a task names is the only one that acts on it.

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

## The certificate lab account

`winrm-cert-setup.ps1` sets this host up for certificate authentication on 5986, for one
account that can do only what a lab run needs. It is a standard user with a random
password that is used once, to map the certificate, and never shown; it reaches WinRM
through its own RootSDDL entry, not a group; it cannot log on at the console, over
Remote Desktop, or as a batch job or service; and on every fixed drive except the
system drive it is denied everything but the folders named with `-ReadPath` and
`-WritePath`. With `-AllowVirtualBox` it may also start VirtualBox's two COM servers,
VBoxSVC and VBoxSDS, which `VBoxManage` needs: Windows' default launch permission admits
only administrators, SYSTEM and interactive logons, and a WinRM logon is a network one, so
without the grant `VBoxManage list vms` fails with `E_ACCESSDENIED`. The grant is local
launch and local activation, on those two AppIDs only, for this account's SID only; an
AppID with no launch permission of its own keeps the machine default's entries beside it.
The same switch sets the machine-wide policy "do not forcefully unload the user registry at
user logoff" (`DisableForceUnload`): the account's registry is unloaded when its last WinRM
shell closes, and the VBoxSVC a running VM keeps alive would then fail every later call
with `REGDB_E_READREGDB`. The teardown restores the policy's earlier value.

**A VM does not yet start from this account.** VirtualBox's hardening verifies Windows'
own DLLs, and Windows' catalog signature check fails for a non-interactive, non-admin
logon (`VERR_LDRVI_NOT_SIGNED` for `WinHvPlatform.dll`; VirtualBox ticket 20341). Query
access on Cryptographic Services was tried and does not help.
`-VirtualBoxAutostart` is the one way VirtualBox offers around that, and it works: its own autostart
service (`VBoxAutostartSvc`), installed for this account so a VM starts under a service
logon rather than a network one. It lifts only the account's service-logon denial, lets the
service manager keep the account's password for that service, sets the machine variable
`VBOXAUTOSTART_CONFIG` to an allow policy (VirtualBox's policy file cannot name an account:
its parser takes no `\`, `@` or `-` in a key), and lets the account start
and query that one service. The teardown removes all of it. Measured on the lab host, a VM
marked `--autostart-enabled` starts when the account starts that service, and from then on,
while any of the account's VMs runs, a plain `VBoxManage startvm` over WinRM works too: every
WinRM client is handed the VirtualBox server the service started, and VirtualBox launches a
VM as that server.
The certificate authority's private key is deleted once the server and
client certificates exist, and the client's once it is exported, so nothing on the host
can issue a certificate the host trusts. What it granted is recorded in
`lab-state.json`, which `winrm-cert-teardown.ps1` reads to revoke exactly that.

```powershell
# elevated; the deny on the other drives writes into every file there, once
powershell -ExecutionPolicy Bypass -File .\winrm-cert-setup.ps1 -ReadPath G:\iso -WritePath G:\PleiadesLab -AllowVirtualBox
powershell -ExecutionPolicy Bypass -File .\winrm-cert-teardown.ps1
```

What Pleiades needs lands in `%USERPROFILE%\pleiades-gate`, readable only by you: the
authority as `ca.pem`, and the client identity as `client.pfx` and
`client.pfx.passphrase`. From that directory, add the host with its authority pinned, so it
is verified without adding the lab's CA to anything else's trust, then import the identity
and delete both of its files:

```bash
pleiades add-host win-lab --type windows_server --set host=<address> --set port=5986 \
  --set "tls_ca_pem=$(cat ca.pem)"
pleiades add-credential win-lab --pfx client.pfx --passphrase-stdin < client.pfx.passphrase
```

Each run issues a new authority and client certificate, so after a re-run, pin the new
authority with `pleiades set-host win-lab --set "tls_ca_pem=$(cat ca.pem)"` and import the
new bundle with the same `add-credential` line, which replaces the stored one.

A setup run from before `ca.pem` existed wrote only `ca.cer`; `certutil -encode ca.cer ca.pem`
converts it.

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
