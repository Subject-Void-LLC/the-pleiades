# VirtualBox lab: Ubuntu VMs The Pleiades makes, seeds and manages

This directory is the worked example for the `virt.vbox.*` Collection: a Windows host running
Oracle VirtualBox, and Ubuntu VMs on it that The Pleiades downloads, creates, gives a login,
boots, trusts and then manages over SSH. Every step is a `pleiades` command or runbook. Nothing
is done by hand in the VirtualBox Manager, and nothing on the host is scripted outside The
Pleiades. Like the other labs here, it is documentation infrastructure rather than part of the
shipped product.

It was built and run on a Windows 11 Home desktop with VirtualBox 7.2.20, driven from WSL 2.
The output shown below is from those runs, abridged to the lines that matter.

```
examples/virtualbox_lab/
  pleiades/
    inventory.yaml          the VirtualBox host and the VM, as the steps below build them
    runbooks/
      01-media.yaml         fetch Ubuntu's cloud image onto the host, checked by SHA-256
      02-base.yaml          import it as a base VM that is never booted, and snapshot it
      03-create.yaml        clone the lab VM from that snapshot, boot it, read its host keys
      04-bootstrap.yaml     the first run against the VM itself, over SSH
      list.yaml             every VM on the host, with the address each was given
      teardown.yaml         power the lab VM off and delete it
```

## What you end up with

```
  WSL 2 (pleiades)                     Windows host (VirtualBox)
  ----------------                     -------------------------
  pleiades run  ---- WinRM, cert ----> pleiades-gate account
      |                                  VBoxManage, autostart service
      |                                  G:\PleiadesLab\
      |                                    ubuntu-2404-base  (never booted, snapshot "base")
      |                                    ubuntu-lab        (linked clone, running)
      |                                                           NIC 1: NAT (internet)
      +------------- SSH, key --------------------------------->  NIC 2: host-only
                                                                   192.168.56.10
```

- **The host** is an ordinary `windows_server` device that says it runs VirtualBox. The
  Pleiades reaches it over WinRM with a client certificate, as `examples/windows_lab` sets
  up, and runs VBoxManage there.
- **The base** is Ubuntu 24.04's official cloud image, imported once and snapshotted before it
  ever boots.
- **The lab VM** is a linked clone of that snapshot: a small disk of its own on top of the
  base's, made in seconds. It is also an inventory device (`linux_server`), and The Pleiades
  manages it like any other Linux host once it is up.

A full rebuild of the lab VM, from `03-create.yaml` to cloud-init finishing, took 38 to 50
seconds on this host.

## Before you start

1. **VirtualBox 7.x on the Windows host.** The installer makes the host-only adapter
   `VirtualBox Host-Only Ethernet Adapter` at `192.168.56.1`, with a DHCP server handing out
   `.101` to `.254`. The lab VM takes a fixed address below that range. The Extension Pack is
   not needed.
2. **The host set up for The Pleiades.** Run `examples/windows_lab/winrm-cert-setup.ps1` as
   its README describes, with `-AllowVirtualBox`, `-VirtualBoxAutostart`, and write access to
   the folder the VMs will live in:

   ```powershell
   powershell -ExecutionPolicy Bypass -File .\winrm-cert-setup.ps1 `
       -ReadPath G:\iso -WritePath G:\PleiadesLab -AllowVirtualBox -VirtualBoxAutostart
   ```

   It creates the least-privilege account The Pleiades logs in as, grants it what VirtualBox
   needs from a WinRM logon, and prints the `add-host`, `set-host` and `add-credential`
   commands for this project. `docs/10-running-in-production.md` ("A Windows host that runs
   VirtualBox") explains each grant.
3. **The host in your inventory, marked as a VirtualBox host:**

   ```bash
   pleiades set-host vengeance --set virtualbox=true --set 'vm_folder=G:\PleiadesLab'
   ```

4. **WSL 2 must reach the host-only network.** With WSL's default NAT networking it does on
   this host: the lab VM at `192.168.56.10` answers SSH from WSL.

## The walkthrough

Run these from your project directory. The runbooks here assume the host is called
`vengeance`, the VM folder is `G:\PleiadesLab`, and the VM gets `192.168.56.10`; change them to
suit.

### 1. Fetch the image

```bash
pleiades run runbooks/01-media.yaml -v
```

```
  tasks[0] [...]: changed
    diff:
      after:
        sha256: 513a22ebe3982b9387b038f8a2a6dad1af980d4623dca03511312e55ba620b1a
      before:
        sha256: ""
    path: G:\PleiadesLab\ubuntu-24.04-server-cloudimg-amd64.ova
    sha256: 513a22ebe3982b9387b038f8a2a6dad1af980d4623dca03511312e55ba620b1a
    size_bytes: 594800640
