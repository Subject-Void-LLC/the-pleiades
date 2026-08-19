# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/Catalog-First-Tier`, off `main`. HEAD is `60dae0d`, `cloud.aws.*` plus the `aws`
sync plugin (committed with the user's own live go-ahead). Everything below — the four Windows
capability accessors on `windows.Server`, `svc.windows.*`/`win.feature.*` (7 methods) and the
`windows_server` classification rule — is implemented, tested, and verified on top of that commit,
but uncommitted: no such word has been given yet this session.**

This session opened with "what's the next batch?" `HANDOFF_DOCUMENT.md`'s own "remainder, in
order" list named items 3 and 4 (the `windows_server` classification rule, and
`svc.windows.*`/`win.feature.*`) as next. A plan for both together was written, approved, and
implemented — one batch rather than two, because the classification rule only matters once
`windows_server` is a device type real methods can run against, the same reasoning that made
`cloud.aws.*` and the `aws` plugin one combined commit even though they were planned separately.

### What landed

**`windows.Server` gained four real accessors**, closing the TODO its own doc comment named since
the type was first generated: `WindowsEdition()` (property `windows_edition`, no fallback — purely
descriptive, nothing gates on it, the same restraint `linux.Server.Distribution` applies to its own
detected fact), `ServiceManagerName()` (property `service_manager`, defaulting to `"windows_scm"`,
the exact mirror of `linux.Server.ServiceManagerName`'s shape — this is what makes
`internal/catalog/svc.managerNamespace`'s pre-existing `"windows_scm" -> "svc.windows"` mapping
resolve for real for the first time), `WindowsServiceStartMode()` (property
`windows_service_start_mode`, defaulting to `"Automatic"`, informational like
`SystemdUnitPath` — no method reads it, it satisfies the capability's structural contract) and
`DISMLogPath()` (property `dism_log_path`, defaulting to the real Windows default,
`C:\Windows\Logs\DISM\dism.log`).

**Two new `pkg/` packages, mirroring `pkg/remotesvc` for a transport with no persistent
connection.** `pkg/winrmsvc` (Service Control Manager state) and `pkg/winrmdism` (DISM feature
state) are both built on the existing `pkg/winrmexec`, which dials fresh per call rather than
holding a `Conn` (the credential is a call argument to `winrmexec.Run`, not package state), so both
take an explicit `Session{Target, Auth, Options}` config bundle instead of a live connection.
`pkg/winrmsvc.Status` reads a service's existence, run state and start type in one PowerShell round
trip (`Get-Service -ErrorAction SilentlyContinue` plus `ConvertTo-Json`), the same "one round trip,
decide from real reported state" rule `pkg/remotesvc.Status` already applies. `pkg/winrmdism`
shells out to `dism.exe` directly rather than the `ServerManager` PowerShell module
(`Install-WindowsFeature`), deliberately: `windows.Server.DISMLogPath` already commits this design
to DISM, and `dism.exe /online` works on every Windows SKU while `ServerManager` is Server-only. A
real, non-obvious gotcha surfaced building it: calling a native executable from a PowerShell script
does not make the script's own exit code reflect the executable's, so every script this package
sends ends with an explicit `exit $LASTEXITCODE` line — without it, `Result.ExitCode` would read
success regardless of what `dism.exe` actually reported. DISM's real exit codes are applied
directly: `0` success, `3010` (`ERROR_SUCCESS_REBOOT_REQUIRED`) success-needs-restart (surfaced as
a new `reboot_required` stat rather than folded into `changed`), `87`
(`ERROR_INVALID_PARAMETER`) an unrecognized feature name (surfaced as `Exists: false`, not an
error — the identical "a name the platform has never heard of is an answer" rule `pkg/remotesvc`
applies to a systemd unit).

**`svc.windows.*` (5 methods: `start`/`stop`/`restart`/`enable`/`disable`)** mirrors
`svc/systemd`'s own `unitOp`/`runUnitOp` shared-body shape exactly (`serviceOp`/`runServiceOp`
here). No `daemon_reload` counterpart: the Service Control Manager has no "reread unit files from
disk" operation to expose. `enable`/`disable`'s inverse is genuinely more careful than
`svc.systemd`'s own: Windows services have three start types
(`Automatic`/`Manual`/`Disabled`), and this namespace's `enable`/`disable` only ever set the first
and third. A service found `Manual` that `enable` moves to `Automatic` has no exact reverse through
`disable` (which sets `Disabled`, not `Manual`) — that specific transition emits no inverse at all
rather than one that would over-correct a rollback, which is documented on each method's own
`Reversibility.Notes` and verified directly by driving the real, registered `Enable`/`Disable`
functions with seams swapped, not a hand-copied stand-in for their inverse logic.

