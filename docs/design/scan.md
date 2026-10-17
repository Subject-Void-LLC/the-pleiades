# Design note: the `scan` collection

Status: built (2026-10-15). What a release needs to know about what it ships, asked on the device that holds it: which known
vulnerabilities its dependencies carry, what it is made of (an SBOM), and whether a credential leaked into it.

## Methods

| Method | Does | Site, device |
|---|---|---|
| `scan.vuln` | Runs OSV-Scanner over an image archive, a directory or named lockfiles on the device, and gates on severity (`fail_on`) and, with `fixable_only`, only on vulnerabilities a fixed version exists for. An ignore file on the device suppresses a vulnerability by id, each entry with a reason and an `until` date; an entry past its date suppresses nothing and is reported, as is one that matched nothing. | target, required |
| `scan.sbom` | Runs Syft over an image archive or a directory and writes an SPDX 2.3 JSON SBOM, and a `SHA256SUMS` beside it, into a new directory on the device, after validating the SBOM against the NTIA minimum elements. The subject's own name, version and supplier are the task's. | target, required |
| `scan.secrets` | Runs Gitleaks over a directory on the device and fails on every finding not ignored, each named by file, line and rule, never by its value. | target, required |

## How a scan runs

Each scanner runs in a fresh container on the device's own engine (`containerengine.RunOnce`, as `gate.run`'s
image checks do), from an image pinned by its multi-platform index digest in this platform's source: OSV-Scanner
2.6.0, Syft 1.52.0, Gitleaks 8.30.0. Nothing is installed on the device, the scanner a run uses is the one this
release was tested with, and a tag moving upstream changes nothing. The image is pulled when absent (it is
content-addressed, so a pull cannot substitute another); a device that cannot pull needs it loaded first.

