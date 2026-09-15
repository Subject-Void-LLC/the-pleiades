# Handoff Document

Rewrite the "Current Status" section when stopping mid-task or handing off, per `.AGENTS/AGENTS.md`.

## Current Status (this session)

**Branch `feature/ui-revamp`, off `main` at the merge of PR #30 (Phase 40). A web UI pass:
page anatomy, accessibility mode, AWX tab parity, a declared system settings area, and
structural fixes to the Las Ventanas skin. Three commits, NOT pushed. One design question
is open and is the reason it was not.** No Phase 40 code was touched.

### The open question, and why the branch stopped here

**A runbook has no Run button, and cannot simply be given one.** A dispatch needs an
inventory; a runbook names none; `Dispatcher.LaunchTemplate` and `Dispatcher.Relaunch` are
the only launch paths that exist, and both are keyed on a template. So "Run this runbook"
is one of three things, and which one is a product decision rather than an implementation
detail:

1. **Run creates a template, then launches it.** Buildable today with existing ports.
   Every ad-hoc run leaves a permanent template behind, so the Templates list fills with
   one-offs.
2. **A real ad-hoc dispatch path.** New `Dispatcher` surface, a new endpoint and scope.
   Cleanest for the operator, and it bypasses the declared field bounds that are the
   reason templates exist as a security boundary.
3. **No Run button.** What is built now: a Templates tab on the runbook listing what
   already runs it, one click to that template's Launch. This is what AWX does, where a
   playbook is never dispatched directly either.

Nothing else is blocked on this.

### What was built

**One shared chrome, table and zero state.** `view.Chrome` (`internal/ui/view/chrome.go`)
carries breadcrumb, title, status badge, actions and tabs, built by all six page models and
rendered by one component holding no decisions. `view.TableModel` and `view.ZeroState`
(`table.go`) replaced three near-identical table copies and four renderings of "nothing
here". The section copy had no reference links, which is why three drill-downs were dead
ends. See LESSONS_LEARNED #176.

**Records have tabs**, addressed by `?tab=`, computed from the `Sections` and `Stream` a
descriptor already declares. `Descriptor.DefaultTab` lets a view name its landing tab; Jobs
opens on Live output. A lone Details tab renders no strip: one tab is not a tab strip.

**Status badges and record titles are declared, not inferred.** `Descriptor.NameField` and
`Descriptor.StatusBadgeField`. Inferring the status from the first listed badge field was
wrong nearly everywhere it applied: a failed job was headed "4821 runbook" and every runbook
record was headed "YES". Only `jobs`, `devices` and `templates` declare a status.

**Accessibility mode is a mode.** Named that everywhere,
`AccountModel.AccessibilityEffects` enumerates all five things it does, and `/a11y`,
`/theme` and `/skin` left the session gate for a group using the new `preferenceCSRF`.
Signed out, the toggle is the first focusable element on the document. See
LESSONS_LEARNED #178.

**Appearance moved to a Preferences page** (renamed from Settings, which the deployment's
own configuration needed: LESSONS_LEARNED #179). Preference changes return the reader to
the exact tab and page they were on (`web/preferences.go`, LESSONS_LEARNED #177).

**Every view carries its AWX counterpart's tabs.** Implemented where a port exists:
Organizations gained Teams, Inventories gained Access, Templates gained Schedules, Runbooks
gained Templates. `"Completed jobs"` became `"Jobs"` because it never filtered to completed
ones (FAILURE_PATTERNS #212). Declared in full where no port exists, each naming the
specific gap (LESSONS_LEARNED #175).

**A declared system settings area** at `/settings`, gated on the new `auth.ScopeSettingsRead`,
covering AWX's five tiles. Nothing is editable and nothing renders a live control, because
there is no settings store. **Role admin is settings access today, with no separate grant**
(`auth.Identity.HasScope` lets `RoleAdmin` bypass every scope check).

**Las Ventanas was structurally broken and is fixed.** The shell painted nothing, so every
region that is not a `.block` rendered onto the Windows 95 desktop teal at roughly 1.5:1,
while every contrast test passed because they measure against `--bg` and the text was on
`--body-bg` (FAILURE_PATTERNS #215). The outer window bevel is a real border now, not an
inset shadow the opaque children painted over. The pane divider and empty states use the
bevel quartet rather than flat and dashed borders.

### Where it stands

- `go build ./...`, `go test ./internal/... ./cmd/...`, `make fmt`, `make vet`, `make arch`,
  `make gosec`, `make docs-lint` and `templ generate` in-sync: all clean.
- `go run ./tools/coverage-check -tolerant`, which is what `push-gate` runs, exits 0. The
  four unrelated floor regressions seen earlier in the session do not reproduce.
- `make ci` itself has NOT been run end to end.
- The pre-commit gate passes with three warnings it does not refuse on: `chrome.go` (327
  lines) and `systemsettings.go` (366) exceed the soft 300-line cap, and a pre-existing
  error string in `field.go` opens with a capital.

### Next steps

1. Answer the Run-button question above.
2. The list toolbar and a real pager, which needs a `Count` on each store and a
   `Filterable` flag on `view.Field`. It is the largest remaining gap against AWX.
3. The Devices Capabilities tab, declared now: the only screen that can answer why a task
   was skipped on one device and not its neighbour.
4. The settings area's fifth tile. AWX has six and the request named five.