**`win.feature.install`/`remove`** mirror the same read-decide-act-read-back shape over
`pkg/winrmdism`. Unlike `svc.windows`'s enable/disable, this inverse is unconditional on the state
found before: DISM's feature states have no third state this namespace manages around the way
`Manual` complicates services, so `Enabled`/`Disabled` are exact complements for the transitions
`install`/`remove` make. `install` passes `/all` (also enabling required parent features, matching
what the Windows GUI's own "Add roles and features" does by default); `remove` deliberately does
not, so removing a feature never silently removes the parents it depended on.

**The `windows_server` classification rule** (`internal/classification/default_ruleset.go`), added
at its own root — agentless, `configure_polling`, the same four capabilities
`windows.NewServer`'s baseline already grants — the same pattern `aws_account`/`catalyst_center`
were each added under when the plugin or batch that needed them was built. The one real, direct
consumer: the `aws` sync plugin's `Classify` no longer quarantines a discovered Windows EC2
instance (`Platform: "windows"`) — it resolves to `windows_server` — while a `Platform` value this
tree still has no rule for continues to quarantine honestly. `aws_localstack_test.go`'s own
`TestClassify_WindowsInstance_Quarantines` (proving the old, now-false behavior) was replaced with
`TestClassify_WindowsInstance` plus a new `TestClassify_UnrecognizedPlatform_Quarantines`
preserving direct coverage of the real quarantine path; `conformance_test.go`'s `aws` backend's own
`unclassifiableUnsupported` explanation was updated to stop citing the retired test by name.

**A real regression, caught and fixed, in code from an earlier session, not new to this batch.**
`internal/catalog/svc/svc_test.go`'s `TestDeclaredButNotImplementedTargetIsNamed` depended on
`svc.windows.start` staying declared forever, and both concrete namespaces
`svc.managerNamespace` maps to are now fully implemented, so there is no longer any real
device/verb combination reachable from outside the package that exercises `dispatch`'s own
"declared but not implemented" branch. `LESSONS_LEARNED.md` #150 generalizes this. Fixed with a new
whitebox test (`internal/catalog/svc/dispatch_internal_test.go`) registering one throwaway,
uniquely-named `StatusDeclared` fixture purely to prove the branch, and a new black-box
`TestDispatchesToWindows` (mirroring `TestDispatchesToSystemd`) proving real dispatch resolves to
`svc.windows.start` against an unreachable address. The identical regression class
`cmd/pleiades/doc_test.go` has hit every prior session that flips a fixture FQCN from declared to
implemented recurred here too, fixed the same way: the fixture moved to `file.template`, the one
FQCN this document already commits to staying declared.

### Testing posture: `pkg/winrmexec`'s, not `cloud.aws.*`'s LocalStack precedent

There is no WinRM emulator the way LocalStack emulates the AWS wire protocol, and `pkg/winrmexec`'s
own package doc already states and accepts that constraint rather than building a stub server that
"would only prove this package agrees with the stub." Every new package and Collection method hits
**100% coverage on everything reachable without a live host**: `pkg/winrmsvc`/`pkg/winrmdism`'s
script construction, quoting and state parsing against canned input; `internal/catalog/svc/windows`
and `internal/catalog/win/feature`'s full decision logic (converged/refusal/inverse, including every
downstream failure-wrapping branch) via `statusFunc`/`startFunc`/`stopFunc`/`restartFunc`/
`enableFunc`/`disableFunc` seams swapped to canned answers — the same role `remoteexectest`'s fake
systemctl plays for `pkg/remotesvc`'s own tests, adapted to a transport with no in-process fake
worth building. `pkg/winrmsvc`/`pkg/winrmdism` themselves sit at 77.5%/73.3% (no recorded floor,
the same "informational" bucket `pkg/winrmexec` itself already sits in): the remaining gap is the
one thing that genuinely needs a live host, a real command's real output coming back, which is
exactly what `pkg/winrmexec`'s own tests document as unfakeable. That one thing gets a new,
env-gated Release Gate, `cmd/pleiades/winrm_service_feature_release_gate_test.go`, reusing
`winrm_static_ip_release_gate_test.go`'s existing host/user/password env vars and adding its own
(`PLEIADES_WINRM_TEST_SERVICE`, `PLEIADES_WINRM_TEST_FEATURE`). It reports **skipped** in this
environment, the same honest status the static-IP gate has carried every session that has touched
WinRM.

