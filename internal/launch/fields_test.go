package launch_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds"
)

// This file covers the shapes a launch value actually arrives in.
//
// The same field reaches this package as a Go int from code, a float64 from
// encoding/json, and a string from an HTML form, and every one of those is a
// real caller. A reader that only handled the first would pass every unit
// test written in Go and silently resolve zero on the wire, which for
// `forks` or `timeout` is a behaviour change nobody would attribute to a
// type assertion.

// Template satisfying Launchable is asserted at compile time, so a method
// removed from the interface's implementation fails the build rather than a
// test somebody has to run.
var _ launch.Launchable = launch.Template{}

func wireTemplate() launch.Template {
	return launch.Template{
		Name: "wire", KindName: "runbook", Definition: "wire-runbook",
		InventoryID: 1, OrganizationID: 1,
		Defaults: launch.Fields{"forks": 5, "limit": "default", "labels": []string{"default"}},
		Prompts:  []string{"forks", "timeout", "verbosity", "limit", "labels", "extra_vars"},
	}
}

func TestFields_ReadsAValueThatArrivedAsJSON(t *testing.T) {
	// The shape a launch request really has: decoded from a JSON body, so
	// every number is a float64 and every list is a []any.
	var overrides launch.Fields
	body := `{"forks": 12, "verbosity": 3, "labels": ["patch", "urgent"], "extra_vars": {"region": "eu"}}`
	if err := json.Unmarshal([]byte(body), &overrides); err != nil {
		t.Fatalf("decoding the launch body: %v", err)
	}

	resolved, ignored, err := wireTemplate().Resolve(context.Background(), launch.Config{Overrides: overrides})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(ignored) != 0 {
		t.Fatalf("a well-formed JSON launch reported ignored fields: %+v", ignored)
	}

	if got := resolved.Fields.Int("forks"); got != 12 {
		t.Errorf("forks = %d, want 12: a JSON number read as zero", got)
	}
	if got := resolved.Fields.Int("verbosity"); got != 3 {
		t.Errorf("verbosity = %d, want 3", got)
	}
	if got := resolved.Fields.List("labels"); len(got) != 2 || got[0] != "patch" {
		t.Errorf("labels = %v, want the two submitted", got)
	}
	if resolved.ExtraVars["region"] != "eu" {
		t.Errorf("extra variables = %+v, want the submitted region", resolved.ExtraVars)
	}
}

