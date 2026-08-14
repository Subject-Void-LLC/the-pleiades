package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credstore"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	_ "github.com/Subject-Void-LLC/the-pleiades/internal/launch/kinds"
	"github.com/Subject-Void-LLC/the-pleiades/internal/topology"
)

// The launch path's credential behaviour: what is recorded, what travels on
// the event, and what a relaunch refuses.

// stubCredentialReader is the narrow port a relaunch consults.
//
// A double, and the narrow kind this project allows: what is under test is
// the launch path's own decision, and the real ent-backed store has its own
// tests. It returns credstore's REDACTED projection, which is the only
// shape this package can hold at all.
type stubCredentialReader struct {
	bound []credstore.Credential
	types map[int]credstore.CredentialType
	err   error
}

func (s stubCredentialReader) TemplateCredentials(context.Context, int) ([]credstore.Credential, error) {
	return s.bound, s.err
}

func (s stubCredentialReader) GetType(_ context.Context, id int) (credstore.CredentialType, error) {
	ct, ok := s.types[id]
	if !ok {
		return credstore.CredentialType{}, credstore.ErrNotFound
	}
	return ct, nil
}

// promptingType is a credential type that asks for a value at launch and
// never stores it.
func promptingType() credstore.CredentialType {
	return credstore.CredentialType{
		ID: 4,
		CredentialType: credtype.CredentialType{
			Name: "Prompted API Token", Kind: credtype.KindCloud, Namespace: "prompted_api",
			Inputs: credtype.InputSchema{Fields: []credtype.InputField{
				{ID: "api_token", Label: "Token", Secret: true, AskAtRuntime: true},
			}},
		},
	}
}

// storingType is an ordinary type whose values are all stored.
func storingType() credstore.CredentialType {
	return credstore.CredentialType{
		ID: 5,
		CredentialType: credtype.CredentialType{
			Name: "Stored API Token", Kind: credtype.KindCloud, Namespace: "stored_api",
			Inputs: credtype.InputSchema{Fields: []credtype.InputField{
				{ID: "api_token", Label: "Token", Secret: true},
			}},
		},
	}
}

// TestLaunchRecordsTheCredentialIdsAndNothingElse covers what a job carries
// about its credentials, and the negative half is the important one.
//
// The ids are the audit answer to what a run authenticated as. Nothing
// else about a credential belongs on a job row, and above all no value:
// injection happens at fan-out precisely so a rendered secret never reaches
// this table.
func TestLaunchRecordsTheCredentialIdsAndNothingElse(t *testing.T) {
	jobs := newTestJobStore(t)
	tmpl := launchableTemplate()
	tmpl.CredentialIDs = []int{18, 9}

	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), jobs, newCapturingBus(),
		api.WithTemplates(stubTemplates{tmpl: tmpl}))

	jobID, _, err := dispatcher.LaunchTemplate(context.Background(), "ada@example.com", 12, launchConfig(), nil)
	if err != nil {
		t.Fatalf("LaunchTemplate returned unexpected error: %v", err)
	}

	job, _, err := jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatalf("Get returned unexpected error: %v", err)
	}
	if len(job.CredentialIDs) != 2 || job.CredentialIDs[0] != 18 || job.CredentialIDs[1] != 9 {
		t.Errorf("CredentialIDs = %v, want the template's own binding order", job.CredentialIDs)
	}
}

// TestRecordConfigCannotSeeAPromptedCredentialInput is the never-persist
// rule, and it is a test rather than a comment because "there is nothing to
// redact" is a claim.
//
// The structural half is the signature: LaunchTemplate takes prompted as
// its own parameter and recordConfig takes only launch.Config, so the
// storing function is not handed the value that must not be stored. This is
// the behavioural half, asserted against everything the launch actually
// wrote.
func TestRecordConfigCannotSeeAPromptedCredentialInput(t *testing.T) {
	const prompted = "typed-at-launch-and-never-stored"

	jobs := newTestJobStore(t)
	configs := &recordingConfigs{}
	tmpl := launchableTemplate()
	tmpl.CredentialIDs = []int{18}

	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), jobs, newCapturingBus(),
		api.WithTemplates(stubTemplates{tmpl: tmpl}),
		api.WithLaunchConfigs(configs))

	jobID, _, err := dispatcher.LaunchTemplate(context.Background(), "ada@example.com", 12,
		launchConfig(),
		credtype.PromptedInputs{18: {"api_token": prompted}})
	if err != nil {
		t.Fatalf("LaunchTemplate returned unexpected error: %v", err)
	}

	// Nothing the configuration store was handed.
	for i, cfg := range configs.saved {
		encoded, err := json.Marshal(cfg)
		if err != nil {
			t.Fatalf("failed to encode saved configuration %d: %v", i, err)
		}
		if strings.Contains(string(encoded), prompted) {
			t.Errorf("saved configuration %d carries the prompted value: %s", i, encoded)
		}
	}

	// Nothing the job row was handed.
	job, _, err := jobs.Get(context.Background(), jobID)
	if err != nil {
		t.Fatalf("Get returned unexpected error: %v", err)
	}
	encoded, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("failed to encode the job record: %v", err)
	}
	if strings.Contains(string(encoded), prompted) {
		t.Errorf("the job record carries the prompted value: %s", encoded)
	}
}

