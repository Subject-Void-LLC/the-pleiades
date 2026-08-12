package launch_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds"
)

// This file covers the prompt-resolution matrix: what a caller may change
// at launch, what they may not, and what they are told about the
// difference.
//
// The headline is Phase 21's own Release Gate, written before any of this
// existed: a template with three prompt-able and two locked fields accepts
// overrides for the three, reports the two in ignored_fields, and rejects a
// survey answer violating its schema.

// gateTemplate is that template: three fields open, two locked.
func gateTemplate() launch.Template {
	return launch.Template{
		Name:           "patch the edge routers",
		KindName:       "runbook",
		Definition:     "patch-edge",
		InventoryID:    7,
		OrganizationID: 3,
		Defaults: launch.Fields{
			"limit":      "edge-*",
			"verbosity":  1,
			"forks":      5,
			"timeout":    600,
			"extra_vars": map[string]any{"region": "eu-west-1"},
		},
		// Open: limit, verbosity, extra_vars. Locked: forks, timeout.
		Prompts: []string{"limit", "verbosity", "extra_vars"},
	}
}

func TestReleaseGate_ThreeOpenFieldsApplyAndTwoLockedOnesAreReported(t *testing.T) {
	tmpl := gateTemplate()
	if err := tmpl.Validate(); err != nil {
		t.Fatalf("the gate's own template does not validate: %v", err)
	}

	resolved, ignored, err := tmpl.Resolve(context.Background(), launch.Config{
		Overrides: launch.Fields{
			// The three the template opened.
			"limit":      "edge-01",
			"verbosity":  4,
			"extra_vars": map[string]any{"version": "2.1"},
			// The two it locked.
			"forks":   100,
			"timeout": 1,
		},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// The three applied.
	if got := resolved.Fields.String("limit"); got != "edge-01" {
		t.Errorf("limit = %q, want the override %q", got, "edge-01")
	}
	if got := resolved.Fields.Int("verbosity"); got != 4 {
		t.Errorf("verbosity = %d, want the override 4", got)
	}

	// The two did not, and the template's own values survived. This is the
	// half that matters: a locked field silently taking the override would
	// be a privilege escalation with nothing anywhere to show it happened.
	if got := resolved.Fields.Int("forks"); got != 5 {
		t.Errorf("forks = %d, want the template's 5: a locked field took an override", got)
	}
	if got := resolved.Fields.Int("timeout"); got != 600 {
		t.Errorf("timeout = %d, want the template's 600: a locked field took an override", got)
	}

	// And the caller was told, by name and by reason.
	if len(ignored) != 2 {
		t.Fatalf("Resolve reported %d ignored fields (%+v), want exactly forks and timeout", len(ignored), ignored)
	}
	for _, ig := range ignored {
		if ig.Name != "forks" && ig.Name != "timeout" {
			t.Errorf("Resolve reported %q as ignored, which the template opened", ig.Name)
		}
		if ig.Reason != launch.ReasonLocked {
			t.Errorf("%s was ignored for reason %q, want the locked reason", ig.Name, ig.Reason)
		}
		if ig.Layer != launch.LayerLaunch {
			t.Errorf("%s was reported at layer %q, want %q", ig.Name, ig.Layer, launch.LayerLaunch)
		}
	}

	// Extra variables merged rather than replaced: the launch added one and
	// the template's own survived. Two callers setting two different
	// variables both meant it.
	if resolved.ExtraVars["region"] != "eu-west-1" {
		t.Errorf("extra_vars lost the template's region: %+v", resolved.ExtraVars)
	}
	if resolved.ExtraVars["version"] != "2.1" {
		t.Errorf("extra_vars did not take the launch's version: %+v", resolved.ExtraVars)
	}
}

func TestReleaseGate_ASurveyAnswerViolatingItsSchemaIsRejected(t *testing.T) {
	tmpl := gateTemplate()
	tmpl.Survey = launch.Survey{
		Enabled: true,
		Questions: []launch.Question{
			{Variable: "target_version", Label: "Version", Type: launch.QuestionChoice,
				Required: true, Choices: []string{"17.3", "17.6"}},
			{Variable: "batch_size", Label: "Batch size", Type: launch.QuestionInteger,
				Min: 1, Max: 50, Default: "10"},
		},
	}
	if err := tmpl.Validate(); err != nil {
		t.Fatalf("the gate's own survey does not validate: %v", err)
	}

	// A choice that is not one of the choices.
	_, _, err := tmpl.Resolve(context.Background(), launch.Config{
		Answers: map[string]any{"target_version": "18.0"},
	})
	if !errors.Is(err, launch.ErrSurveyAnswer) {
		t.Errorf("Resolve with an answer outside its choices returned %v, want ErrSurveyAnswer", err)
	}

	// A number outside its bounds.
	_, _, err = tmpl.Resolve(context.Background(), launch.Config{
		Answers: map[string]any{"target_version": "17.6", "batch_size": 500},
	})
	if !errors.Is(err, launch.ErrSurveyAnswer) {
		t.Errorf("Resolve with an out-of-range answer returned %v, want ErrSurveyAnswer", err)
	}

	// A required question left blank.
	_, _, err = tmpl.Resolve(context.Background(), launch.Config{Answers: map[string]any{}})
	if !errors.Is(err, launch.ErrSurveyAnswer) {
		t.Errorf("Resolve with a required answer missing returned %v, want ErrSurveyAnswer", err)
	}

	// And the valid case really does run, so the three refusals above are
	// about the answers rather than about the survey being unanswerable.
	resolved, _, err := tmpl.Resolve(context.Background(), launch.Config{
		Answers: map[string]any{"target_version": "17.6"},
	})
	if err != nil {
		t.Fatalf("Resolve with valid answers: %v", err)
	}
	if resolved.ExtraVars["target_version"] != "17.6" {
		t.Errorf("the answer did not reach extra variables: %+v", resolved.ExtraVars)
	}
	// The unanswered optional question fell back to its declared default,
	// rather than being absent.
	if resolved.ExtraVars["batch_size"] != 10 {
		t.Errorf("batch_size = %v, want the declared default 10", resolved.ExtraVars["batch_size"])
	}
}

func TestResolve_LayersApplyLeastSpecificFirst(t *testing.T) {
	tmpl := gateTemplate()
	tmpl.Prompts = []string{"limit", "verbosity", "extra_vars", "forks", "timeout"}

	resolved, ignored, err := tmpl.Resolve(context.Background(), launch.Config{
		Saved:     launch.Fields{"limit": "from-saved", "forks": 20},
		Overrides: launch.Fields{"limit": "from-launch"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(ignored) != 0 {
		t.Fatalf("nothing should be ignored when every field is open: %+v", ignored)
	}

	// The launch beats the saved configuration, and the saved
	// configuration beats the template. An operator standing at the form
	// beats a configuration saved months ago.
	if got := resolved.Fields.String("limit"); got != "from-launch" {
		t.Errorf("limit = %q, want the launch's value", got)
	}
	if got := resolved.Fields.Int("forks"); got != 20 {
		t.Errorf("forks = %d, want the saved configuration's 20", got)
	}
	if got := resolved.Fields.Int("timeout"); got != 600 {
		t.Errorf("timeout = %d, want the template's 600, which no layer changed", got)
	}
}

func TestResolve_ALockedSavedConfigurationIsReportedAgainstItsOwnLayer(t *testing.T) {
	tmpl := gateTemplate()

	_, ignored, err := tmpl.Resolve(context.Background(), launch.Config{
		Saved: launch.Fields{"forks": 99},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(ignored) != 1 {
		t.Fatalf("Resolve reported %+v, want the one locked field from the saved layer", ignored)
	}

	// Named against the saved configuration, not against the launch. A
	// saved configuration that has drifted out of what its template permits
	// is worth telling somebody about, and it is not the mistake of
	// whoever is launching right now.
	if ignored[0].Layer != launch.LayerSaved {
		t.Errorf("the ignored field is reported at layer %q, want %q", ignored[0].Layer, launch.LayerSaved)
	}
}

func TestResolve_AFieldTheKindDoesNotHaveIsReportedDifferentlyFromALockedOne(t *testing.T) {
	tmpl := gateTemplate()

	// tags is a real field, but of the playbook kind. A runbook template
	// has no such field at all, which is a different thing from having one
	// that is locked, and a caller can act on the difference: one means
	// edit the template, the other means stop sending it.
	_, ignored, err := tmpl.Resolve(context.Background(), launch.Config{
		Overrides: launch.Fields{"tags": []string{"patch"}},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(ignored) != 1 || ignored[0].Name != "tags" {
		t.Fatalf("Resolve reported %+v, want tags alone", ignored)
	}
	if ignored[0].Reason != launch.ReasonUnknownField {
		t.Errorf("tags was ignored for reason %q, want the unknown-field reason", ignored[0].Reason)
	}
}

func TestResolve_CarriesTheAdapterItsKindDeclares(t *testing.T) {
	native, _, err := gateTemplate().Resolve(context.Background(), launch.Config{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	legacy := gateTemplate()
	legacy.KindName = "playbook"
	legacy.Definition = "playbooks/patch.yml"
	sandboxed, _, err := legacy.Resolve(context.Background(), launch.Config{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// The badge is not decoration. The two kinds resolve to two different
	// adapters, which is what makes "runbook or playbook" a statement about
	// what will actually run it rather than a label.
	if native.Adapter == sandboxed.Adapter {
		t.Fatalf("both kinds resolved to adapter %q, so the kind decides nothing", native.Adapter)
	}
	if native.Adapter != "native" {
		t.Errorf("a runbook template resolved to adapter %q, want native", native.Adapter)
	}
	if sandboxed.Adapter != "legacy" {
		t.Errorf("a playbook template resolved to adapter %q, want legacy", sandboxed.Adapter)
	}
}

func TestResolve_CarriesTheTenancyItsInventoryGaveIt(t *testing.T) {
	resolved, _, err := gateTemplate().Resolve(context.Background(), launch.Config{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// A job launched from a template is tagged with the template's
	// organization, which the template got from its inventory. This is what
	// gives Job.organization_id its first writer.
	if resolved.OrganizationID != 3 {
		t.Errorf("resolved organization = %d, want the template's 3", resolved.OrganizationID)
	}
	if resolved.InventoryID != 7 {
		t.Errorf("resolved inventory = %d, want the template's 7", resolved.InventoryID)
	}
}

func TestResolve_DoesNotMutateWhatItWasGiven(t *testing.T) {
	tmpl := gateTemplate()
	saved := launch.Fields{"limit": "from-saved"}
	tmpl.Prompts = append(tmpl.Prompts, "forks")

	if _, _, err := tmpl.Resolve(context.Background(), launch.Config{Saved: saved}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, _, err := tmpl.Resolve(context.Background(), launch.Config{
		Saved:     saved,
		Overrides: launch.Fields{"limit": "from-launch", "forks": 3},
	}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// A saved configuration is shared by every launch that uses it. A fold
	// that wrote through would let one launch's overrides leak into the
	// next one, which nobody would notice until two runs disagreed.
	if saved.String("limit") != "from-saved" {
		t.Errorf("the saved configuration was mutated to %q", saved.String("limit"))
	}
	if saved.Has("forks") {
		t.Error("a launch override was written into the saved configuration")
	}
	if tmpl.Defaults.String("limit") != "edge-*" {
		t.Errorf("the template's defaults were mutated to %q", tmpl.Defaults.String("limit"))
	}
}

func TestResolve_RefusesAnUnregisteredKind(t *testing.T) {
	tmpl := gateTemplate()
	tmpl.KindName = "terraform"

	_, _, err := tmpl.Resolve(context.Background(), launch.Config{})
	if !errors.Is(err, launch.ErrUnknownKind) {
		t.Errorf("Resolve of an unregistered kind returned %v, want ErrUnknownKind", err)
	}

	// Refused rather than defaulted to the one kind that does exist.
	// Running a playbook as a runbook would hand a file to an executor that
	// cannot read it; the reverse would hand a runbook to a sandbox that
	// would try to.
	if err := tmpl.Validate(); !errors.Is(err, launch.ErrUnknownKind) {
		t.Errorf("Validate of an unregistered kind returned %v, want ErrUnknownKind", err)
	}
}

func TestResolve_AMalformedValueIsReportedRatherThanDiscardingTheLaunch(t *testing.T) {
	tmpl := gateTemplate()

	resolved, ignored, err := tmpl.Resolve(context.Background(), launch.Config{
		Overrides: launch.Fields{
			"verbosity": 99,        // out of its declared range
			"limit":     "edge-42", // perfectly good, in the same layer
		},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// One bad value does not discard the rest of the launch, and the
	// caller is told which one it was.
	if got := resolved.Fields.String("limit"); got != "edge-42" {
		t.Errorf("limit = %q: a valid value was discarded alongside an invalid one", got)
	}
	if got := resolved.Fields.Int("verbosity"); got != 1 {
		t.Errorf("verbosity = %d, want the template's 1", got)
	}
	if len(ignored) != 1 || ignored[0].Name != "verbosity" {
		t.Fatalf("Resolve reported %+v, want verbosity alone", ignored)
	}
	if ignored[0].Reason == launch.ReasonLocked {
		t.Error("an out-of-range value was reported as locked, which sends its caller to edit the wrong thing")
	}
}
