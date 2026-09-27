# VirtualBox lab: Ubuntu and Windows VMs The Pleiades makes, seeds and manages

This directory is the worked example for the `virt.vbox.*` Collection: a Windows host running
Oracle VirtualBox, and Ubuntu VMs on it that The Pleiades downloads, creates, gives a login,
boots, trusts and then manages over SSH, and Windows Server VMs it installs, seeds and manages
over WinRM. Every step is a `pleiades` command or runbook. Nothing
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
      list.yaml             every VM on the host, with its size and the address it was given
      resize.yaml           shut the lab VM down, give it another size, start it again
      teardown.yaml         power the lab VM off and delete it
      windows-01-install.yaml   install Windows Server from its ISO, generalized, as a base
      windows-02-create.yaml    clone the Windows lab VM from it, seeded, and boot it
      windows-03-wait.yaml      wait until it answers WinRM as its Administrator
      windows-04-eject.yaml     take its answer file out and delete it
      windows-05-first-run.yaml the first run against it, over WinRM
      windows-look.yaml         a picture of its screen, its log, its addresses
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

To check all seven runbooks against your inventory before running any of them, without
touching the host:

```bash
pleiades validate
```

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
    size: small
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
  - the `small` size: one CPU and 2048 MB ("Sizes", below);
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
        size: small
        state: running
        uuid: 493a71a2-27aa-41f5-a436-4647fb3cbada
```

This is where the lab VMs are visible: your own VirtualBox Manager will not list them (next
section). `size` is read from each VM's CPUs and memory, and left out when they are not one
size's, as for the base. `address` and `device` come from what `virt.vbox.vm.clone` recorded
on the VM.
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

## Windows Server guests

The same host makes Windows Server VMs. Microsoft's evaluation ISO is installed once, with no one
at the keyboard, into a base that is generalized and never booted again; each lab VM is a linked
clone of it, seeded with its own computer name, address and Administrator password.

### 1. Install the base

Fetch the evaluation ISO onto the host with `win.file.download`, as step 1 fetches Ubuntu's
image, then:

```bash
pleiades run runbooks/windows-01-install.yaml
```

`virt.vbox.vm.install` makes a VM with a new 64 GB disk, BIOS firmware and no network, and puts
the ISO and an answer file in its DVD drives. The answer file is built by The Pleiades: it
partitions the disk, installs the edition `image` names (`Windows Server 2025 Standard
Evaluation` is Server Core) and takes the new Windows through audit mode. There it deletes the two
copies of itself Windows cached, points the registry (`HKLM\SYSTEM\Setup`, `UnattendFile`) at
`D:\Autounattend.xml`, and runs sysprep, which generalizes it and shuts it down. The task waits for
that: between 5 and 9 minutes on the lab host at two CPUs.

The registry pointer is the one way a clone reads its seed. Measured in the clones' own setup
logs, a generalized Windows looks for its answer file at first boot only in that registry value
and in its own folders, never on a DVD, although Microsoft's documentation lists removable media;
and a cached copy of the install's own answer file, left in place, is found first and ends the
search. Then it takes both DVDs out, deletes the answer file and marks the
VM installed. The answer file's only password is one made at random for audit mode's sign-in and
kept nowhere by The Pleiades. Windows keeps it, not blanked, in the second cached copy
(`C:\Windows\Panther\unattend-original.xml`), which is why audit mode deletes both; a clone's seed
then sets a password of its own.

A run that stops waiting (its timeout, or a read VirtualBox refused for a moment) leaves the
install running; run the task again and it waits for the same install and finishes it. An install
that is still running at the timeout also leaves a picture of its screen in its folder.

### 2. Give the VM an inventory entry and a login

```bash
pleiades add-host win-lab --type windows_server --set host=192.168.56.30 --tags lab
pleiades add-credential win-lab --username Administrator --generate
```

A Windows VM is reached over WinRM by its built-in Administrator's password, and its answer file
can hold nothing else, so this is the one case where the engine hands a creating method the
password itself: only `virt.vbox.vm.clone`, which declares it can seed a Windows VM, and only for
a login device reached over WinRM. A Linux login still gives only a public key and a hash. The
generated password always has upper case, lower case and a digit, as Windows' default policy
requires.

### 3. Create, boot and wait

```bash
pleiades run runbooks/windows-02-create.yaml
pleiades run runbooks/windows-03-wait.yaml
```

The clone's seed is `Autounattend.xml` on its one DVD (on its SATA controller, so D:): computer
name, fixed address on the host-only adapter, time zone, locale, the first-boot screens skipped,
and the Administrator's password. On the lab host the clone answered WinRM at 192.168.56.30 as
its Administrator 82 seconds after it started. With no size, a Windows clone is small (one CPU and 2048 MB).
`wait.connection` waits until the VM answers WinRM as its Administrator: Ansible's
`wait_for_connection`, for SSH or WinRM.

### 4. Eject the seed and use the VM

```bash
pleiades run runbooks/windows-04-eject.yaml
pleiades run -v runbooks/windows-05-first-run.yaml
```

The answer file holds the Administrator's password, so `virt.vbox.vm.eject_seed` takes it out of
the drive once the first boot has read it, and the guest can never read it again. VirtualBox keeps
an image locked for as long as the VM that held it runs, so on a running VM the file stays on the
host and the task says so in a warning; run it again after `virt.vbox.vm.stop` and it deletes the
file. It works for Linux VMs too, whose seed holds a password hash.

### When a VM has no way in yet

`runbooks/windows-look.yaml` shows what a VM without a window is doing:

- `virt.vbox.vm.screenshot` saves a picture of its screen here, as a PNG;
- `virt.vbox.vm.log` reads the end of its VirtualBox log, filtered by a pattern if you give one;
- `virt.vbox.vm.addresses` reports the addresses its host-only adapter has: the fixed one clone
  gave it and the DHCP leases VirtualBox's own server handed it, which is how to reach a VM whose
  fixed address was never applied;
- `virt.vbox.vm.send_keys` types into its console, in Packer's `<enter>`/`<tab>` notation. Never
  type a secret with it: the text travels on the host's VBoxManage command line.

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

### Sizes

A VM's size names its CPUs and memory together, as a cloud's instance type does:

| Size | CPUs | Memory |
|---|---|---|
| `xsmall` | 1 | 1024 MB |
| `small` | 1 | 2048 MB |
| `medium` | 2 | 4096 MB |
| `large` | 4 | 8192 MB |
| `xlarge` | 8 | 16384 MB |

- **`virt.vbox.vm.clone` takes `size`**, or `memory_mb` and `cpus`, but not both, since one
  would silently overrule the other. With none of them, the VM is `xsmall`.
- **`virt.vbox.vm.resize` changes a stopped VM**, by `size` or by either number alone.
  VirtualBox changes memory and CPUs only while a VM is powered off, so a running VM is
  refused; stop it first. The run records the size the VM had as its undo.
- **A VM larger than the host is refused** before it is made or resized: more CPUs than the
  host has processors online, or more memory than it has.
- **`virt.vbox.vm.start` refuses a VM the host cannot hold right now**: more memory than the
  host has free, less 1024 MB it keeps for itself. On this host, with 32 GB and about 15 GB
  free, `xlarge` can be made but not started.
- **A clone that finds a VM of another size warns.** It never changes a VM it finds, and a
  plain "ok" would hide that the VM is not the size the runbook asks for.

To make the lab VM bigger:

```yaml
- name: Shut the lab VM down
  virt.vbox.vm.stop:
    name: ubuntu-lab