### Read this first

**Module names are `xxx.xxx.xxx`.** FAILURE_PATTERNS #158; still the rule, still not violated here.

**No commit without the user's own live word in the current conversation.** Unchanged. `60dae0d`
landed because the user gave that word; nothing below has been asked for yet.

**Never use the Agent or Workflow tool to delegate without being asked, even with Ultracode on.**
Unchanged (`pleiades_no_unrequested_delegation`). Held again this session, including through the
plan-mode transition for this batch.

**Before flipping the last `StatusDeclared` entry a generic dispatcher can resolve to, grep that
dispatcher's own tests for the specific FQCN literal, not just for the word "declared."**
`LESSONS_LEARNED.md` #150, new this session. `svc.managerNamespace` only ever mapped two names
(`systemd`, `windows_scm`); once both concrete namespaces were fully implemented, the dispatcher's
"declared but not implemented" refusal branch had no real example left to exercise it through the
public API at all, which a naive "the test still compiles and the error is still non-nil" glance
would not have caught. The fix (a throwaway registered-but-declared fixture in a new whitebox test
file) is the reusable pattern; watch for the same shape in `net.cli`/`net.netconf` once every
`net.*` vendor namespace is eventually implemented too.

**Calling a native executable from a PowerShell script does not propagate its exit code
automatically.** New this session, in `pkg/winrmdism`'s own package doc: `$LASTEXITCODE` holds the
value, and a script that never reads it leaves the host process's own exit status at whatever it
would otherwise be, typically 0, regardless of what the executable actually reported. Every script
`pkg/winrmdism` builds ends with an explicit `exit $LASTEXITCODE` line for exactly this reason;
worth checking for in any future package that shells out to a native `.exe` over WinRM the way this
one shells out to `dism.exe`.

**Docker was unreachable from this session's shell partway through**
(`docker: command not found in this WSL 2 distro`), and was confirmed clean and reachable again
before this session ended: the user isolated the host crashes this session's earlier segment
discussed to running Docker and Hyper-V at the same time, and a re-check after that fix landed
found `docker ps` answering normally. Every check that needed it was re-run for real at that point
(see "Verification state" below); nothing here is inferred from the earlier Docker-unavailable
window.

### The remainder, in order

1. ~~`fs.*`/`archive.*` and `fw.*`/`container.*`~~ — done, committed at `93a7818`.
2. ~~`cloud.aws.*` (4) and the `aws` sync plugin~~ — done, committed at `60dae0d`.
3. ~~A `windows_server` classification rule~~ — done this session.
4. ~~`svc.windows.*`/`win.feature.*` (7)~~ — done this session.
5. **`net.cli`/`ios`/`eos`/`junos`/`netconf` (6)** is blocked on a NETCONF transport that does not
   exist yet. The next natural batch by this list's own ordering, and the last real transport gap
   in the catalog.
6. **`file.template`** stays declared: the render engine is `internal/render`, unreachable from a
   Collection, and is a stable test fixture in `internal/validate` (and now also
   `cmd/pleiades/doc_test.go`) precisely because it is expected to stay declared for a while.
7. **Make `exec.shell` dispatch on capability**, the way `svc.start` resolves to `svc.systemd.start`
   (and, as of this session, `svc.windows.start`). Unchanged from prior sessions: a design step,
   not a port, still not done.