```

`win.file.download` has the host fetch the file itself, with the `curl.exe` Windows ships,
over HTTPS only (redirects included). It keeps the file only if its SHA-256 matches, so nothing
passes through the machine running The Pleiades. The digest comes from the release's
`SHA256SUMS` on `cloud-images.ubuntu.com`. The runbook pins a dated release
(`release-20260926`) rather than `release/`, which moves. Run it again and it reports `ok`
without downloading anything.

### 2. Import the base and snapshot it

```bash
pleiades run runbooks/02-base.yaml
```

`virt.vbox.vm.import_ova` imports the image as `ubuntu-2404-base` into the host's
`vm_folder`, then removes the network adapter the image asks for. Ubuntu's image asks for a
**bridged** one, which would put the VM on your home network. `virt.vbox.snapshot.take` then
keeps the never-booted disk as the snapshot `base`. The base is never started. Every lab VM is
cloned from that snapshot, so each one starts from a disk no boot has touched.

### 3. Give the VM an inventory entry and a login

```bash
pleiades add-host ubuntu-lab --type linux_server --set host=192.168.56.10 --tags lab
pleiades add-credential ubuntu-lab --username root --generate
```

```
stored a generated credential for device "ubuntu-lab": an ed25519 key and a 24-character password, for root
public key: ssh-ed25519 AAAAC3NzaC1lZDI1NTE5... root@ubuntu-lab
```

`--generate` makes a random ed25519 key and a random 24-character password and stores both
encrypted in the project's vault (`.pleiades/credentials.yaml`). No person chooses, types or
sees either one; only the key's public half is printed. It refuses to replace a credential the
device already has unless you add `--replace`, since the machine that credential reaches would
then be locked out.

The login is `root` because The Pleiades has no `become` yet: a method that needs root must log
in as root. Any other user name gets passwordless `sudo` instead.

### 4. Create and boot the VM

```bash
pleiades run runbooks/03-create.yaml -v
```

```
  tasks[0] [...]: changed
    address: 192.168.56.10
    uuid: 493a71a2-27aa-41f5-a436-4647fb3cbada
  tasks[1] [...]: changed
    state: running
    via_autostart_service: true
  tasks[2] [...]: ok
    ssh_host_keys:
      - ecdsa-sha2-nistp256 AAAAE2VjZHNh...
      - ssh-ed25519 AAAAC3NzaC1lZDI1NTE5...
      - ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAAB...
```

Three tasks do it:

- **`virt.vbox.vm.clone`** makes `ubuntu-lab` as a linked clone of `ubuntu-2404-base`'s
  snapshot `base`, and gives it:
  - 2048 MB and one CPU;
  - a NAT adapter for the internet, and a host-only adapter at `192.168.56.10`;
  - a serial console written to `console.log` in its folder;
  - a cloud-init seed on its DVD drive (next section).
- **`virt.vbox.vm.start`** boots it headless. When none of the account's VMs is running, the
  start goes through the account's VirtualBox autostart service, because Windows refuses to
  start a VM from a WinRM logon ("Starting a VM from a WinRM logon", below).
- **`virt.vbox.vm.host_keys`** waits for cloud-init's first boot to print the VM's SSH host
  keys on its serial console, and reads them from `console.log` over the WinRM connection.

A check (`--mode check`) of this runbook reports the clone as a change and names the other two
tasks as unchecked, since the VM they act on does not exist until the clone runs.

### 5. Trust the VM's host keys

```bash
pleiades trust-host ubuntu-lab --from-console vengeance
```

```
192.168.56.10 ecdsa-sha2-nistp256 SHA256:LpnHFPL6PPWQGpmSaLgpn86+SniH0XY+ithvwz0OXJk
192.168.56.10 ssh-ed25519 SHA256:joTSGdLSq01oOFPDm0QGhWj0Pdf6eqU4ZrvxF45B2d8
192.168.56.10 ssh-rsa SHA256:Qve+Ma/xa9FKBJnseI6gyD/cFqdGWjgmr7aM2lPnJm0
/home/you/.ssh/known_hosts: 3 key(s) added, 0 already trusted, 0 replaced
```

The Pleiades refuses an SSH host it has no key for. `trust-host --from-console` runs
`virt.vbox.vm.host_keys` against the VirtualBox host and writes the keys it reads into the
known_hosts file every SSH connection verifies against. That is `PLEIADES_KNOWN_HOSTS` if you
set it, otherwise `~/.ssh/known_hosts`. See "Trusting the host key" below for why this is
the safe way.

### 6. Manage it

```bash
pleiades run runbooks/04-bootstrap.yaml -v
```

```
  tasks[0] [...]: ok
    reply: pong
  tasks[1] [...]: changed
    stdout:
      uid=0(root) gid=0(root) groups=0(root)
      ubuntu-lab
      Ubuntu 24.04.5 LTS
      lo               UNKNOWN        127.0.0.1/8
      enp0s3           UP             10.0.2.15/24 metric 100
      enp0s8           UP             192.168.56.10/24
      status: done
  tasks[2] [...]: ok
    ansible_distribution: Ubuntu
    ansible_distribution_version: 24.04
    ansible_hostname: ubuntu-lab
