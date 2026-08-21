// This file covers Dispatcher.LaunchScheduled: the adapter that lets
// internal/schedule reach the dispatch plane without importing this
// package.
//
// The property worth asserting is that a scheduled run is not a parallel
// launch path. It resolves the same template, records the same job, and
// carries an actor naming the schedule that caused it, so an unexpected job
// can be traced back to its cause.
package api_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/schedule"
)

func TestLaunchScheduled_CreatesAJobAttributedToTheSchedule(t *testing.T) {
	jobs := newTestJobStore(t)
	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), jobs, newCapturingBus(),
		api.WithTemplates(stubTemplates{tmpl: launchableTemplate()}))

	actor := schedule.ScheduleActor("sched-123")
	jobID, err := dispatcher.LaunchScheduled(context.Background(), actor, 12, 0)
	if err != nil {
		t.Fatalf("LaunchScheduled: %v", err)
	}
	if jobID == "" {
		t.Fatal("LaunchScheduled returned no job id")
	}

	job, _, err := jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if job.Actor != actor {
		t.Errorf("job actor = %q, want %q so the audit trail names the schedule", job.Actor, actor)
	}
	if !strings.HasPrefix(job.Actor, "scheduler:") {
		t.Errorf("actor %q does not identify itself as a scheduled run", job.Actor)
	}
	// It resolved the template rather than inventing a dispatch: the
	// tenant, the inventory and the definition all come off the template.
	if job.TemplateID != 12 || job.OrganizationID != 3 || job.InventoryID != 7 {
		t.Errorf("job = %+v, want the template's own template/organization/inventory", job)
	}
}

// TestLaunchScheduled_AppliesASavedConfiguration proves the saved bundle a
// schedule points at actually reaches the launch, rather than being
// accepted and dropped.
func TestLaunchScheduled_AppliesASavedConfiguration(t *testing.T) {
	configs := &recordingConfigs{}
	saved, err := configs.SaveConfig(context.Background(), launch.SavedConfig{
		TemplateID: 12,
		Fields:     launch.Fields{"limit": "core-*"},
	})
	if err != nil {
		t.Fatal(err)
	}

	jobs := newTestJobStore(t)
	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), jobs, newCapturingBus(),
		api.WithTemplates(stubTemplates{tmpl: launchableTemplate()}),
		api.WithLaunchConfigs(configs))

	jobID, err := dispatcher.LaunchScheduled(context.Background(),
		schedule.ScheduleActor("sched-123"), 12, saved.ID)
	if err != nil {
		t.Fatalf("LaunchScheduled: %v", err)
	}

	job, _, err := jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := job.Fields["limit"]; got != "core-*" {
		t.Errorf("limit = %v, want the saved configuration's own value", got)
	}
}

// TestLaunchScheduled_RefusesAConfigurationFromAnotherTemplate covers the
// cross-template check, which is a tenancy boundary and not bookkeeping: a
// configuration carries survey answers meaningful only against the template
// whose questions produced them.
func TestLaunchScheduled_RefusesAConfigurationFromAnotherTemplate(t *testing.T) {
	configs := &recordingConfigs{}
	saved, err := configs.SaveConfig(context.Background(), launch.SavedConfig{
		TemplateID: 999,
		Fields:     launch.Fields{"limit": "somebody-elses-*"},
	})
	if err != nil {
		t.Fatal(err)
	}

	jobs := newTestJobStore(t)
	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), jobs, newCapturingBus(),
		api.WithTemplates(stubTemplates{tmpl: launchableTemplate()}),
		api.WithLaunchConfigs(configs))

	if _, err := dispatcher.LaunchScheduled(context.Background(),
		schedule.ScheduleActor("sched-123"), 12, saved.ID); err == nil {
		t.Fatal("a configuration belonging to another template was accepted")
	}
	if listed, err := jobs.List(context.Background(), "", 10); err != nil {
		t.Fatal(err)
	} else if len(listed) != 0 {
		t.Errorf("%d jobs were created despite the refusal", len(listed))
	}
}

func TestLaunchScheduled_ReportsAnUnresolvableTemplate(t *testing.T) {
	jobs := newTestJobStore(t)
	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), jobs, newCapturingBus(),
		api.WithTemplates(stubTemplates{err: launch.ErrNotFound}))

	_, err := dispatcher.LaunchScheduled(context.Background(),
		schedule.ScheduleActor("sched-123"), 12, 0)
	if !errors.Is(err, launch.ErrNotFound) {
		t.Errorf("LaunchScheduled = %v, want it to report the missing template", err)
	}
	if listed, err := jobs.List(context.Background(), "", 10); err != nil {
		t.Fatal(err)
	} else if len(listed) != 0 {
		t.Error("a job was created for a template that does not exist")
	}
}

// TestLaunchScheduled_RefusesWhenTemplatesAreNotWired covers the guard that
// keeps a half-configured controller from failing later and less clearly.
func TestLaunchScheduled_RefusesWhenTemplatesAreNotWired(t *testing.T) {
	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), newTestJobStore(t), newCapturingBus())

	if _, err := dispatcher.LaunchScheduled(context.Background(),
		schedule.ScheduleActor("sched-123"), 12, 0); err == nil {
		t.Fatal("launching by template with no template store wired was accepted")
	}
}

