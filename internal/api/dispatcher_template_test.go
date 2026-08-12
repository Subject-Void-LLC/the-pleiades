package api_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds"
)

// This file covers launching by template, which is what replaces naming a
// group and a runbook in a query string.
//
// The headline assertion is tenancy. Job.organization_id has existed in the
// schema since Phase 14 with nothing anywhere writing it, because a
// free-text group name has no tenant to inherit. A template names an
// inventory, an inventory carries a required organization edge, so a job
// launched from a template has an owner by construction. That is the whole
// mechanism, and this is where it is proved end to end through the real
// dispatcher and a real job store.

// erroringJobStore refuses every write. A double, and the narrow kind this
// project allows: the behaviour under test is what the launch path does
// when persistence fails, which a real store cannot be made to do on demand
// without also breaking the assertion.
type erroringJobStore struct {
	dispatch.JobStore
	err error
}

func (s *erroringJobStore) Create(context.Context, *dispatch.Job) error { return s.err }

// Get refuses too, so a relaunch can be shown to answer 500 rather than
// 404: a store that cannot answer is not the same fact as a job that is not
// there, and telling a caller their job had been deleted would be worse
// than telling them nothing.
func (s *erroringJobStore) Get(context.Context, string) (*dispatch.Job, []dispatch.JobTask, error) {
	return nil, nil, s.err
}

// erroringBus refuses every publish, for the same reason.
type erroringBus struct {
	event.Bus
	err error
}

func (b *erroringBus) Publish(context.Context, string, event.Event) error { return b.err }

// recordingConfigs is the narrow write port a launch records what it was
// configured with through.
//
// A double, and the narrow kind this project allows: the behaviour under
// test here is which values reach a resolved launch and which are reported,
// and the real store would need the template these tests deliberately do
// not persist. templates_test.go asserts the same path against the real
// ent-backed store, so nothing about storage is being taken on trust.
type recordingConfigs struct {
	saved []launch.SavedConfig
	next  int
}

func (c *recordingConfigs) SaveConfig(_ context.Context, cfg launch.SavedConfig) (launch.SavedConfig, error) {
	c.next++
	cfg.ID = c.next
	c.saved = append(c.saved, cfg)
	return cfg, nil
}

func (c *recordingConfigs) GetConfig(_ context.Context, id int) (launch.SavedConfig, error) {
	for _, cfg := range c.saved {
		if cfg.ID == id {
			return cfg, nil
		}
	}
	return launch.SavedConfig{}, launch.ErrNotFound
}

// stubTemplates is the narrow read port LaunchTemplate takes.
type stubTemplates struct {
	tmpl launch.Template
	err  error
}

func (s stubTemplates) Get(context.Context, int) (launch.Template, error) {
	return s.tmpl, s.err
}

// launchableTemplate is a runbook template with two fields open and two
// locked, against inventory 7 in organization 3.
func launchableTemplate() launch.Template {
	return launch.Template{
		ID:             12,
		Name:           "patch the edge routers",
		KindName:       "runbook",
		Definition:     "pb-1",
		InventoryID:    7,
		OrganizationID: 3,
		Defaults:       launch.Fields{"limit": "edge-*", "forks": 5},
		Prompts:        []string{"limit"},
	}
}

func TestLaunchTemplate_WritesTheTenantAJobInheritsFromItsInventory(t *testing.T) {
	jobs := newTestJobStore(t)
	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), jobs, newCapturingBus(),
		api.WithTemplates(stubTemplates{tmpl: launchableTemplate()}))

	jobID, ignored, err := dispatcher.LaunchTemplate(context.Background(), "ada@example.com", 12, launch.Config{})
	if err != nil {
		t.Fatalf("LaunchTemplate: %v", err)
	}
	if len(ignored) != 0 {
		t.Fatalf("a launch supplying nothing reported %+v as ignored", ignored)
	}

	job, _, err := jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// The line this phase exists for.
	if job.OrganizationID != 3 {
		t.Errorf("the job belongs to organization %d, want the template's 3: "+
			"Job.organization_id has had no writer since Phase 14", job.OrganizationID)
	}
	if job.InventoryID != 7 {
		t.Errorf("the job targets inventory %d, want the template's 7", job.InventoryID)
	}
	if job.TemplateID != 12 || job.TemplateName != "patch the edge routers" {
		t.Errorf("the job records template %d/%q, want the one it was launched from", job.TemplateID, job.TemplateName)
	}
	// The kind travels so the Runner routes on a value it was given.
	if job.Kind != "runbook" {
		t.Errorf("the job records kind %q, want runbook", job.Kind)
	}
	// And no group name, because nothing names a group any more.
	if job.GroupName != "" {
		t.Errorf("the job carries group %q, which no launch path sets", job.GroupName)
	}
}

func TestLaunchTemplate_ReportsWhatItRefusedRatherThanRefusingTheLaunch(t *testing.T) {
	jobs := newTestJobStore(t)
	configs := &recordingConfigs{}
	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), jobs, newCapturingBus(),
		api.WithTemplates(stubTemplates{tmpl: launchableTemplate()}), api.WithLaunchConfigs(configs))

	jobID, ignored, err := dispatcher.LaunchTemplate(context.Background(), "ada@example.com", 12, launch.Config{
		Overrides: launch.Fields{"limit": "edge-01", "forks": 100},
	})
	if err != nil {
		t.Fatalf("LaunchTemplate: %v", err)
	}

	// What was recorded is what the caller supplied, including the locked
	// field. The record is of the launch, not of the resolution: a relaunch
	// resolves it again through the same gate, so trimming it here would
	// make the stored configuration disagree with the request that produced
	// it for no gain.
	if len(configs.saved) != 1 || configs.saved[0].TemplateID != 12 {
		t.Fatalf("the launch recorded %+v, want one configuration against template 12", configs.saved)
	}

	// The launch happened. A locked field is not a failure: refusing the
	// whole launch would make a template author's decision look like the
	// operator's mistake.
	if jobID == "" {
		t.Fatal("a launch supplying one locked field produced no job")
	}
	if len(ignored) != 1 || ignored[0].Name != "forks" {
		t.Fatalf("LaunchTemplate reported %+v, want forks alone", ignored)
	}
	if ignored[0].Reason != launch.ReasonLocked {
		t.Errorf("forks was ignored for reason %q, want the locked reason", ignored[0].Reason)
	}
}