8. **`file.directory` still has its own mode validator**, unreconciled with `attributes.go`. Also
   unchanged from prior sessions.
9. **Supplementary group membership and account passwords**, deliberately out of scope for
   `identity.user.*`. Unchanged from prior sessions.
10. **The four pre-existing private int-param parsers** could migrate to `sdk.IntParam`. Unchanged
    from prior sessions: deliberately not done, mechanical once started.
11. **Wire `FirewalldCapable`/`DockerCapable`** (and, from a prior session, `PosixAccountCapable`)
    onto a real device type. `FirewalldCapable` specifically needs a per-instance property (like
    `service_manager`) rather than a baseline declare, since firewalld isn't universal the way
    `LinuxCapable`/`SystemdCapable` are.
12. **An S3 object-level primitive** (`PutObject` at minimum) was deliberately not added to
    `pkg/awscloud`. Only worth building if a real `cloud.aws.s3.*` object method is ever wanted.

With items 3 and 4 done, the module catalog now has **70 of 77** methods at
`collection.StatusImplemented` in the working tree (63 committed at `60dae0d`, plus these seven),
confirmed via `internal/archtest`'s `TestEveryImplementedMethodAnswersReversibility`, which logs
the count.

### Verification state

**Every package this batch actually touched, verified individually and cleanly**: `go build
./...`, `go vet ./...`, `make fmt`, `go test -race` (each touched package: `pkg/winrmsvc`,
`pkg/winrmdism`, `internal/catalog/svc/...`, `internal/catalog/win/feature`,
`internal/inventory/devices/windows`, `internal/classification`, `internal/inventory/plugins/aws`,
`cmd/pleiades`), `go test ./internal/archtest/...` (full suite clean, including
`TestCatalogPackagesImportOnlyPkg` proving the two new `pkg/` packages are layered correctly,
`TestCatalogDataDocsMatchTheRegistry` after hand-syncing `internal/forge/catalogdata`'s two files,
and `TestEveryImplementedMethodAnswersReversibility` reporting 70), `make gosec` (the same 9
pre-existing individually-waived findings, zero new ones), `go run ./tools/docs-lint` (clean),
`go run ./tools/govulncheck`/`make govulncheck` (clean — 0 vulnerabilities affecting this code, an
improvement on the `lib/pq` CVEs prior sessions noted; worth re-confirming next session rather than
assuming), and `go generate ./internal/forge/catalogdata` plus `go run ./tools/gendocs` (both
confirmed idempotent, a second run of each produces no further diff).

**Full-repo verification completed cleanly once Docker came back**, and every earlier caveat about
it is superseded by this: `go test -race ./...` (whole repo, real containers — real LocalStack,
real sshd, real NATS) ran to completion with **zero failures across 126 packages**. `go run
./tools/coverage-check`, run non-tolerant with `LOCALSTACK_AUTH_TOKEN` sourced from
`.IGNORE/.localstack.env` (needed separately from Docker itself — the first run after Docker came
back still showed `cloud.aws.ec2`/`s3` "regressed," and the actual cause was this token not yet
being exported in the fresh shell, not Docker), reports **173 packages measured, none below their
recorded floor**. `pkg/awscloud` (95.6%), `internal/inventory/plugins/aws` (99.0%), and every other
LocalStack-dependent number matches exactly what the prior `cloud.aws.*` session recorded, with no
drift. `make gosec` and `go run ./tools/docs-lint` were both re-run clean after Docker returned too.
The one loose end from the Docker-unavailable window is worth still naming rather than dropping:
`internal/catalog/pleiades/builtin/wait`'s `TestPort_UsesTheBashProber` failed once under
full-suite load during that earlier pass and passed cleanly in isolation immediately after and
again during this clean full run; this session touched nothing in or near that package, and it is
not yet added to `flaky-packages.json` — worth watching for a repeat before deciding whether it
belongs there.

`make docs-gen-check` "fails" for the same non-defect reason as every prior session: its own `git
diff --exit-code` compares the regenerated tree against `60dae0d`, and this session's work is real,
intentional, uncommitted content in `docs/reference` and `internal/api/wellknown`. Resolves on its
own the moment this is committed.