func TestFields_ReadsAValueThatArrivedFromAForm(t *testing.T) {
	// The shape a browser really submits: every value is a string.
	resolved, ignored, err := wireTemplate().Resolve(context.Background(), launch.Config{
		Overrides: launch.Fields{"forks": "20", "timeout": "300"},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(ignored) != 0 {
		t.Fatalf("a well-formed form submission reported ignored fields: %+v", ignored)
	}
	if got := resolved.Fields.Int("forks"); got != 20 {
		t.Errorf("forks = %d, want 20: a form value read as zero", got)
	}
	if got := resolved.Fields.Int("timeout"); got != 300 {
		t.Errorf("timeout = %d, want 300", got)
	}
}

func TestFields_RefusesANumberThatIsNotWhole(t *testing.T) {
	// "forks: 4.7" is a caller who has misunderstood something, and
	// silently running four would hide it.
	_, ignored, err := wireTemplate().Resolve(context.Background(), launch.Config{
		Overrides: launch.Fields{"forks": 4.7},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(ignored) != 1 || ignored[0].Name != "forks" {
		t.Fatalf("Resolve reported %+v, want forks alone", ignored)
	}
}

func TestFields_RefusesAValueOfTheWrongShapeForItsField(t *testing.T) {
	cases := map[string]launch.Fields{
		"a list where text belongs": {"limit": []string{"edge"}},
		"text where a list belongs": {"labels": "patch"},
		"a list of numbers":         {"labels": []any{1, 2}},
		"text where a map belongs":  {"extra_vars": "region=eu"},
		"words where a number goes": {"forks": "many"},
	}

	for name, overrides := range cases {
		t.Run(name, func(t *testing.T) {
			resolved, ignored, err := wireTemplate().Resolve(context.Background(), launch.Config{Overrides: overrides})
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if len(ignored) != 1 {
				t.Fatalf("Resolve reported %+v, want the one malformed field", ignored)
			}
			// The template's own value survived rather than being
			// overwritten with a zero.
			if resolved.Fields.String("limit") != "default" && ignored[0].Name != "limit" {
				t.Errorf("a malformed value damaged another field: %+v", resolved.Fields)
			}
		})
	}
}

func TestFields_AbsentAndEmptyAreDifferentInstructions(t *testing.T) {
	tmpl := wireTemplate()

	// Absent: inherit the template's limit.
	inherited, _, err := tmpl.Resolve(context.Background(), launch.Config{Overrides: launch.Fields{}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := inherited.Fields.String("limit"); got != "default" {
		t.Errorf("an absent limit resolved to %q, want the template's own", got)
	}

	// Present and empty: run against everything the inventory holds. This
	// is FAILURE_PATTERNS.md #103's distinction, in the other direction:
	// there an absent list was read as an explicit empty one and emptied a
	// membership.
	cleared, _, err := tmpl.Resolve(context.Background(), launch.Config{Overrides: launch.Fields{"limit": ""}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got := cleared.Fields.String("limit"); got != "" {
		t.Errorf("an explicit empty limit resolved to %q, want it cleared", got)
	}
}

func TestSurvey_AcceptsAnswersInTheShapesTheyArriveIn(t *testing.T) {
	tmpl := wireTemplate()
	tmpl.Survey = launch.Survey{Enabled: true, Questions: []launch.Question{
		{Variable: "batch", Type: launch.QuestionInteger, Min: 1, Max: 50},
		{Variable: "ratio", Type: launch.QuestionFloat, Min: 0, Max: 10},
		{Variable: "regions", Type: launch.QuestionMultiSelect, Choices: []string{"eu", "us", "ap"}},
		{Variable: "notes", Type: launch.QuestionTextarea, Max: 19},
	}}

	resolved, _, err := tmpl.Resolve(context.Background(), launch.Config{Answers: map[string]any{
		"batch":   "10",                  // a form
		"ratio":   float64(2.5),          // JSON
		"regions": []any{"eu", "us"},     // JSON
		"notes":   "patched the routers", // exactly at the 19-character limit
	}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	if resolved.ExtraVars["batch"] != 10 {
		t.Errorf("batch = %v, want 10 from a form string", resolved.ExtraVars["batch"])
	}
	if resolved.ExtraVars["ratio"] != 2.5 {
		t.Errorf("ratio = %v, want 2.5", resolved.ExtraVars["ratio"])
	}
	if got, ok := resolved.ExtraVars["regions"].([]string); !ok || len(got) != 2 {
		t.Errorf("regions = %v, want the two chosen", resolved.ExtraVars["regions"])
	}

	// And one character more than the declared maximum is refused, so the
	// bound is real rather than advisory.
	if _, _, err := tmpl.Resolve(context.Background(), launch.Config{Answers: map[string]any{
		"notes": "patched the routers!",
	}}); err == nil {
		t.Error("an answer one character over its maximum was accepted")
	}
}

func TestSurvey_RefusesAChoiceOutsideItsList(t *testing.T) {
	tmpl := wireTemplate()
	tmpl.Survey = launch.Survey{Enabled: true, Questions: []launch.Question{
		{Variable: "regions", Type: launch.QuestionMultiSelect, Choices: []string{"eu", "us"}},
	}}

	if _, _, err := tmpl.Resolve(context.Background(), launch.Config{
		Answers: map[string]any{"regions": []string{"eu", "elsewhere"}},
	}); err == nil {
		t.Error("a multi-select accepted a value that is not one of its choices")
	}
}