// TestAPromptedInputTravelsOnTheEvent is the other half of the same design,
// and without it the test above would pass for a build that simply lost the
// value.
//
// The event is the one place it CAN travel: it must not be persisted, and
// the fan-out worker runs on every controller replica, so the replica that
// served this launch and the one that fans it out are routinely different
// processes.
func TestAPromptedInputTravelsOnTheEvent(t *testing.T) {
	const prompted = "typed-at-launch"

	jobs := newTestJobStore(t)
	bus := newCapturingBus()
	tmpl := launchableTemplate()
	tmpl.CredentialIDs = []int{18}

	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), jobs, bus,
		api.WithTemplates(stubTemplates{tmpl: tmpl}))

	if _, _, err := dispatcher.LaunchTemplate(context.Background(), "ada@example.com", 12,
		launchConfig(),
		credtype.PromptedInputs{18: {"api_token": prompted}}); err != nil {
		t.Fatalf("LaunchTemplate returned unexpected error: %v", err)
	}

	requested, ok := bus.firstOnTopic(topology.JobRequestedSubject())
	if !ok {
		t.Fatal("no job.requested event was published")
	}
	var payload struct {
		JobID    string                  `json:"job_id"`
		Prompted credtype.PromptedInputs `json:"prompted"`
	}
	if err := json.Unmarshal(requested.Data, &payload); err != nil {
		t.Fatalf("failed to decode the job.requested payload: %v", err)
	}
	if payload.Prompted[18]["api_token"] != prompted {
		t.Errorf("the event carries %v, want the prompted value", payload.Prompted)
	}
}

// TestALaunchWithNoPromptedInputsCarriesNoPromptedKey pins the wire form of
// the ordinary case, which is every launch in a deployment that has created
// no prompting credential type.
func TestALaunchWithNoPromptedInputsCarriesNoPromptedKey(t *testing.T) {
	jobs := newTestJobStore(t)
	bus := newCapturingBus()

	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), jobs, bus,
		api.WithTemplates(stubTemplates{tmpl: launchableTemplate()}))

	if _, _, err := dispatcher.LaunchTemplate(context.Background(), "ada@example.com", 12, launchConfig(), nil); err != nil {
		t.Fatalf("LaunchTemplate returned unexpected error: %v", err)
	}
	requested, ok := bus.firstOnTopic(topology.JobRequestedSubject())
	if !ok {
		t.Fatal("no job.requested event was published")
	}
	if strings.Contains(string(requested.Data), "prompted") {
		t.Errorf("an ordinary launch carries a prompted key: %s", requested.Data)
	}
}

