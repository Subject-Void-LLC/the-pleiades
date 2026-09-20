// This file covers the Dispatcher as internal/launchable.Launcher: how a job
// template is run when the caller holds a launchable reference, which is what
// a schedule stores.
//
// Two properties are worth asserting. A scheduled run is not a parallel launch
// path: it resolves the same template, records the same job, and carries an
// actor naming the schedule that caused it, so an unexpected job can be traced
// back to its cause. And every refusal Launch makes, Preflight makes too, so a
// schedule that could only ever fail is refused at the write rather than
// silently at three in the morning.
package api_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launchable"
	"github.com/Subject-Void-LLC/the-pleiades/internal/schedule"
)

// scheduledRequest is what a schedule hands the Dispatcher: the launchable row
// standing for a template (id 44 here, deliberately not the template's own 12,
// so a test cannot pass by confusing the two id spaces), the schedule's actor,
// and optionally a saved configuration.
func scheduledRequest(actor string, savedConfigID int) launchable.Request {
	return launchable.Request{
		Target: launchable.Target{
			ID: 44, Type: launchable.TypeJobTemplate,
			Name: "patch the edge routers", OrganizationID: 3,
		},
		Actor:         actor,
		SavedConfigID: savedConfigID,
	}
}

func TestDispatcherLaunch_CreatesAJobAttributedToTheSchedule(t *testing.T) {
	jobs := newTestJobStore(t)
	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), jobs, newCapturingBus(),
		api.WithTemplates(stubTemplates{tmpl: launchableTemplate()}))

	actor := schedule.ScheduleActor("sched-123")
	launched, err := dispatcher.Launch(context.Background(), scheduledRequest(actor, 0))
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if launched.RunID == "" {
		t.Fatal("Launch returned no job id")
	}

	job, _, err := jobs.Get(context.Background(), launched.RunID)
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

// TestDispatcherLaunch_AppliesASavedConfiguration proves the saved bundle a
// schedule points at actually reaches the launch, rather than being
// accepted and dropped.
func TestDispatcherLaunch_AppliesASavedConfiguration(t *testing.T) {
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

	launched, err := dispatcher.Launch(context.Background(),
		scheduledRequest(schedule.ScheduleActor("sched-123"), saved.ID))
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	job, _, err := jobs.Get(context.Background(), launched.RunID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got := job.Fields["limit"]; got != "core-*" {
		t.Errorf("limit = %v, want the saved configuration's own value", got)
	}
}

// TestDispatcherLaunch_RefusesAConfigurationFromAnotherTemplate covers the
// cross-template check, which is a tenancy boundary and not bookkeeping: a
// configuration carries survey answers meaningful only against the template
// whose questions produced them.
func TestDispatcherLaunch_RefusesAConfigurationFromAnotherTemplate(t *testing.T) {
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

	if _, err := dispatcher.Launch(context.Background(),
		scheduledRequest(schedule.ScheduleActor("sched-123"), saved.ID)); err == nil {
		t.Fatal("a configuration belonging to another template was accepted")
	}
	if listed, err := jobs.List(context.Background(), "", 10); err != nil {
		t.Fatal(err)
	} else if len(listed) != 0 {
		t.Errorf("%d jobs were created despite the refusal", len(listed))
	}
}

func TestDispatcherLaunch_ReportsAnUnresolvableTemplate(t *testing.T) {
	jobs := newTestJobStore(t)
	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), jobs, newCapturingBus(),
		api.WithTemplates(stubTemplates{err: launch.ErrNotFound}))

	_, err := dispatcher.Launch(context.Background(),
		scheduledRequest(schedule.ScheduleActor("sched-123"), 0))
	if !errors.Is(err, launch.ErrNotFound) {
		t.Errorf("Launch = %v, want it to report the missing template", err)
	}
	if listed, err := jobs.List(context.Background(), "", 10); err != nil {
		t.Fatal(err)
	} else if len(listed) != 0 {
		t.Error("a job was created for a template that does not exist")
	}
}

// TestDispatcherLaunch_RefusesWhenTemplatesAreNotWired covers the guard that
// keeps a half-configured controller from failing later and less clearly.
func TestDispatcherLaunch_RefusesWhenTemplatesAreNotWired(t *testing.T) {
	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), newTestJobStore(t), newCapturingBus())

	if _, err := dispatcher.Launch(context.Background(),
		scheduledRequest(schedule.ScheduleActor("sched-123"), 0)); err == nil {
		t.Fatal("launching by template with no template store wired was accepted")
	}
}

// TestDispatcherLaunch_SatisfiesEveryPortItIsComposedInto is the wiring
// assertion.
//
// Three interfaces, none of which this package imports the declarer of, so
// nothing in the compiler checks they agree unless somebody says so:
// cmd/controller passes a *Dispatcher as a launchable.Launcher, the router
// asks it for a Preflighter, and internal/schedule's own narrow Launcher is
// what the scanner holds. Failing here at build time is cheaper than
// discovering it in a release gate.
func TestDispatcherLaunch_SatisfiesEveryPortItIsComposedInto(t *testing.T) {
	var (
		_ launchable.Launcher    = (*api.Dispatcher)(nil)
		_ launchable.Preflighter = (*api.Dispatcher)(nil)
		_ schedule.Launcher      = (*api.Dispatcher)(nil)
	)
}

