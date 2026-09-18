package launch_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds"
)

// This file covers what a template must be before it is saved.
//
// Checked at the write rather than at the launch, for the reason every
// other write path here gives: a record that cannot be used is one somebody
// finds out about at the worst possible moment, which for a template is the
// moment they are trying to run something.

func TestTemplate_RefusesWhatCouldNeverBeLaunched(t *testing.T) {
	valid := func() launch.Template {
		return launch.Template{
			Name: "patch", KindName: "runbook", Definition: "patch-edge",
			InventoryID: 1, OrganizationID: 1,
		}
	}

	cases := map[string]func(launch.Template) launch.Template{
		"no name": func(tm launch.Template) launch.Template {
			tm.Name = " "
			return tm
		},
		"no definition": func(tm launch.Template) launch.Template {
			tm.Definition = ""
			return tm
		},
		"a definition its kind refuses": func(tm launch.Template) launch.Template {
			tm.Definition = "patch.edge"
			return tm
		},
		// Required, and this is the line that closes a recorded gap: a job
		// launched with no inventory has no organization to be tagged with,
		// which is why Job.organization_id had no writer.
		"no inventory": func(tm launch.Template) launch.Template {
			tm.InventoryID = 0
			return tm
		},
		"a default its kind has no field for": func(tm launch.Template) launch.Template {
			tm.Defaults = launch.Fields{"job_tags": []string{"patch"}}
			return tm
		},
		"a default of the wrong type": func(tm launch.Template) launch.Template {
			tm.Defaults = launch.Fields{"verbosity": "very"}
			return tm
		},
		"a default outside its declared range": func(tm launch.Template) launch.Template {
			tm.Defaults = launch.Fields{"verbosity": 99}
			return tm
		},
		// A promptable field the kind does not accept would render a
		// control on the launch form that the resolver then ignores, which
		// is the affordance-for-nothing shape recorded as #100.
		"a prompt for a field that does not exist": func(tm launch.Template) launch.Template {
			tm.Prompts = []string{"colour"}
			return tm
		},
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			if err := mutate(valid()).Validate(); err == nil {
				t.Errorf("Validate accepted a template with %s", name)
			}
		})
	}

	if err := valid().Validate(); err != nil {
		t.Fatalf("the valid template does not validate, so none of the above proves anything: %v", err)
	}
}

func TestSurvey_RefusesWhatNobodyCouldAnswerCorrectly(t *testing.T) {
	cases := map[string]launch.Survey{
		"a question writing nowhere": {Enabled: true, Questions: []launch.Question{
			{Variable: " ", Type: launch.QuestionText},
		}},
		"two questions writing to one variable": {Enabled: true, Questions: []launch.Question{
			{Variable: "version", Type: launch.QuestionText},
			{Variable: "version", Type: launch.QuestionInteger},
		}},
		"an unknown question type": {Enabled: true, Questions: []launch.Question{
			{Variable: "version", Type: launch.QuestionType("colour")},
		}},
		"a choice between nothing": {Enabled: true, Questions: []launch.Question{
			{Variable: "version", Type: launch.QuestionChoice},
		}},
		"a default outside its own choices": {Enabled: true, Questions: []launch.Question{
			{Variable: "version", Type: launch.QuestionChoice, Choices: []string{"a", "b"}, Default: "c"},
		}},
		// A default password is a credential sitting in the template
		// record, readable by anybody who may edit the template and copied
		// into every duplicate of it.
		"a password with a default": {Enabled: true, Questions: []launch.Question{
			{Variable: "token", Type: launch.QuestionPassword, Default: "hunter2"},
		}},
		"a minimum above its maximum": {Enabled: true, Questions: []launch.Question{
			{Variable: "batch", Type: launch.QuestionInteger, Min: 50, Max: 10},
		}},
	}

	for name, survey := range cases {
		t.Run(name, func(t *testing.T) {
			if err := survey.Validate(); !errors.Is(err, launch.ErrInvalidSurvey) {
				t.Errorf("Validate returned %v for %s, want ErrInvalidSurvey", err, name)
			}
		})
	}
}

func TestSurvey_KnowsWhichAnswersAreSecret(t *testing.T) {
	survey := launch.Survey{Enabled: true, Questions: []launch.Question{
		{Variable: "version", Type: launch.QuestionText},
		{Variable: "vault_token", Type: launch.QuestionPassword},
		{Variable: "sudo_password", Type: launch.QuestionPassword},
	}}

	// Named here rather than decided at each call site, so the storage
	// layer, the API projection and the UI cannot disagree about which
	// answers are secret. Getting that wrong in one place is enough to
	// write a password into a database column in plaintext.
	secret := survey.SecretVariables()
	if len(secret) != 2 || secret[0] != "sudo_password" || secret[1] != "vault_token" {
		t.Errorf("SecretVariables() = %v, want both password answers in a stable order", secret)
	}
}

func TestSurvey_ADisabledSurveyAsksNothingWithoutLosingItsQuestions(t *testing.T) {
	survey := launch.Survey{Enabled: false, Questions: []launch.Question{
		{Variable: "version", Type: launch.QuestionText, Required: true},
	}}

	// Turning a survey off is different from deleting its questions, and
	// collapsing the two would mean disabling one loses the work. A
	// disabled survey asks nothing, so its required question does not
	// refuse a launch.
	if survey.Asks() {
		t.Error("a disabled survey reports that it asks something")
	}
	answers, err := survey.Resolve(nil, launch.FilePolicy{})
	if err != nil {
		t.Errorf("a disabled survey refused a launch that supplied no answers: %v", err)
	}
	if len(answers) != 0 {
		t.Errorf("a disabled survey produced %v", answers)
	}
	if len(survey.Questions) != 1 {
		t.Error("disabling a survey lost its questions")
	}
}

