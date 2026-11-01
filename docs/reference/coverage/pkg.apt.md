---
status: beta
---

# pkg.apt: APT (apt-get)

Contract (CLI reference): <https://salsa.debian.org/apt-team/apt/-/raw/2.6.1/doc/apt-get.8.xml>, version `2.6.1`, sha256 `0b6b74538cea46fa17dff8f90b0825101228e5849810f43848480b19c1eff219`.

**19 operations: 2 mapped, 5 excluded, 12 not implemented. Mapped 14.3%, live 14.3% of the 14 in scope.**

## Operations

| Operation | Contract summary | Status | Method, or why excluded | Live |
| --- | --- | --- | --- | --- |
| `update` | Resynchronize the package index files from their sources. | unmapped | - | - |
| `upgrade` | Install the newest versions of all packages currently installed. | unmapped | - | - |
| `dist-upgrade` | Upgrade, handling changing dependencies with new package versions. | unmapped | - | - |
| `dselect-upgrade` | Follow the changes made by dselect to the Status field of available packages. | excluded | follows selections made in the dselect interactive frontend, which an unattended run never sets | - |
| `install` | Install one or more packages, optionally at a version or from a release. | mapped | [pkg.apt.install](../modules/pkg/apt/install.md), [pkg.apt.upgrade](../modules/pkg/apt/upgrade.md) | yes |
| `reinstall` | Reinstall packages that are already installed and at the newest version. | unmapped | - | - |
| `remove` | Remove packages, leaving their configuration files. | mapped | [pkg.apt.remove](../modules/pkg/apt/remove.md) | yes |
| `purge` | Remove packages and delete their configuration files. | unmapped | - | - |
| `source` | Fetch source packages. | excluded | fetches a source package into the current directory: it changes nothing the host runs, and a task has no working directory on the device to collect it from | - |
| `build-dep` | Install or remove packages to satisfy the build dependencies of source packages. | unmapped | - | - |
| `satisfy` | Satisfy dependency strings. | unmapped | - | - |
| `check` | Update the package cache and check for broken dependencies. | unmapped | - | - |
| `download` | Download the binary package into the current directory. | excluded | downloads a .deb into the current directory without installing it, leaving no state on the host a runbook would manage | - |
| `clean` | Clear out the local repository of retrieved package files. | unmapped | - | - |
| `autoclean` | Clear out retrieved package files that can no longer be downloaded. | unmapped | - | - |
| `autoremove` | Remove packages installed to satisfy dependencies that are no longer needed. | unmapped | - | - |
| `autopurge` | Autoremove, also deleting configuration files. | unmapped | - | - |
| `changelog` | Download a package changelog and display it. | excluded | prints a package's changelog from the network and changes nothing on the host | - |
| `indextargets` | Display the index files apt-get update would download. | excluded | a query for frontend developers about which index files apt-get update would fetch, not package management | - |

## Methods and their proof

Per OS: `live` is a test against the real thing, `fixture` one against a stand-in, `-` none.

| Method | Operations | linux | freebsd | darwin | windows |
| --- | --- | --- | --- | --- | --- |
| [pkg.apt.install](../modules/pkg/apt/install.md) | `install` | live (`TestCLI_AptChecksMatchTheirRealRuns`) | - | - | - |
| [pkg.apt.remove](../modules/pkg/apt/remove.md) | `remove` | live (`TestCLI_AptChecksMatchTheirRealRuns`) | - | - | - |
| [pkg.apt.upgrade](../modules/pkg/apt/upgrade.md) | `install` | live (`TestCLI_AptChecksMatchTheirRealRuns`) | - | - | - |