// TestDispatcherLaunch_RefusesACredentialThatPromptsAtLaunch is a security
// assertion, not a completeness one.
//
// A prompted credential input is never stored, so a schedule bound to one
// could only ever fail. Refusing it at the launch is what turns "this job
// mysteriously fails every night" into a refusal somebody can read, and it
// mirrors exactly what Relaunch already refuses for the same reason.
func TestDispatcherLaunch_RefusesACredentialThatPromptsAtLaunch(t *testing.T) {
	jobs := newTestJobStore(t)
	bound := launchableTemplate()
	bound.CredentialIDs = []int{18}

	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), jobs, newCapturingBus(),
		api.WithTemplates(stubTemplates{tmpl: bound}),
		api.WithCredentialReader(stubCredentialReader{
			bound: []credstore.Credential{{ID: 18, Name: "prod api", TypeID: 4}},
			types: map[int]credstore.CredentialType{4: promptingType()},
		}))

	_, err := dispatcher.Launch(context.Background(),
		scheduledRequest(schedule.ScheduleActor("sched-123"), 0))
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
	if _, err := ok.Launch(context.Background(),
		scheduledRequest(schedule.ScheduleActor("sched-123"), 0)); err != nil {
		t.Errorf("a schedule bound to a stored credential was refused: %v", err)
	}
}

// TestDispatcherLaunch_RefusesToReplayASavedSurveyPassword is the other
// security assertion, and the stronger of the two.
//
// Relaunch already refuses to replay a stored survey password: the value
// was typed once by one operator, and repeating it would let somebody cause
// a secret they have never seen to be used again under their own name. On a
// schedule the same replay happens unattended, on every occurrence,
// indefinitely, under nobody's decision at all.
func TestDispatcherLaunch_RefusesToReplayASavedSurveyPassword(t *testing.T) {
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

	_, err = dispatcher.Launch(context.Background(),
		scheduledRequest(schedule.ScheduleActor("sched-123"), saved.ID))
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
	if _, err := okDispatcher.Launch(context.Background(),
		scheduledRequest(schedule.ScheduleActor("sched-123"), plainSaved.ID)); err != nil {
		t.Errorf("a schedule replaying an ordinary saved answer was refused: %v", err)
	}
}

// TestDispatcherLaunch_ReportsAFailureResolvingTheTemplate separates the two
// ways a template can fail to resolve.
//
// A template that is gone is a refusal naming the control, because somebody has
// to repoint or delete the schedule. A storage failure is not: there is nothing
// for them to change, so it travels as an error rather than as advice.
func TestDispatcherLaunch_ReportsAFailureResolvingTheTemplate(t *testing.T) {
	broken := errors.New("reading template 12: database is locked")
	jobs := newTestJobStore(t)
	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), jobs, newCapturingBus(),
		api.WithTemplates(stubTemplates{err: broken}))

	_, err := dispatcher.Launch(context.Background(),
		scheduledRequest(schedule.ScheduleActor("sched-123"), 0))
	if !errors.Is(err, broken) {
		t.Fatalf("Launch = %v, want the storage failure itself", err)
	}
	var refusal launchable.Refusal
	if errors.As(err, &refusal) {
		t.Errorf("a storage failure was reported as a refusal about %q", refusal.Field)
	}
	if listed, listErr := jobs.List(context.Background(), "", 10); listErr != nil {
		t.Fatal(listErr)
	} else if len(listed) != 0 {
		t.Error("a job was created despite the failure")
	}
}

// TestDispatcherLaunch_ReportsAFailureRecordingTheJob covers the last step of an
// unattended launch: the template resolved, every refusal passed, and the job
// could not be written.
//
// It matters that this is an error rather than a silent success, because the
// caller is a schedule: an occurrence that recorded a fired job which does not
// exist would be a history nobody can follow, and the scheduler's own answer to
// a failed launch (a skip carrying the reason) depends on hearing about it.
func TestDispatcherLaunch_ReportsAFailureRecordingTheJob(t *testing.T) {
	broken := errors.New("writing the job: database is locked")
	jobs := &erroringJobStore{JobStore: newTestJobStore(t), err: broken}
	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), jobs, newCapturingBus(),
		api.WithTemplates(stubTemplates{tmpl: launchableTemplate()}))

	launched, err := dispatcher.Launch(context.Background(),
		scheduledRequest(schedule.ScheduleActor("sched-123"), 0))
	if err == nil {
		t.Fatal("a launch whose job could not be recorded was reported as succeeding")
	}
	if launched.RunID != "" {
		t.Errorf("the failed launch reported run %q, want nothing to point at", launched.RunID)
	}
}