func TestSurvey_DropsAnAnswerToAQuestionItNoLongerAsks(t *testing.T) {
	survey := launch.Survey{Enabled: true, Questions: []launch.Question{
		{Variable: "version", Type: launch.QuestionText},
	}}

	// A relaunch of a job whose template has since lost a question would
	// otherwise be unlaunchable. The dropped value reaches nothing: it is
	// not in the merged map, so no runbook or playbook can read it.
	answers, err := survey.Resolve(map[string]any{"version": "17.6", "removed_question": "value"}, launch.FilePolicy{})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, present := answers["removed_question"]; present {
		t.Error("an answer to a question the survey no longer asks reached the variables")
	}
	if answers["version"] != "17.6" {
		t.Errorf("the answer that is still asked for did not survive: %v", answers)
	}
}

func TestTemplate_RequiredCapabilitiesAreItsOwnCopy(t *testing.T) {
	tmpl := launch.Template{RequiredCaps: []string{"AptCapable"}}

	got := tmpl.RequiredCapabilities()
	got[0] = "mutated"

	if tmpl.RequiredCaps[0] != "AptCapable" {
		t.Error("a caller mutating the returned capabilities changed the template's own")
	}
}

func TestSurvey_ChecksWhatWasSuppliedWithoutDemandingWhatWasNot(t *testing.T) {
	survey := launch.Survey{Enabled: true, Questions: []launch.Question{
		{Variable: "version", Label: "VERSION", Type: launch.QuestionChoice, Required: true, Choices: []string{"17.3", "17.6"}},
		{Variable: "forks", Label: "FORKS", Type: launch.QuestionInteger, Min: 1, Max: 20},
		{Variable: "region", Label: "REGION", Type: launch.QuestionText},
	}}

	// The difference from Resolve, and the reason this method exists: a
	// saved configuration is legitimately partial. It may carry the routine
	// overrides while the required answer arrives at launch, so demanding
	// one here would refuse a configuration that is perfectly launchable.
	if err := survey.CheckAnswers(map[string]any{"region": "eu-west"}); err != nil {
		t.Errorf("CheckAnswers refused a partial configuration: %v", err)
	}
	if _, err := survey.Resolve(map[string]any{"region": "eu-west"}, launch.FilePolicy{}); err == nil {
		t.Error("Resolve accepted a launch with no answer to a required question, which is what CheckAnswers is a weaker form of")
	}

	// What it does check is every value that is present, by the same rules
	// Resolve applies, so a configuration cannot be stored carrying a value
	// that is refused the moment somebody launches from it.
	for name, answers := range map[string]map[string]any{
		"a choice nobody offered":    {"version": "99.9"},
		"a number outside its range": {"forks": 900},
		"text where a number goes":   {"forks": "many"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := survey.CheckAnswers(answers); err == nil {
				t.Errorf("CheckAnswers accepted %v", answers)
			}
		})
	}

	// A blank answer is the untouched control a form submits, which is an
	// absent answer rather than an invalid one.
	if err := survey.CheckAnswers(map[string]any{"version": "", "region": nil}); err != nil {
		t.Errorf("CheckAnswers refused an untouched control: %v", err)
	}

	// An answer to a question this survey does not ask is dropped rather
	// than refused, matching Resolve: a configuration saved before a
	// question was removed must not become unstorable.
	if err := survey.CheckAnswers(map[string]any{"gone": "value"}); err != nil {
		t.Errorf("CheckAnswers refused an answer to a question it no longer asks: %v", err)
	}

	// And a survey that asks nothing checks nothing, rather than refusing
	// every answer as unknown.
	if err := (launch.Survey{}).CheckAnswers(map[string]any{"anything": "at all"}); err != nil {
		t.Errorf("a survey that asks nothing refused an answer: %v", err)
	}
}

func TestDescriptor_NormalizeIsTheOneDefinitionOfWhatAFieldAccepts(t *testing.T) {
	d, ok := launch.Lookup("runbook")
	if !ok {
		t.Fatal("the runbook kind is not registered")
	}

	// Exported for the launch form, which has to refuse a bad value while
	// the operator is still standing at the control. The resolver reports
	// such a value as ignored, which is right for a caller who chose their
	// own request body and wrong for a form that offered the control.
	if _, err := d.Normalize("forks", "12"); err != nil {
		t.Errorf("Normalize refused a numeric string for an int field: %v", err)
	}
	if _, err := d.Normalize("forks", 9000); err == nil {
		t.Error("Normalize accepted a value outside the field's declared bounds")
	}
	if _, err := d.Normalize("nothing_like_it", "x"); !errors.Is(err, launch.ErrInvalidField) {
		t.Errorf("Normalize of an unknown field returned %v, want ErrInvalidField", err)
	}

	// The same rules the resolver applies, which is the whole reason it is
	// exported rather than reimplemented: a form carrying its own copy of
	// "an integer between 1 and 1000" would be a second answer that drifts
	// the first time a kind changed its bounds.
	tmpl := launch.Template{
		Name: "t", KindName: "runbook", Definition: "pb-1", InventoryID: 1,
		Prompts: []string{"forks"},
	}
	_, ignored, err := tmpl.Resolve(context.Background(), launch.Config{
		Overrides: launch.Fields{"forks": 9000},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(ignored) != 1 || ignored[0].Name != "forks" {
		t.Fatalf("Resolve reported %+v for a value Normalize refuses, want forks alone", ignored)
	}
}
