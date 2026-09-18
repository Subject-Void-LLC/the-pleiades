// This file covers the survey file question where it is actually decided:
// the dispatcher, which is the one place the deployment's gate and the
// template's gate meet.
//
// internal/launch's own tests prove the rule in isolation. These prove the
// wiring, which is a separate and more fragile claim: that the deployment's
// half really reaches a launch, that a caller cannot supply it, and that it
// is consulted at every launch rather than only when the question was
// authored. A gate that is correct in its own package and unwired is the
// shape this repository has shipped before.
package api_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
)

// fileQuestionTemplate is a launchable template asking one file question.
func fileQuestionTemplate(armed bool) launch.Template {
	tmpl := launchableTemplate()
	tmpl.Survey = launch.Survey{Enabled: true, Questions: []launch.Question{{
		Variable: "bootstrap", Label: "Bootstrap file", Type: launch.QuestionFile,
		AllowProgramContent: armed,
	}}}
	return tmpl
}

// TestLaunchTemplate_ProgramContentNeedsBothGates is the security property,
// asserted through the real launch path rather than against Survey.Resolve.
//
// The four combinations matter individually. Three of them must refuse, and
// a bug that made any ONE of them pass would be invisible to a test that
// only checked the two diagonal cases.
func TestLaunchTemplate_ProgramContentNeedsBothGates(t *testing.T) {
	const script = "#!/bin/sh\nsetup\n"

	cases := []struct {
		name     string
		system   bool
		question bool
		wantErr  bool
	}{
		{"neither gate", false, false, true},
		{"only the deployment consents", true, false, true},
		{"only the question is marked", false, true, true},
		{"both gates open", true, true, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			jobs := newTestJobStore(t)
			opts := []api.DispatcherOption{
				api.WithTemplates(stubTemplates{tmpl: fileQuestionTemplate(tc.question)}),
				api.WithLaunchConfigs(&recordingConfigs{}),
			}
			if tc.system {
				opts = append(opts, api.WithSurveyFilePolicy(launch.FilePolicy{AllowProgramContent: true}))
			}
			dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), jobs, newCapturingBus(), opts...)

			jobID, _, err := dispatcher.LaunchTemplate(context.Background(), "ada@example.com", 12,
				launch.Config{Answers: map[string]any{"bootstrap": script}}, nil)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("a launch with system=%v question=%v was accepted, want a refusal",
						tc.system, tc.question)
				}
				if !errors.Is(err, launch.ErrProgramContent) {
					t.Fatalf("LaunchTemplate error = %v, want ErrProgramContent", err)
				}
				// A refused launch must leave no job. A run that was
				// recorded and then refused is worse than either outcome.
				if jobID != "" {
					t.Errorf("a refused launch produced job %q", jobID)
				}
				return
			}

			if err != nil {
				t.Fatalf("a launch with both gates open was refused: %v", err)
			}
			job, _, err := jobs.Get(context.Background(), jobID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got := job.ExtraVars["bootstrap"]; got != script {
				t.Errorf("the job runs with bootstrap = %q, want the answer unchanged", got)
			}
		})
	}
}

// TestLaunchTemplate_ACallerCannotSupplyTheDeploymentsConsent is the
// property that makes the pair a real separation of duty rather than two
// flags one person sets.
//
// launch.Config carries the policy, and a Config is built by callers. If
// the dispatcher merely defaulted it rather than overwriting it, anybody
// who could construct a Config could grant themselves the deployment's
// half, which is exactly one of the two gates.
func TestLaunchTemplate_ACallerCannotSupplyTheDeploymentsConsent(t *testing.T) {
	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), newTestJobStore(t), newCapturingBus(),
		api.WithTemplates(stubTemplates{tmpl: fileQuestionTemplate(true)}),
		api.WithLaunchConfigs(&recordingConfigs{}))
	// No WithSurveyFilePolicy: this deployment has not consented.

	_, _, err := dispatcher.LaunchTemplate(context.Background(), "mallory@example.com", 12, launch.Config{
		Answers: map[string]any{"bootstrap": "#!/bin/sh\nsetup\n"},
		// The caller asserts the deployment's consent. It must be
		// overwritten rather than honoured.
		FilePolicy: launch.FilePolicy{AllowProgramContent: true},
	}, nil)

	if err == nil {
		t.Fatal("a caller supplied the deployment's consent in its own Config and the launch was accepted")
	}
	if !errors.Is(err, launch.ErrProgramContent) {
		t.Fatalf("LaunchTemplate error = %v, want ErrProgramContent", err)
	}
}