- name: Make it medium
  virt.vbox.vm.resize:
    name: ubuntu-lab
    size: medium
- name: Start it again
  virt.vbox.vm.start:
    name: ubuntu-lab
```

On a Windows host where WSL 2 or Docker Desktop runs, give a VM its first boot at a one-CPU
size; "Limits and troubleshooting" says why, and what to change in the guest before resizing it
to `medium` or larger.

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

**Keep to one CPU on this kind of host, or change the guest first.** Where WSL 2 or Docker
Desktop is installed, Windows' own hypervisor is running, and VirtualBox runs guests on top of it
through the Windows Hypervisor Platform (its log says `Attempting fall back to NEM`). Measured on
this host:

- A guest with 2, 3 or 4 CPUs hung at boot about one time in three on first boots, and 7 times
  in 8 on later reboots. Every hang stopped at `Begin: Loading essential drivers ...`, where
  the initramfs loads the RAID modules and the kernel times its RAID6 and XOR routines against
  the guest's timer. A one-CPU guest never hung.
- Holding the VM to the processor's performance cores did not help, nor did turning
  VirtualBox's paravirtualized clock off (`paravirt_provider: none`).
- A guest that no longer loads those modules at boot did not hang: 8 reboots in 8 at two CPUs,
  against 7 hangs in 8 before.

So make the first boot at one CPU (`size: xsmall` or `small`), remove what loads the modules
(`apt-get purge mdadm btrfs-progs` and `update-initramfs -u` on Ubuntu, fine for a VM whose root
is ext4 on a plain disk), stop the VM, and resize it with `virt.vbox.vm.resize`. The other fix
is to turn Windows' hypervisor off (`bcdedit /set hypervisorlaunchtype off`), which also turns
off WSL 2 and Docker Desktop.

**A Windows VM from an EFI base: one CPU.** A VM booting EFI firmware with two CPUs stopped at the
firmware's `DXE_AP` step (starting the second CPU) under the Windows hypervisor, before drawing
anything; at one CPU the same VM booted. This was measured while the account's VirtualBox server
was pinned to the performance cores, so the pinning is not ruled out. `virt.vbox.vm.install`
makes BIOS bases, which ran Windows Setup at two CPUs.

**Microsoft's evaluation VHDX does not read a seed.** `virt.vbox.vm.import_disk` makes a VM from
it (2 minutes 46 seconds for its 11 GB, EFI read off the disk), but a clone of it never reads its
answer file at first boot, for the reason above: nothing in that image points Windows at the DVD.
Its clones stop at the first-boot screens. Use `virt.vbox.vm.install` for a Windows base.

**A seed stays on the host while its VM runs.** See step 4: VirtualBox keeps the image locked, so
`eject_seed` on a running VM empties the drive and leaves the file, which for Windows holds the
Administrator's password, until the VM is stopped and `eject_seed` runs again.

**`host_keys` times out.** Its error quotes the last line the VM's console printed, which says
where the boot stopped; `Begin: Loading essential drivers ...` is the multi-CPU hang above. The
whole console is `G:\PleiadesLab\<vm>\console.log`, and the VM's own VirtualBox log is
`G:\PleiadesLab\<vm>\Logs\VBox.log`.

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