What is scanned is bind-mounted **read-only**, the container's root is read-only, it runs with
no-new-privileges, and it joins **no network**, except `scan.vuln` online, which joins the engine's default
network to reach osv.dev. With `offline_db` (a directory of OSV-Scanner's local databases on the device) it joins
none. The scanner's report comes back on its standard output, bounded, and is parsed here; the SBOM and
`SHA256SUMS` are written by this platform over SSH, never by the container, so no mount is ever writable.

Paths are the engine host's, which is the device's for an engine reached over SSH or its local socket. On an
SELinux host a read-only mount is not relabelled (relabelling would change the files' labels), so a file the
container cannot read is reported as the scanner's error.

## Capabilities

| Method | Capabilities | Why |
|---|---|---|
| all three | `CommandExecCapable`, `ContainerEngineCapable` | reads the ignore file and checks paths over SSH; runs the scanner on the engine |

## Check and Reversibility

| Method | Check | Reversible |
|---|---|---|
| `scan.vuln` | Same as Invoke: it reads, and changes nothing. | Read-only. |
| `scan.sbom` | Generates and validates the SBOM exactly as a run does and reports what it would write, writing nothing. | Yes: its undo removes the directory it created (it never writes into an existing one). |
| `scan.secrets` | Same as Invoke. | Read-only. |

The goal names scans as changing nothing; `scan.sbom` writes its SBOM, so its check is a run that stops before
writing, rather than the run itself.

## Decisions, by edge case

- **Pinned images over binaries on the device.** A device's own `osv-scanner` is whatever version it has, or none;
  three different machines would give three answers. An image pinned by digest gives one, and a release moves the
  pin deliberately (`build.digests.refresh` reads a Go constant no more than a Dockerfile, so the pins are
  constants with a test that each is an index digest).
- **Severity.** OSV-Scanner reports a CVSS score per group of aliases; this maps it to critical (9.0 and up), high
  (7.0), medium (4.0) and low. A vulnerability with no score is **unknown** and is gated as high: an advisory
  nobody has scored yet is not evidence it is harmless. `fail_on` is `critical`, `high` (default), `medium`, `low`
  or `any`.
- **`fixable_only`.** Fails only on vulnerabilities whose advisory names a fixed version for the package's
  ecosystem; the rest are still reported. The default fails on every one at the threshold: an unfixable
  vulnerability is still shipped.
- **The ignore file is this platform's, not the scanner's.** One YAML list per project, `{id, reason, until}`,
  every field required, `until` a date (`2026-12-31`, in UTC) no more than 366 days after the run's, so an
  acceptance is reviewed at least once a year. The scanner's own ignore formats do not require a reason
  or an end, and an ignore that never ends is a vulnerability nobody looks at again. Past `until`, an entry
  suppresses nothing and the run warns; an entry that matches nothing warns, so the file shrinks as fixes land.
  `scan.secrets` reads the same format, its `id` a finding's fingerprint (`file:rule:line`).
- **What is scanned never configures its scanner.** Gitleaks reads a `.gitleaks.toml` (rules and allowlists) and a
  `.gitleaksignore` (fingerprints) at the root of what it scans, and honors an inline `gitleaks:allow` comment;
  OSV-Scanner reads an `osv-scanner.toml` beside each lockfile, at any depth, as vulnerabilities to ignore. Each
  would let a tree's own author suppress its findings, with no reason and no end. So a tree holding one is refused
  before the scanner runs, naming the file, and the platform's ignore file is the way to suppress a finding;
  Gitleaks runs with `--ignore-gitleaks-allow`, and OSV-Scanner with `--config /dev/null`, so none is read even
  where the search could not see it (inside an image archive). A tree the search cannot read whole is refused,
  since the scanner, as its container's root, may read what the search could not.
- **A scan that saw nothing fails.** A wrong path and files the scanner could not read look like a clean tree, so
  OSV-Scanner finding no package to check fails `scan.vuln`, naming what was scanned, and an SBOM holding no
  component but its subject fails `scan.sbom` (it meets every NTIA element and says nothing), each unless
  `allow_empty` says that is expected (then it warns). Gitleaks skips a file or directory it cannot open with only
  a warning on standard error and exits 0, so `scan.secrets` reads that log (at warn level every skip it logs is one
  it could not help; those its rules make are debug lines) and fails naming each path.
- **No secret value leaves the container in a form this keeps.** Gitleaks runs with `--redact`, and the parser
  drops its `Secret` and `Match` fields regardless, so a finding is file, line, rule and fingerprint. A test plants
  a sentinel credential and requires it absent from the result, the error and every log line.
- **NTIA minimum elements.** The document must name its author and timestamp, describe a subject that is a package
  it holds (a DESCRIBES naming anything else is refused, never skipped), and carry dependency relationships,
  and every component a name, a version and a unique identifier (a purl or CPE); the subject's supplier is the
  task's `supplier`. Generators rarely know a dependency's supplier (Syft writes NOASSERTION), so a dependency
  without one is counted and warned about, and fails the task only with `strict_suppliers`; failing every real
  SBOM by default would teach operators to ignore the check.
- **`SHA256SUMS` in `sha256sum` format**, one line for the SBOM, so `sha256sum -c` verifies it on any host. Both
  files are mode 0644 in a 0755 directory: they are reports meant to be read.
- **The subject's supplier is set here, not by Syft.** Syft's `--source-supplier` was measured stamping the
  supplier on every package, claiming the operator supplied every dependency (and doubling the
  `Organization: ` prefix). `pkg/sbomcheck` records it on the described subject alone and adds this platform to
  the creators, since the document is no longer the generator's alone.
- **A report and its scanner's logs are kept apart.** `RunOnce` gains `Errors`, so standard error no longer
  interleaves with the JSON report on standard output.
- **Offline OSV databases are OSV-Scanner 2's layout,** `osv-scalibr/<ecosystem>/all.zip`, read from a read-only
  mount; OSV-Scanner refuses (rather than skips) an ecosystem it finds no database for, which this reports as the
  scanner's error. Syft's update check is turned off (`SYFT_CHECK_FOR_APP_UPDATE=false`), since a container with
  no network answers it with an error on every run.
- **An operating system's binary packages are named.** Alpine's `ssl_client` and `busybox-binsh` come from the
  `busybox` source package; a finding names both, or one advisory reads as three identical lines.

## Reuse

`containerengine.RunOnce` (gains read-only bind mounts, the only kind it will offer), `pkg/remotefile`
(`ReadText` for the ignore file, `Write` for the SBOM), `pkg/gate`'s bounded tail for scanner errors, and the
`file.remove` undo `db.pg.dump` already records. New: `pkg/osvreport` (OSV-Scanner's JSON and the severity
mapping), `pkg/sbomcheck` (SPDX 2.3 and the NTIA elements), `pkg/scanignore` (the ignore file), each parsing
untrusted input and each fuzzed.

## Failure modes

| Failure | Answer |
|---|---|
| The device has no engine onboarded | Refused before contact, naming `pleiades onboard`. |
| The image is absent and cannot be pulled | Refused naming the image and digest, and how to load it. |
| The target path does not exist, or is the wrong kind | Refused before the scanner runs. |
| The tree holds the scanner's own configuration (`.gitleaks.toml`, `.gitleaksignore`, `osv-scanner.toml`) | Refused before the scanner runs, naming each file. |
| The scanner exits with an error (not a finding) | Fails with its last lines, bounded and escaped. |
| OSV-Scanner finds no package to check, or the SBOM holds nothing but its subject | Fails naming what was scanned, unless `allow_empty`. |
| Gitleaks skips a path it cannot read | Fails naming each path (up to 50, the rest counted). |
| The scanner's report is not what this parses | Fails naming the scanner and version; a report is never half-read. |
| An ignore entry without a reason or `until` | The ignore file is refused whole, naming the entry. |
| The SBOM misses an NTIA element | Fails naming the element and the first components missing it. |
| `scan.sbom`'s directory exists | Refused; an SBOM never replaces one. |
| The scan outlives its timeout | The container is killed and removed; fails naming the timeout. |
| The run fails | Its error carries what a failure needs (the failing vulnerabilities, findings or missing elements), bounded. |