// TestLaunchTemplate_TheDeploymentGateIsALiveKillSwitch proves the
// deployment's half is consulted at LAUNCH rather than only when the
// question was authored.
//
// This is the difference between a gate and a lint. A template carrying the
// flag was authored while the deployment consented; turning that consent
// off has to stop the template launching, not merely stop new ones being
// written. Here the template is identical in both halves and only the
// Controller's configuration differs.
func TestLaunchTemplate_TheDeploymentGateIsALiveKillSwitch(t *testing.T) {
	armed := fileQuestionTemplate(true)
	answers := launch.Config{Answers: map[string]any{"bootstrap": "#!/bin/sh\nsetup\n"}}

	consenting := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), newTestJobStore(t), newCapturingBus(),
		api.WithTemplates(stubTemplates{tmpl: armed}),
		api.WithLaunchConfigs(&recordingConfigs{}),
		api.WithSurveyFilePolicy(launch.FilePolicy{AllowProgramContent: true}))
	if _, _, err := consenting.LaunchTemplate(context.Background(), "ada@example.com", 12, answers, nil); err != nil {
		t.Fatalf("the consenting deployment refused the launch: %v", err)
	}

	// Same template, same answer, a Controller started without the
	// variable.
	withdrawn := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), newTestJobStore(t), newCapturingBus(),
		api.WithTemplates(stubTemplates{tmpl: armed}))
	_, _, err := withdrawn.LaunchTemplate(context.Background(), "ada@example.com", 12, answers, nil)
	if err == nil {
		t.Fatal("withdrawing the deployment's consent did not stop a template that already carried the flag")
	}
	if !errors.Is(err, launch.ErrProgramContent) {
		t.Fatalf("LaunchTemplate error = %v, want ErrProgramContent", err)
	}
}

// TestLaunchTemplate_BinaryAndOversizeFileAnswersAreRefused proves the
// gates govern one class rather than the type: opening the deployment's
// consent changes who decided, never what may be uploaded.
func TestLaunchTemplate_BinaryAndOversizeFileAnswersAreRefused(t *testing.T) {
	cases := []struct {
		name   string
		answer string
		want   string
	}{
		{"an ELF header", "\x7fELF\x02\x01\x01\x00", "must be a text file"},
		{"a UTF-16 export", "\xff\xfeh\x00o\x00s\x00t\x00", "must be a text file"},
		{"over the size bound", strings.Repeat("a", launch.MaxFileAnswerBytes+1), "at most"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Both gates open, so a refusal here cannot be the program
			// rule firing.
			dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), newTestJobStore(t), newCapturingBus(),
				api.WithTemplates(stubTemplates{tmpl: fileQuestionTemplate(true)}),
				api.WithLaunchConfigs(&recordingConfigs{}),
				api.WithSurveyFilePolicy(launch.FilePolicy{AllowProgramContent: true}))

			_, _, err := dispatcher.LaunchTemplate(context.Background(), "ada@example.com", 12,
				launch.Config{Answers: map[string]any{"bootstrap": tc.answer}}, nil)
			if err == nil {
				t.Fatalf("%s was accepted with both gates open", tc.name)
			}
			if errors.Is(err, launch.ErrProgramContent) {
				t.Fatalf("%s was refused as program content, which implies a gate could admit it: %v", tc.name, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not say %q: %v", tc.want, err)
			}
		})
	}
}

// TestDispatcher_AllowsProgramContentIsNilSafe guards the accessor the UI
// calls to decide whether to draw a control.
//
// The UI legitimately holds a nil *Dispatcher in some compositions, and a
// nil one must report the refusal rather than panic: a nil check that was
// forgotten would take out the template page rather than the feature.
func TestDispatcher_AllowsProgramContentIsNilSafe(t *testing.T) {
	var d *api.Dispatcher
	if d.AllowsProgramContent() {
		t.Fatal("a nil Dispatcher reports the deployment as consenting")
	}
	if api.NewDispatcher(nil, nil, nil).AllowsProgramContent() {
		t.Fatal("a Dispatcher built without the option reports the deployment as consenting")
	}
}