### Commit message

Drafted, not run; nothing is committed except `60dae0d`.

```
feat(catalog): svc.windows.* and win.feature.*, the windows_server classification rule (70 of 77)

windows.Server gains four real accessors (WindowsEdition,
ServiceManagerName, WindowsServiceStartMode, DISMLogPath), closing the
TODO its own doc comment has named since the type was first generated
and structurally implementing the three capabilities svc.windows.*/
win.feature.* need. ServiceManagerName defaults to "windows_scm",
which is what makes svc.*'s pre-existing "windows_scm" -> "svc.windows"
dispatch mapping resolve for real for the first time.

pkg/winrmsvc and pkg/winrmdism are new, mirroring pkg/remotesvc for a
transport (WinRM) with no persistent connection to hold: both take an
explicit Session{Target, Auth, Options} bundle rather than a live
conn, since pkg/winrmexec dials fresh per call. pkg/winrmdism shells
out to dism.exe directly rather than the ServerManager PowerShell
module, since dism.exe works on every Windows SKU and
windows.Server.DISMLogPath already commits this design to DISM; every
script it builds ends with an explicit "exit $LASTEXITCODE" line,
without which a native executable's real exit code never reaches
Result.ExitCode at all. DISM's own exit codes are applied directly:
3010 (reboot required) is success, surfaced as a new reboot_required
stat rather than folded into changed; 87 (invalid parameter) on
/get-featureinfo means an unrecognized feature name, surfaced as
Exists: false rather than an error.

svc.windows.* (start/stop/restart/enable/disable) mirrors
svc/systemd's own shared unitOp/runUnitOp shape. enable/disable's
inverse is more careful than svc.systemd's own: a service found with
start type Manual that enable moves to Automatic has no exact reverse
through disable (which sets Disabled, not Manual), so that specific
transition emits no inverse at all rather than one that would
over-correct a rollback. win.feature.install/remove mirror the same
read-decide-act-read-back shape over pkg/winrmdism; install passes
/all (also enabling required parent features), remove deliberately
does not.

The windows_server classification rule (internal/classification/
default_ruleset.go) is what lets the aws sync plugin's Classify
resolve a discovered Windows EC2 instance instead of quarantining it,
the one real consumer this session wired: Classify now resolves
Platform "windows" to windows_server and "" to linux_server, still
quarantining any Platform value neither names.

A real regression in code from an earlier session, not new to this
batch: internal/catalog/svc/svc_test.go's
TestDeclaredButNotImplementedTargetIsNamed depended on
svc.windows.start staying declared forever, and both concrete
namespaces svc.managerNamespace maps to are now fully implemented, so
dispatch's own "declared but not implemented" branch had no real
example left reachable from outside the package. Fixed with a new
whitebox test registering one throwaway declared-only fixture purely
to prove the branch, and a new black-box TestDispatchesToWindows
proving real dispatch to svc.windows.start against an unreachable
address. cmd/pleiades/doc_test.go's own recurring fixture regression
(every prior session that flips a declared FQCN to implemented has hit
this) recurred here too; its two "still declared" fixtures moved to
file.template, the one FQCN this document already commits to staying
declared.

Coverage: pkg/winrmsvc/pkg/winrmdism 77.5%/73.3% (no recorded floor,
the same informational bucket pkg/winrmexec itself already sits in --
the remaining gap is the one thing that genuinely needs a live
Windows host, which pkg/winrmexec's own tests already document as
unfakeable). Every Collection method and the windows.Server accessors
hit 100% coverage on everything reachable without one, via
statusFunc/startFunc/stopFunc/restartFunc/enableFunc/disableFunc seams
swapped to canned answers. cmd/pleiades/
winrm_service_feature_release_gate_test.go is the new, env-gated
Release Gate for the one thing that does need a live host; it reports
skipped in every environment without one, the same honest status
winrm_static_ip_release_gate_test.go has carried every session that
has touched WinRM.

The module catalog now has 70 of 77 methods implemented in the
working tree (63 committed, plus these seven).
```