func TestLaunchTemplate_RefusesWhatNoConfigurationCouldFix(t *testing.T) {
	jobs := newTestJobStore(t)

	// A survey answer that violates its own schema is a failure rather
	// than an ignored field, and the difference is who decided: a locked
	// field is the template author's decision, an invalid answer means the
	// run would proceed with a variable set the author said was not
	// acceptable.
	surveyed := launchableTemplate()
	surveyed.Survey = launch.Survey{Enabled: true, Questions: []launch.Question{
		{Variable: "version", Type: launch.QuestionChoice, Required: true, Choices: []string{"17.3"}},
	}}
	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), jobs, newCapturingBus(),
		api.WithTemplates(stubTemplates{tmpl: surveyed}))

	if _, _, err := dispatcher.LaunchTemplate(context.Background(), "ada@example.com", 12, launch.Config{
		Answers: map[string]any{"version": "99.9"},
	}); !errors.Is(err, launch.ErrSurveyAnswer) {
		t.Errorf("LaunchTemplate with an invalid answer returned %v, want ErrSurveyAnswer", err)
	}

	// And nothing was persisted, so a refused launch leaves no job behind
	// for somebody to find in "pending" forever.
	if _, _, err := jobs.Get(context.Background(), "any"); err == nil {
		t.Error("a refused launch persisted a job")
	}
}

func TestLaunchTemplate_RefusesWhenTheTemplatePortIsNotWired(t *testing.T) {
	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), newTestJobStore(t), newCapturingBus())

	// A convenience for existing harnesses is not a permission. Falling
	// back to a launch that names no template would produce a job with no
	// tenant and no kind, which is the state this phase exists to end.
	if _, _, err := dispatcher.LaunchTemplate(context.Background(), "ada@example.com", 12, launch.Config{}); err == nil {
		t.Error("LaunchTemplate succeeded with no template port wired")
	}
}

func TestLaunchTemplate_ReportsAMissingTemplate(t *testing.T) {
	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), newTestJobStore(t), newCapturingBus(),
		api.WithTemplates(stubTemplates{err: launch.ErrNotFound}))

	if _, _, err := dispatcher.LaunchTemplate(context.Background(), "ada@example.com", 99, launch.Config{}); !errors.Is(err, launch.ErrNotFound) {
		t.Errorf("LaunchTemplate of a missing template returned %v, want ErrNotFound", err)
	}
}

func TestLaunchTemplate_ReportsAFailedPersistAndAFailedPublish(t *testing.T) {
	// A job store that refuses. The launch must not answer with a job id
	// for a job that was never saved: a caller that then polled it would
	// get a 404 for a dispatch it was told had been accepted.
	failingJobs := &erroringJobStore{err: errors.New("the database is unreachable")}
	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), failingJobs, newCapturingBus(),
		api.WithTemplates(stubTemplates{tmpl: launchableTemplate()}))

	jobID, _, err := dispatcher.LaunchTemplate(context.Background(), "ada@example.com", 12, launch.Config{})
	if err == nil {
		t.Error("LaunchTemplate reported success when the job could not be persisted")
	}
	if jobID != "" {
		t.Errorf("LaunchTemplate returned job id %q for a job that was never saved", jobID)
	}

	// And a bus that refuses. Here the job IS persisted, so the failure
	// leaves a durable record stuck in "pending" rather than a silent
	// nothing, which is the ordering the launch path is built around: the
	// record exists before anything is told to look one up.
	jobs := newTestJobStore(t)
	dispatcher = api.NewDispatcher(newTestRunbookSource(t, "pb-1"), jobs, &erroringBus{err: errors.New("nats is down")},
		api.WithTemplates(stubTemplates{tmpl: launchableTemplate()}))

	if _, _, err := dispatcher.LaunchTemplate(context.Background(), "ada@example.com", 12, launch.Config{}); err == nil {
		t.Error("LaunchTemplate reported success when the event could not be published")
	}
}

func TestLaunchTemplate_RefusesToLaunchWithAConfigurationItCannotRecord(t *testing.T) {
	jobs := newTestJobStore(t)

	// No configuration store wired, and a launch that supplied something.
	// Launching anyway would persist a job that ran with overrides and has
	// no record of them, which reads as a job launched with the template's
	// defaults: a lie about what ran, told to whoever reads it next.
	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), jobs, newCapturingBus(),
		api.WithTemplates(stubTemplates{tmpl: launchableTemplate()}))

	if _, _, err := dispatcher.LaunchTemplate(context.Background(), "ada@example.com", 12, launch.Config{
		Overrides: launch.Fields{"limit": "edge-01"},
	}); err == nil {
		t.Error("LaunchTemplate recorded nothing and reported success")
	}

	// A launch that supplied nothing has nothing to record, so it is
	// unaffected: the refusal is about losing information, not about the
	// port being absent.
	if _, _, err := dispatcher.LaunchTemplate(context.Background(), "ada@example.com", 12, launch.Config{}); err != nil {
		t.Errorf("LaunchTemplate with nothing to record: %v", err)
	}
}