// TestLaunchScheduled_SatisfiesTheSchedulerPort is the wiring assertion.
//
// internal/schedule declares a one-method Launcher rather than importing
// this package, so nothing in the compiler checks the two agree unless
// somebody says so. cmd/controller passes a *Dispatcher where a
// schedule.Launcher is wanted; this fails at build time if that stops being
// true, which is cheaper than discovering it in a release gate.
func TestLaunchScheduled_SatisfiesTheSchedulerPort(t *testing.T) {
	var _ schedule.Launcher = (*api.Dispatcher)(nil)
}

// TestLaunchScheduled_RefusesACredentialThatPromptsAtLaunch is a security
// assertion, not a completeness one.
//
// A prompted credential input is never stored, so a schedule bound to one
// could only ever fail. Refusing it at the launch is what turns "this job
// mysteriously fails every night" into a refusal somebody can read, and it
// mirrors exactly what Relaunch already refuses for the same reason.
func TestLaunchScheduled_RefusesACredentialThatPromptsAtLaunch(t *testing.T) {
	jobs := newTestJobStore(t)
	bound := launchableTemplate()
	bound.CredentialIDs = []int{18}

	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), jobs, newCapturingBus(),
		api.WithTemplates(stubTemplates{tmpl: bound}),
		api.WithCredentialReader(stubCredentialReader{
			bound: []credstore.Credential{{ID: 18, Name: "prod api", TypeID: 4}},
			types: map[int]credstore.CredentialType{4: promptingType()},
		}))

	_, err := dispatcher.LaunchScheduled(context.Background(),
		schedule.ScheduleActor("sched-123"), 12, 0)
	if err == nil {
		t.Fatal("a schedule bound to a prompted credential was accepted; it could never run")
	}
	if listed, listErr := jobs.List(context.Background(), "", 10); listErr != nil {
		t.Fatal(listErr)
	} else if len(listed) != 0 {
		t.Error("a job was created for a schedule that cannot supply its credential")
	}

	// The same template with an ordinary stored credential is fine, which
	// is what makes the refusal specific rather than a blanket ban on
	// scheduling anything with credentials.
	ok := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), newTestJobStore(t), newCapturingBus(),
		api.WithTemplates(stubTemplates{tmpl: bound}),
		api.WithCredentialReader(stubCredentialReader{
			bound: []credstore.Credential{{ID: 19, Name: "stored api", TypeID: 5}},
			types: map[int]credstore.CredentialType{5: storingType()},
		}))
	if _, err := ok.LaunchScheduled(context.Background(),
		schedule.ScheduleActor("sched-123"), 12, 0); err != nil {
		t.Errorf("a schedule bound to a stored credential was refused: %v", err)
	}
}

// TestLaunchScheduled_RefusesToReplayASavedSurveyPassword is the other
// security assertion, and the stronger of the two.
//
// Relaunch already refuses to replay a stored survey password: the value
// was typed once by one operator, and repeating it would let somebody cause
// a secret they have never seen to be used again under their own name. On a
// schedule the same replay happens unattended, on every occurrence,
// indefinitely, under nobody's decision at all.
func TestLaunchScheduled_RefusesToReplayASavedSurveyPassword(t *testing.T) {
	asking := launchableTemplate()
	asking.Survey = launch.Survey{
		Enabled: true,
		Questions: []launch.Question{
			{Variable: "vault_password", Label: "Vault password", Type: launch.QuestionPassword},
		},
	}

	configs := &recordingConfigs{}
	saved, err := configs.SaveConfig(context.Background(), launch.SavedConfig{
		TemplateID: 12,
		Answers:    map[string]any{"vault_password": "hunter2"},
	})
	if err != nil {
		t.Fatal(err)
	}

	jobs := newTestJobStore(t)
	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), jobs, newCapturingBus(),
		api.WithTemplates(stubTemplates{tmpl: asking}),
		api.WithLaunchConfigs(configs))

	_, err = dispatcher.LaunchScheduled(context.Background(),
		schedule.ScheduleActor("sched-123"), 12, saved.ID)
	if err == nil {
		t.Fatal("a schedule replaying a saved survey password was accepted")
	}
	if !strings.Contains(err.Error(), "vault_password") {
		t.Errorf("the refusal does not name the answer at fault: %v", err)
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("the refusal echoed the secret it refused to replay: %v", err)
	}
	if listed, listErr := jobs.List(context.Background(), "", 10); listErr != nil {
		t.Fatal(listErr)
	} else if len(listed) != 0 {
		t.Error("a job was created despite the refusal")
	}

	// A non-secret answer in the same saved configuration is replayed
	// normally, so the refusal is about secrecy rather than about surveys.
	plain := launchableTemplate()
	plain.Survey = launch.Survey{
		Enabled:   true,
		Questions: []launch.Question{{Variable: "release", Label: "Release", Type: launch.QuestionText}},
	}
	plainConfigs := &recordingConfigs{}
	plainSaved, err := plainConfigs.SaveConfig(context.Background(), launch.SavedConfig{
		TemplateID: 12,
		Answers:    map[string]any{"release": "2024.3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	okDispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), newTestJobStore(t), newCapturingBus(),
		api.WithTemplates(stubTemplates{tmpl: plain}),
		api.WithLaunchConfigs(plainConfigs))
	if _, err := okDispatcher.LaunchScheduled(context.Background(),
		schedule.ScheduleActor("sched-123"), 12, plainSaved.ID); err != nil {
		t.Errorf("a schedule replaying an ordinary saved answer was refused: %v", err)
	}
}