```

From here `ubuntu-lab` is an ordinary Linux device: `pkg.*`, `svc.*`, `file.*` and the rest
work on it as on any other.

### 7. See what is on the host

```bash
pleiades run runbooks/list.yaml -v
```

```
    vms:
      - autostart_enabled: false
        cpus: 2
        memory_mb: 1024
        name: ubuntu-2404-base
        state: poweroff
        uuid: 58af4b55-8f5b-4126-b11d-3fff38f2b9ca
      - address: 192.168.56.10
        autostart_enabled: true
        cpus: 1
        device: ubuntu-lab
        memory_mb: 2048
        name: ubuntu-lab
        state: running
        uuid: 493a71a2-27aa-41f5-a436-4647fb3cbada
```

This is where the lab VMs are visible: your own VirtualBox Manager will not list them (next
section). `address` and `device` come from what `virt.vbox.vm.clone` recorded on the VM.
VirtualBox cannot know a guest's address without its Guest Additions, which the cloud image
does not have.

### 8. Start over

```bash
pleiades run runbooks/teardown.yaml
pleiades run runbooks/03-create.yaml
pleiades trust-host ubuntu-lab --from-console vengeance --replace
```

`teardown.yaml` cuts the VM's power and deletes it: its disk, its seed and its console log,
and its folder once that is empty. The base and its snapshot stay, so `03-create.yaml` makes a
fresh VM from the same never-booted disk. The new VM has new host keys, so `trust-host`
refuses them until you say `--replace`. Keys changing under a name is also what a machine in
the middle looks like, so it is never accepted silently.

The vault credential stays, so the new VM gets the same login. To also make a new login, run
`pleiades add-credential ubuntu-lab --username root --generate --replace` before
`03-create.yaml`.

## How it works

### Two accounts, two lists of VMs

VirtualBox keeps a separate list of VMs for each Windows account, each served by that
account's own `VBoxSVC` process. The Pleiades works as the `pleiades-gate` account the setup
script made, so the lab VMs are registered to it:

- **Your own VirtualBox Manager does not show them.** `virt.vbox.vm.list` does. Do not add
  their `.vbox` files to your own VirtualBox Manager: two VirtualBox servers would then manage
  the same VM and its disks, which can corrupt them.
- **The account does not appear under Settings > Accounts.** The setup script hides it from
  the sign-in screen, which hides it there too, and Windows 11 Home has no "Local Users and
  Groups". `Get-LocalUser pleiades-gate` in PowerShell shows it.
- **The VMs run headless, with no window.** To see a VM's console from Windows, read
  `G:\PleiadesLab\<vm>\console.log`.

### The VM's login: generated, vaulted, seeded

The private key and the password never leave the vault. What the VM needs is only what lets
that key and that password in:

| What | Where it lives | Where it goes |
|---|---|---|
| Private key | the vault (encrypted) | used by `pleiades run` to log in; nowhere else |
| Password | the vault (encrypted) | nowhere; it is for the VM's console |
| Public key | derived when needed | the VM's cloud-init seed |
| Password hash (SHA-512 crypt, fresh salt) | derived when needed | the VM's cloud-init seed |

`virt.vbox.vm.clone` names the device whose login it seeds (`login: ubuntu-lab`). The engine
reads that device's credential from the vault and hands the method only the user name, the
public key and a freshly salted hash of the password. The method never holds the key or the
password.

The seed configures the VM so that:
- SSH takes no password from anyone (`PasswordAuthentication no`);
- root may log in by key only (`PermitRootLogin without-password`);
- the password works at the console;
- no default `ubuntu` user is created.

This path exists in the `pleiades` CLI only for now. The Controller and Runner refuse a method
that seeds a login rather than running it without one.

### The seed: rendered by The Pleiades, delivered on stdin

cloud-init's NoCloud source looks for a filesystem labelled `cidata` holding `user-data`,
`meta-data` and `network-config`. The Pleiades renders all three from typed values (the login,
the host name, each adapter matched by its MAC address) and builds the ISO 9660 image itself.
It sends the image to the host on the WinRM command's standard input, never on a command line,
where the host checks its SHA-256 before writing it next to the VM and attaching it as a DVD.
The host needs no tool to build a seed and only ever receives finished bytes.

cloud-init on the VM reports reading it:

```
Cloud-init v. 26.1-0ubuntu1~24.04.1 finished at ... Datasource DataSourceNoCloud [seed=/dev/sr0]
```

The instance ID is the VM's UUID, so a VM made again runs its first boot again.

### Starting a VM from a WinRM logon

Windows' catalog signature check fails for a non-administrator logon that is not interactive,
which is what WinRM gives, and VirtualBox's hardening then refuses to start any VM
(`VERR_LDRVI_NOT_SIGNED`, VirtualBox ticket 20341). So when none of the account's VMs is
running, `virt.vbox.vm.start` does four things:
1. marks the VM for autostart;
2. lets the account's own VirtualBox server exit;
3. starts the account's VirtualBox autostart service, which starts marked VMs under a service
   logon, where the check passes;
4. waits for the service to finish.

While any of the account's VMs runs, a plain start works, and `via_autostart_service` says
which way a start went. The autostart mark stays on while the VM runs, since VirtualBox will
not change a running VM's settings; `virt.vbox.vm.stop` clears it.

### Trusting the host key

A VM makes new SSH host keys on its first boot, so nothing knows them in advance. Two ways to
learn them:

- **`--from-console <host>` (use this one).** cloud-init prints the keys on the VM's serial
  console, VirtualBox writes that console to a file, and The Pleiades reads the file over the
  WinRM connection it already authenticates with a pinned certificate authority. The keys come
  from the VM itself, through the hypervisor, not from whatever answers on the network.
- **`--first-connect`.** Takes whatever answers at the device's address, as `ssh-keyscan`
  would, and warns on every use. Anything on that network at that moment could have answered.
  It is there for a device whose keys cannot be learned any other way.

## Limits and troubleshooting

**Keep the VM at one CPU on this kind of host.** Where WSL 2 or Docker Desktop is installed,
Windows' own hypervisor is running, and VirtualBox runs guests on top of it. On this host, a
two-CPU clone completed its first boot once in three tries. The other two times the guest hung
four seconds in, at `Begin: Loading essential drivers ...` in its initramfs, with one host core
busy. That point is where the kernel times its RAID checksum routines against the guest's
timer, and VirtualBox's log for the VM warned that the timer mode it wanted was not available
on this host. A one-CPU clone booted every time. `03-create.yaml` asks for one CPU, as does
`virt.vbox.vm.clone`'s default.

**`host_keys` times out.** Read `G:\PleiadesLab\<vm>\console.log` to see where the boot stopped.
The VM's own VirtualBox log is `G:\PleiadesLab\<vm>\Logs\VBox.log`.

**`clone` fails partway.** A clone that fails after VirtualBox made the VM deletes it again, so
a later run makes it afresh rather than finding a half-made VM and calling it done. The error
says whether that cleanup worked.

**The seed ISO holds the password hash.** It stays in the VM's folder until `virt.vbox.vm.delete`
removes it. The hash is SHA-512 crypt of a random 24-character password, so it does not reveal
the password, but anyone who can read that folder can read it, as they can read the VM's disk.

**`start` leaves the autostart mark on.** While the VM runs, a restart of the Windows host
starts it again. `virt.vbox.vm.stop` clears the mark.

**Deleting the base.** `virt.vbox.vm.delete` of a VM that linked clones were made from is
refused by VirtualBox while those clones exist. Delete the clones first.