// TestRelaunchRefusesACredentialThatPromptsAtLaunch is the exact mirror of
// the survey-secret refusal configFor already applies.
//
// The reasoning is identical: the value was typed once by one operator and
// was never stored, so there is nothing to repeat. Silently relaunching
// without it would produce a run that fails to authenticate, attributed to
// whoever pressed the button.
func TestRelaunchRefusesACredentialThatPromptsAtLaunch(t *testing.T) {
	tests := []struct {
		name        string
		credentials api.CredentialReader
		wantRefused bool
	}{
		{
			name: "a credential whose type prompts is refused, naming both it and the input",
			credentials: stubCredentialReader{
				bound: []credstore.Credential{{ID: 18, Name: "prod api", TypeID: 4}},
				types: map[int]credstore.CredentialType{4: promptingType()},
			},
			wantRefused: true,
		},
		{
			name: "a credential whose values are all stored is repeatable",
			credentials: stubCredentialReader{
				bound: []credstore.Credential{{ID: 19, Name: "stored api", TypeID: 5}},
				types: map[int]credstore.CredentialType{5: storingType()},
			},
		},
		{
			name: "a controller with no credential reader wired permits the relaunch",
			// The right default rather than a gap: a deployment with no
			// credential bindings has nothing to refuse, and refusing every
			// relaunch to guard a case that cannot arise would break the
			// feature for everybody.
			credentials: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jobs := newTestJobStore(t)
			tmpl := launchableTemplate()
			tmpl.CredentialIDs = []int{18}

			opts := []api.DispatcherOption{api.WithTemplates(stubTemplates{tmpl: tmpl})}
			if tt.credentials != nil {
				opts = append(opts, api.WithCredentialReader(tt.credentials))
			}
			dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), jobs, newCapturingBus(), opts...)

			jobID, _, err := dispatcher.LaunchTemplate(context.Background(), "ada@example.com", 12, launchConfig(), nil)
			if err != nil {
				t.Fatalf("LaunchTemplate returned unexpected error: %v", err)
			}

			_, _, err = dispatcher.Relaunch(context.Background(), "grace@example.com", jobID)
			if !tt.wantRefused {
				if err != nil {
					t.Fatalf("Relaunch refused a repeatable job: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Relaunch repeated a job whose credential was never stored")
			}
			if !errors.Is(err, api.ErrNotRelaunchable) {
				t.Fatalf("error = %v, want one matching ErrNotRelaunchable", err)
			}
			for _, want := range []string{"prod api", "api_token"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
		})
	}
}

// TestRelaunchReadsTheTemplatesCurrentBindings covers the deliberate choice
// to check the template rather than the job's recorded ids.
//
// The two can differ, and the template is what a relaunch will actually run
// with: LaunchTemplate resolves it afresh, which is also what lets a
// relaunch pick up a credential rotated since the original run. Checking
// the job's recorded ids would refuse or permit based on the wrong set.
func TestRelaunchReadsTheTemplatesCurrentBindings(t *testing.T) {
	jobs := newTestJobStore(t)

	// The job was launched from a template binding nothing.
	plain := launchableTemplate()
	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), jobs, newCapturingBus(),
		api.WithTemplates(stubTemplates{tmpl: plain}))
	jobID, _, err := dispatcher.LaunchTemplate(context.Background(), "ada@example.com", 12, launchConfig(), nil)
	if err != nil {
		t.Fatalf("LaunchTemplate returned unexpected error: %v", err)
	}

	// Since then, somebody bound a prompting credential to that template.
	bound := launchableTemplate()
	bound.CredentialIDs = []int{18}
	rebound := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), jobs, newCapturingBus(),
		api.WithTemplates(stubTemplates{tmpl: bound}),
		api.WithCredentialReader(stubCredentialReader{
			bound: []credstore.Credential{{ID: 18, Name: "prod api", TypeID: 4}},
			types: map[int]credstore.CredentialType{4: promptingType()},
		}))

	if _, _, err := rebound.Relaunch(context.Background(), "grace@example.com", jobID); !errors.Is(err, api.ErrNotRelaunchable) {
		t.Fatalf("error = %v, want the relaunch refused on the template's CURRENT bindings", err)
	}
}

// TestRelaunchReportsACredentialStoreFailureRatherThanGuessing covers the
// error path, which must not degrade into permitting the relaunch: a store
// that cannot answer is not the same fact as a credential that does not
// prompt.
func TestRelaunchReportsACredentialStoreFailureRatherThanGuessing(t *testing.T) {
	jobs := newTestJobStore(t)
	tmpl := launchableTemplate()
	tmpl.CredentialIDs = []int{18}

	dispatcher := api.NewDispatcher(newTestRunbookSource(t, "pb-1"), jobs, newCapturingBus(),
		api.WithTemplates(stubTemplates{tmpl: tmpl}),
		api.WithCredentialReader(stubCredentialReader{err: errors.New("the credential store is unreachable")}))

	jobID, _, err := dispatcher.LaunchTemplate(context.Background(), "ada@example.com", 12, launchConfig(), nil)
	if err != nil {
		t.Fatalf("LaunchTemplate returned unexpected error: %v", err)
	}
	if _, _, err := dispatcher.Relaunch(context.Background(), "grace@example.com", jobID); err == nil {
		t.Fatal("Relaunch proceeded despite being unable to read the template's credentials")
	}
}

// launchConfig is an empty launch configuration, named so the several call
// sites above read as "launched with nothing supplied" rather than as a
// bare struct literal repeated eight times.
func launchConfig() launch.Config { return launch.Config{} }
