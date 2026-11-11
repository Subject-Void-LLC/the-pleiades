# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**2026-11-10. Branch `feat/inventory-version` (follow-up (4), inventory concurrency) on `main` at `dc740124` (#153).
Built and reviewed; the fp-reviewer and claim-refuter findings are fixed. Every change is staged, nothing is
committed, pushed or opened as a pull request. `make pr-check` has not run on it.**

### What works

- **A stale member list can no longer undo a revocation through the API** (FAILURE_PATTERNS 1140). An inventory has a
  `version` column (sqlite `0046`, postgres `0043`), bumped only in `UpdateWith`'s committing write. `GET`, `POST` and
  `PATCH /inventories/{id}` send an `ETag`; a PATCH naming `groups` or `devices` needs `If-Match` (428 without, 412 when
  not exactly the current tag, 404 first), judged under the row lock. The tag also covers the content, for writes from
  an older build (1144). Proof: `internal/api/inventories_precondition_test.go`
  (`TestInventoryPatch_AStaleCopyCannotRevertARevocation`, `TestInventoryPatch_IfMatchIsJudgedUnderTheLock`,
  `TestInventoryPatch_IfMatchMustNameTheCurrentTagExactly`,
  `TestInventoryPatch_AWriteThatLeavesTheVersionStillMovesTheTag`) and
  `internal/inventory/ent_set_store_version_test.go`.
- **The UI inventory edit form merges instead of replacing** (1141, 1145): `view.Field.KeepsOriginal`, a per-element
  three-way merge under the lock, text compared through `view.Comparable`. Proof:
  `internal/ui/resources/inventories_merge_test.go`, `internal/ui/resources/inventories/merge_internal_test.go`,
  `internal/ui/view/originals_internal_test.go`.
- **A refused form keeps every multi-select choice** (1142), and **store refusals are field errors, not 500s** (1147,
  closing 1121). Proof: `TestUIEdit_AFieldChangedUnderneathIsRefusedAndStaysRefused`,
  `TestAsFault_TheStoreRefusalsTheFormShows`.
- **SQLite migrations from 0046 on cannot reset an AUTOINCREMENT sequence unnoticed** (1143):
  `TestSQLiteMigrations_ARebuildKeepsItsSequence`.

### Current blocker

None.

### Open

- 1085 stays open: team membership (`PATCH /teams/{id}`'s `users`, the UI teams users lookup) and the UI template
  credentials lookup still write a whole list from an earlier read.
- 1143: role bindings are not removed when an inventory is deleted (`role_binding.scope_id` has no foreign key). Owner
  question. Older rebuilds 0003, 0007 and 0022 are left as written.
- 1146: `tools/schemacompat` cannot see a header required only for some bodies; the breaking fragment says so.
- 1139 still waits on the owner.
- `git stash list` is empty: the GNS3 stash earlier entries kept (`stash@{0}`) is no longer there.

### Next, in order

1. Commit the staged branch.
2. `make pr-check` through the monitor, on the clean committed tree.
3. Push, `make branch-containers`, then open the pull request when it passes.

### Files changed on this branch

Code (staged): `internal/api/inventories*.go`, `internal/apispec/apispec*.go`, `internal/ent/schema/inventory.go` and
its generated code, migrations `sqlite/0046_inventory_version.sql` and `postgres/0043_inventory_version.sql`,
`internal/ent/migrate/sequence_guard_internal_test.go`, `internal/inventory/{set.go,ent_set_store*.go}`,
`internal/ui/view/{field.go,model.go,pagemodels.go}`, `internal/ui/resources/inventories/{inventories.go,merge.go}`,
`internal/ui/web/resources.go`, `internal/ui/render/views.templ`, `tools/gendocs/openapi.go`, the regenerated OpenAPI
documents, and the tests beside each. Changelog: `inventory-if-match.breaking.md`, `ui-inventory-edit-merge.fixed.md`.
Records (unstaged): `FAILURE_PATTERNS*.md` (1140 to 1147; notes on 1085, 1112, 1121), `LESSONS_LEARNED*.md` (437 to
439), `HANDOFF_DOCUMENT.md`, `HANDOFF_ARCHIVE.md`.

Lab: no change reported for this branch.
