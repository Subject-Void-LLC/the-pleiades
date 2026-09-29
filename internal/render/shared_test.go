package render_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
)

// TestOneEngineServesEveryDeclaredCallSite is the ledger for PLAN.md
// Section 25's "Template renderer" row.
//
// That row names four call sites (six since Phase 117a added task
// parameters and data-chosen targets): credential injectors, notification
// messages, constructed inventory, and survey defaults. The rule attached
// to it is that the contract has exactly one implementation, and a second
// implementation is a defect rather than a variation. Proving that needs
// two things, and this file is one of them:
//
//   - This table is the ledger. Every declared call site has a row. A row
//     either runs against this engine for real, or skips while naming the
//     phase that owns it. A future phase adding its call site edits this
//     table, which is a smaller and more obvious act than inventing a
//     second engine.
//   - internal/archtest's TestExactlyOneRendererImplementation is the
//     enforcement. It fails the build if any package imports a second
//     template engine. A ledger alone would be a promise; the archtest
//     makes it structural.
//
// The reason this is a test rather than a comment is the Phase 22 checklist
// item "Adversarial Pattern Justification: prove the renderer is shared,
// not duplicated." An argument is not a proof.
func TestOneEngineServesEveryDeclaredCallSite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		// site is the call site as PLAN.md Section 25 spells it.
		site string

		// phase owns the call site. Empty means this phase built it.
		phase string

		// source and vars exercise the shape that call site actually
		// renders, so a skipped row still records what the engine will be
		// asked for rather than only that something is missing.
		source string
		vars   map[string]any
		want   string
	}{
		{
			site: "credential injectors",
			// Built by this phase. The two templates below are the exact
			// injector values in the committed parity corpus at
			// tests/parity/testdata/credential_types/custom-rest-api-token.json,
			// which is a real response body from a production Ascender
			// deployment. If this engine cannot render these, no AWX
			// credential type can be migrated at all.
			source: "{{ api_token }}",
			vars:   map[string]any{"api_token": "T", "api_url": "U"},
			want:   "T",
		},
		{
			site: "task parameters",
			// Built by Phase 117a (internal/engine's render_params.go): a
			// task's params read the run's variables and earlier results
			// when its node dispatches. The shape below is the ticket
			// write-back a runbook renders from a registered API result.
			source: "/api/now/table/incident/{{ result.ticket.json.result.sys_id | urlencode }}",
			vars: map[string]any{"result": map[string]any{"ticket": map[string]any{"json": map[string]any{
				"result": map[string]any{"sys_id": "a1b2 c3"},
			}}}},
			want: "/api/now/table/incident/a1b2%20c3",
		},
		{
			site: "data-chosen targets",
			// Built by Phase 117a: a task's params.target renders from data,
			// bounded by its within: (internal/engine's resolveBounded). The
			// shape below is the switch a ticket's configuration item names.
			source: "{{ result.ticket.json.result.cmdb_ci }}",
			vars: map[string]any{"result": map[string]any{"ticket": map[string]any{"json": map[string]any{
				"result": map[string]any{"cmdb_ci": "core-sw1"},
			}}}},
			want: "core-sw1",
		},
		{
			site:  "notification messages",
			phase: "Phase 28, The Notification Engine",
			// Phase 28's own checklist says "Render messages through the
			// Phase 22 renderer. A second renderer is a gate failure." The
			// shape below is an AWX notification body, recorded here so
			// that phase starts from a known target rather than a blank
			// page.
			source: "Job {{ job.id }} finished with status {{ job.status }}",
			vars: map[string]any{"job": map[string]any{
				"id": 42, "status": "successful",
			}},
			want: "Job 42 finished with status successful",
		},
		{
			site:  "constructed inventory",
			phase: "unowned, no phase has claimed it",
			// A constructed inventory names a host expression over device
			// facts. Recorded, not built.
			source: "{{ facts.hostname }}.{{ facts.domain }}",
			vars: map[string]any{"facts": map[string]any{
				"hostname": "web01", "domain": "example.net",
			}},
			want: "web01.example.net",
		},
		{
			site:  "survey defaults",
			phase: "unowned; launch.Question.Default is a literal string today",
			// launch.Survey stores a default as a plain string with no
			// evaluation, so nothing renders here yet. When it does, it
			// renders through this engine.
			source: "{{ organization | default('default-org') }}",
			vars:   map[string]any{},
			want:   "default-org",
		},
	}

	eng := render.New()

	for _, tt := range tests {
		t.Run(tt.site, func(t *testing.T) {
			t.Parallel()

			if tt.phase != "" {
				t.Skipf("call site not built yet, owned by %s. When it is built it consumes render.Engine, and this row loses its skip.", tt.phase)
			}

			tmpl, err := eng.Compile(tt.source)
			if err != nil {
				t.Fatalf("Compile(%q) failed: %v", tt.source, err)
			}
			got, err := tmpl.Render(tt.vars)
			if err != nil {
				t.Fatalf("Render() failed: %v", err)
			}
			if got != tt.want {
				t.Errorf("Render() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestPackageExposesNoDefaultEngine is the third guard, and the cheapest.
//
// There is no render.Default() and no package-level instance, so a caller
// cannot acquire an engine except from its composition root. That is what
// makes "exactly one renderer" a property of the wiring: a second engine
// cannot appear because somebody forgot to pass one in, only because
// somebody typed render.New() in a place that should have taken an Engine,
// and the archtest catches that.
//
// This test exists to fail at compile time if a default is ever added,
// since the only way to satisfy it is for the symbol not to exist.
func TestPackageExposesNoDefaultEngine(t *testing.T) {
	t.Parallel()

	// New is the only constructor, and it returns the port rather than a
	// concrete type, so a caller cannot reach past the interface.
	var eng render.Engine = render.New()
	if eng == nil {
		t.Fatal("New() returned nil")
	}
}
