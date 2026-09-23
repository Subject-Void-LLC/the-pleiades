# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Phase 77 (SFTP/SCP) is COMPLETE at 12 of 12, UNCOMMITTED, on `feature/sftp-scp`**, cut from `265a38d`
(main after PR #39 merged Phase 101 and the dependabot otel bump). It was the last open phase in
v0.2.0, which the tracker now reports as 636 of 636: **v0.2.0 is cuttable.** Cutting it, and moving
`buildinfo.CurrentRelease` to `0.3.0`, is a separate change the user has not asked for.

### What shipped

The user chose to build **both SFTP and legacy SCP** (the checklist named only SFTP), behind one narrow
port:

- **`pkg/filexfer`**: the port (`Store` with `Put`/`Get`, optional `Stater`), a `Path` only the pure
  `Resolve` can build (so an escape is refused before anything is dialed), `Contained` and `LeafKind`
  (the one physical containment rule both protocols use), `ExactReader`/`LimitWriter`, typed errors.
  Stdlib only, held to a no-I/O import allowlist by `internal/archtest`.
- **`pkg/sftpxfer`**: `Open(ctx, rwc)` over `Conn.Subsystem(ctx, "sftp")` with `github.com/pkg/sftp`
  v1.13.11 (the one new dependency). Containment resolved on the client with LSTAT/READLINK. Atomic
  replace through a private 0700 directory and `posix-rename@openssh.com`.
- **`pkg/scpxfer`**: one POSIX script per transfer that reports physical paths and waits for the Go
  client's verdict before running `scp -t`/`-f`; atomic replace through `mktemp -d` and `mv -f`.
- **`pkg/remoteexec`**: `Conn.Start` returning a streaming `Process`; `Subsystem` and `Process` share
  one unexported `stream`. Every existing `TestSubsystem_` test passed unedited.
- **`linux.Server.FileTransferRoot()`**: property `file_transfer_root`, no default; the capability is
  declared only when it is set, and a bad value is refused at inventory load.
- **Test support**: `testsupport.StartSSHD` (with `KnownHosts`, `RootExec`, `InstallClientKey`), and the
  shared release-gate suite `pkg/filexfer/filexfertest`, which both adapters run word for word.

### Deviations from the plan approved after discovery

The plan was approved before the second-opinion design review returned; that review, and what building
the code then showed, changed these parts of it. Each was reported to the user when it was made.

From the design review:

1. `FileTransferCapable` is declared only when `file_transfer_root` is set, and an unusable value is
   refused at inventory load. The plan declared it on every `linux_server` and refused at transfer time,
   which would let a method pass plan-time validation and then fail.
2. `remoteexec` keeps its `Subsystem` type and adds `Start`/`Process`, both over one unexported `stream`.
   The plan renamed `Subsystem` to `Channel`; keeping it let every existing test prove the refactor
   unedited.
3. The port is `filexfer.Store`, `Get` takes a limit, `Put` is all-or-nothing, and `Mode` is a named type.
   The plan had `FileTransport`, no limit, and `fs.FileMode`.
4. SCP runs one script per transfer that waits for the client's verdict, so the check and the write
   share one pinned working directory. The plan ran a separate check command before `scp`.
5. SFTP writes its temporary file inside a private 0700 directory, because `pkg/sftp` creates files with
   no permission attributes. The plan used a temporary sibling file.
6. The archtest staleness guard was changed to catch either condition, and both dead allowlist entries
   were removed. The plan only rewrote the FileTransfer entry's text.
7. The device reference gained conditional capabilities for hand-written types, which also listed
   `cisco_router`'s `NetconfCapable` for the first time. Not in the plan.

From building it:

1. SFTP containment is resolved on the client with LSTAT and READLINK, not with the server's REALPATH,
   because `pkg/sftp`'s own server answers REALPATH lexically.
2. `remoteexectest` now ends a session when the command exits, a harness change outside the planned
   files, and SCP's `receive` closes its input before waiting for the device to end.
3. One shared sshd starter (`testsupport.StartSSHD`, with a fixture probe test and `InstallClientKey`)
   replaced the per-package container starts the plan described.
4. The benchmarks dial a fresh connection per operation, so the comparison with the OpenSSH clients,
   which must connect every time, is fair.
5. The `pkg/tftpxfer` NUL finding was recorded and left for the user's decision, not fixed in its own
   commit as the plan said, because it is a security fix in a package this phase does not otherwise touch.
6. Every new Go file got a file-level doc comment, which `commitgate` requires of an added file and the
   plan did not mention.

### Findings that changed the work

1. **pkg/sftp's server answers REALPATH lexically**, so the planned containment check would have
   failed open against any server built on that library (FAILURE_PATTERNS 313, LESSONS 229).
2. **The in-process SSH harness ended a session on input EOF, not on command exit**, unlike sshd, and
   deadlocked the first client whose command refuses early. Fixed in `remoteexectest`; all 32 packages
   using it pass (FAILURE_PATTERNS 314, LESSONS 231).
3. **The archtest staleness guard needed both conditions**, so two dead allowlist entries lived on, and
   `FileTransferCapable`'s doc cited a docs/10 disclosure that never existed (FAILURE_PATTERNS 315).
4. **The device reference could not show a conditional capability on a hand-written type**, so
   `cisco_router`'s `NetconfCapable` was never listed (FAILURE_PATTERNS 316).
5. **A fuzz counter froze during minimization** and one time-bounded run ended FAIL with no crasher;
   both were rerun properly before any count was recorded (FAILURE_PATTERNS 318, LESSONS 232).

### Open decision for the user (security finding, not fixed)

**`pkg/tftpxfer` accepts NUL in a remote filename**, which `pin/tftp`'s `packRQ` copies verbatim into
the request packet ahead of its own terminator, so a filename could inject a transfer mode or option.
No consumer exists. The fix is one refusal plus a table row, and its fuzz test should assert refusals
rather than only "no panic" (FAILURE_PATTERNS 317). Reasoned from both sources, not exploited.

Also recorded, out of scope: `sdk.Connect` dials a device directly and ignores its bastion route, so
the first Collection method built on these libraries cannot reach a device behind a bastion until
that is closed. docs/10 and Book 11 both say so.

### Verification run

Green: `go build`, `gofmt`, `vet` under both tag sets, `go mod tidy -diff`, `gosec` (the same 23
waived findings, no new waiver), `govulncheck` (nothing reached), `docs-lint`, gendocs tests, the
touched archtests, the tracker's own tests. Under `-race`: `pkg/filexfer` 98.8%, `pkg/sftpxfer` 95.1%,
`pkg/scpxfer` 94.3%, `pkg/remoteexec` 94.5%, `internal/inventory/devices/linux` 100%. Both release gates
against OpenSSH 10.3, including one real bastion hop and 256 MiB each way. Six fuzz targets, 29.1 million
executions, no crashers. Four falsifications, each failing as it should.

`make docs-gen-check` differs from the last commit by exactly the intended `devices.md` change, so it
passes once this is committed.

**NOT yet run: `make ci` in full**, which on this machine has to run alone and is the user's call.

### Next

Commit this as the three commits in the session report (the `remoteexec` streaming change, the
file-transfer feature, then the archtest and device-reference fixes with these records), then run
`make ci` alone. After that, v0.2.0 can be cut.
The tracker's next phase in the walk is Phase 35.

### Files changed this session

New: `pkg/filexfer/` (with `filexfertest/`), `pkg/sftpxfer/`, `pkg/scpxfer/`, `pkg/remoteexec/stream.go`,
`process.go`, `process_test.go`, `internal/inventory/devices/linux/filetransfer.go` and its test,
`internal/testsupport/sshd.go` and its test, `internal/archtest/filexfer_test.go`,
`changelog/sftp-scp-file-transfer.added.md`. Changed: `pkg/remoteexec/subsystem.go`,
`pkg/remoteexec/remoteexectest/server.go`, `internal/inventory/devices/linux/server.go`,
`internal/archtest/transport_reachability_test.go`, `pkg/capability/capabilities_network.go`,
`pkg/remotefile/remotefile.go`, `tools/gendocs/devices.go`, `tools/gendocs/completeness_test.go`,
`docs/reference/devices.md`, `docs/10-running-in-production.md`, `docs/11-extending-pleiades.md`,
`Makefile`, `coverage-floor.json`, `go.mod`, `go.sum`, and the four FAILURE_PATTERNS/LESSONS files.
Gitignored: `.SPECIFICATION/IMPLEMENTATION.md`, `.SPECIFICATION/SECURITY_ATTESTATION.md`.
