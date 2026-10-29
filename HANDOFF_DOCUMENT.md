# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-10-29. Branch `feat/authoring-parity-v0-3-0`, off `main` at `122b8c92`, through `7ff4ddab` plus this
session's uncommitted tree. Not pushed, no pull request, `make pr-check` has not run on this tree, no tag cut.**

This session cut the test pipeline down to the Crawl and Walk tiers and gave it one memory and disk policy, at the
owner's direction ("cut all fat that does not align with the current crawl walk tier"; "the same rigor of memory and
disk space management across local, remote, and container environments"), then, at the owner's second direction,
made the lab VMs shut down after a check and keep every run's variable data on a second disk.

### What works (unit-tested; the lab proofs are listed under Open)

- **Cut from pr-check, code removed:** the Mac's Podman share of the container packages (`testgate -host`,
  `hosts.json`, `maccheck -containers`, `oscheck.Containers`), the Windows ARM leg (`wincheck -arm64` stays for a
  hand run), and govulncheck (it runs in `make ci` and CI's `ci` job). The CI `winrm` job is gone. The Makefile's
  pr-check recipe, the monitor (`tools/monitor`), prreport's part order, CLAUDE.md and `docs/design/os-checks.md`
  say so. `PR_CHECK_PARTS` is now fast, coverage-fast, repeat, containers, durations, mac, bsd, win, images,
  coverage, sizes.
- **One policy, `tools/internal/headroom`, applied inside `tools/testgate`** (headroom.go there): the disk check
  before anything builds (4 GB where the run builds and in TMPDIR), `-p` lowered to what the free memory fits
  (1200 MB a package after 1 GB kept; a cgroup limit such as `capped`'s is read on Linux), `GOMEMLIMIT` at the
  budget unless set, a sampler every 5 s charging each running package, a stop (SIGTERM to go test's own process
  group, SIGKILL 30 s later) under 700 MB or 3 GB of swap growth, an interrupt forwarded the same way, and a
  `headroom` field in every record (`flakegate.Headroom`; schema regenerated). Readers for Linux, macOS, FreeBSD and
  Windows. Tests: `tools/internal/headroom` (89%), `tools/internal/flakegate` (93.6%, observed run and process group
  proven against the real toolchain), `tools/testgate` (80.9%), archtest green.
- **Lab VMs shut down, not saved:** `oscheck.Box.PutBack` is `virt.vbox.vm.stop mode=acpi timeout=300`, then
  `mode=poweroff` if the guest did not shut down (`PutBackGrace` 8 min). Tests in `tools/internal/oscheck`.
- **Data disk:** two new methods, `virt.vbox.vm.attach_disk` and `virt.vbox.vm.detach_disk` (catalog 212), with
  `pkg/vboxmanage.AttachDiskAt`/`HasDisk`, tested against the model host; `PLEIADES_BSD_DATA`,
  `PLEIADES_WINRM_DATA`, `PLEIADES_WINARM_DATA` name the directory, and every POSIX and Windows check script puts
  the trees, GOCACHE, GOMODCACHE and TMPDIR/TEMP there and wipes leftovers before a run (`tools/internal/oscheck`,
  `tools/bsdcheck`, `tools/wincheck` tests). Lab runbooks: `~/pleiades-lab/runbooks/bsd/check-06-data-disk.yaml`,
  `check-07-data-mount.yaml`, `win/check-08-data-disk.yaml`, `check-09-data-mount.yaml` (all validate).
- **The resources manifest (owner's ask, 2026-10-29: "min/max resources needed per test ... in a json manifest for
  faster rebalancing", "disk usage should be known too"):** `tools/testgate/resources.json` holds, per unit (a package,
  or a test of `tests/e2e`), the seconds, memory and disk ever recorded, least and most, with the run count, the last
  date and the hosts. `testgate -resources-record` folds a run's measurements in (`make resources-record`), and
  `testgate -resources-check` holds a run to it (pr-check's new `resources` step; `make coverage-measured`): an unknown
  unit is named while `complete` is false and fails once it is true, and one far past its recorded most is named. A
  local pass now packs its `-p` by the manifest's recorded memory for its packages (`packedParallelism`). The guard
  samples disk too (`headroom.DiskWatch`): the record carries `package_disk_mb` and `min_disk_free_gb`, and the
  measurement `disk_mb`, `test_memory_mb` and `test_disk_mb` for the divided packages' tests. The manifest is seeded
  with seconds from `durations.json` and the last fast pass (269 units, `complete: false`); memory and disk fill as
  gates and CI runs are folded in. Sharding still reads `durations.json`.
- Lab: bsd-check's data disk (40 GB, `/dev/ada1p1` on `/data`) and win-check's (60 GB, `D:`) exist, made and
  formatted through pleiades; `tests/bsd/.env` and `tests/win/.env` name them. FAILURE_PATTERNS 726 (a restart right
  after a shutdown left the VM off once; the start by hand worked).
- Records: LESSONS 355, FAILURE_PATTERNS 726 to 729 (727: the first Windows proof found the test script bypassing
  the data directory's env; 728 and 729: the branch review found the disk methods' undo recorded after fallible steps
  and the Windows stop ending go.exe alone; all fixed). Memory notes `pipeline-cut-and-headroom`,
  `lab-vms-shutdown-data-disk`.

### Open

- **Lab proofs passed** (`.IGNORE/timings.md`, 2026-10-29): `go run ./tools/bsdcheck -p 1 tools/internal/prreport`
  booted bsd-check cold from power-off, built and tested on `/data` (`.pleiades-bsdcheck/{.gocache,<sha>}`, `gomod`,
  `tmp`; 601 MB used of 39 GB), passed both passes in 2m38s and shut the VM down through ACPI; `wincheck` did the same
  on `D:\pleiades` in 6m10s with cgo and the race detector on. A leg tests the committed tree, so the policy's own
  FreeBSD and Windows readers (`headroom.Read`) run on the legs once this tree is committed; their unit tests ran here.
  Both VMs are powered off.
- The manifest's memory and disk numbers come from runs made after this change; `make resources-record` after the
  next gate, and after CI's artifacts are downloaded, fills them. Set `complete: true` once every package is in it.
- Compaction itself (`VBoxManage modifymedium --compact`) is not a method yet; the shutdown makes it possible.
- The branch's earlier open items (Phase 131 R4, Phase 87 to 89 gates, run 7 items, the Mac leg for the full gate)
  are unchanged from the archived status.

### Next, in order

1. Finish the lab proofs above and record their timings in `.IGNORE/timings.md`.
2. Commit this session's work (the owner asked for no commit; the tree is dirty), then `make pr-check` through the
   monitor, push, one pull request.

### Files changed this session

`tools/internal/headroom/*` (new), `tools/testgate/{headroom.go,resources.go,resources.json,main.go,options.go,pieces.go}`
(hosts.go, hosts.json removed), `tools/internal/flakegate/{observe.go,procgroup_*.go,headroom.go,run.go,report.go,flakegate.go}`,
`tools/internal/oscheck/{box.go,lab.go,posix.go,flow.go}` (containers.go removed), `tools/maccheck`, `tools/bsdcheck`,
`tools/wincheck`, `tools/internal/prreport/report.go`, `tools/gendocs/prreportschema*.go`, `tools/monitor/*.py`,
`Makefile`, `.github/workflows/ci.yml`, `internal/catalog/virt/vbox/vm/{attach_disk,detach_disk}.go`,
`pkg/vboxmanage/create.go`, `internal/forge/catalogdata/collections_virt.go`, `docs/reference` (regenerated),
`CLAUDE.md`, `docs/design/os-checks.md`, `tests/{bsd,win}/.env.example`, `.claude/agents/ci-triager.md`.
