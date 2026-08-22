package dispatch_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/internal/dispatch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/internal/render"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
	"github.com/google/uuid"
)

// The fan-out injection tests. What each one is really about:
//
//   - the artifact reaches the wire at all, with real values
//   - a template's MACHINE credential beats the per-device store, which is
//     AWX's semantics and the one precedence decision in this phase
//   - the per-device store still wins when the template binds no machine
//     credential, which is what keeps every pre-Phase-22 dispatch working
//   - a credential failure fails the JOB rather than ten thousand devices
//   - a prompted input reaches the injector and is gone afterwards

// fakeResolver is a CredentialResolver double.
//
// A double rather than the real ent-backed resolver because what is under
// test here is the fan-out's own behaviour: which credential wins, where a
// failure lands, and what reaches the wire. internal/credstore/resolve has
// its own tests against a real database for the resolution itself.
type fakeResolver struct {
	creds []credtype.Credential
	err   error
	// gotIDs records what the fan-out asked for, so a test can assert the
	// job's own binding order was preserved rather than re-sorted.
	gotIDs []int
}

func (r *fakeResolver) Resolve(_ context.Context, ids []int) ([]credtype.Credential, error) {
	r.gotIDs = append([]int(nil), ids...)
	if r.err != nil {
		return nil, r.err
	}
	return r.creds, nil
}

// testInjector builds an injector over an isolated masking set.
func testInjector(t *testing.T) *credtype.Injector {
	t.Helper()

	masker, err := redact.NewMasker(redact.DefaultRuleset())
	if err != nil {
		t.Fatalf("NewMasker() error = %v", err)
	}
	in, err := credtype.NewInjector(render.New(), credtype.WithLiterals(masker.Literals()))
	if err != nil {
		t.Fatalf("NewInjector() error = %v", err)
	}
	return in
}

// cloudCredential is a credential whose type injects an environment
// variable and an extra variable, which is the ordinary custom type shape.
func cloudCredential(id int, name string) credtype.Credential {
	return credtype.Credential{
		ID:   id,
		Name: name,
		Type: credtype.CredentialType{
			Name: "Custom REST API Token", Kind: credtype.KindCloud, Namespace: "custom_api_token",
			Inputs: credtype.InputSchema{
				Fields: []credtype.InputField{
					{ID: "api_token", Label: "Token", Secret: true},
					{ID: "api_url", Label: "URL"},
				},
				Required: []string{"api_token", "api_url"},
			},
			Injectors: credtype.Injectors{
				Env:       map[string]string{"REST_API_TOKEN": "{{ api_token }}"},
				ExtraVars: map[string]any{"ansible_api_url": "{{ api_url }}"},
			},
		},
		Inputs: map[string]string{"api_token": "a-real-bearer-token", "api_url": "https://api.example.test"},
	}
}

// machineCredential is a credential of the one kind that resolves to the
// identity a run authenticates as.
func machineCredential(id int, name, username, password string) credtype.Credential {
	return credtype.Credential{
		ID:   id,
		Name: name,
		Type: credtype.CredentialType{
			Name: "Machine", Kind: credtype.KindSSH, Namespace: "ssh", Managed: true,
			Inputs: credtype.InputSchema{Fields: []credtype.InputField{
				{ID: "username", Label: "Username"},
				{ID: "password", Label: "Password", Secret: true},
			}},
		},
		Inputs: map[string]string{"username": username, "password": password},
	}
}

// requestBoundJob creates a job bound to credentialIDs and returns the
// job.requested event a launcher would publish for it.
//
// prompted is written into the event rather than onto the job, which is the
// asymmetry this phase's design rests on and the reason this helper builds
// the payload by hand: the values must never be persisted, so there is no
// job field to put them in.
func requestBoundJob(t *testing.T, ctx context.Context, store dispatch.JobStore, credentialIDs []int, prompted credtype.PromptedInputs) event.Event {
	t.Helper()

	job := &dispatch.Job{
		RunbookID: "pb-1", GroupName: "routers", Actor: "user@example.com",
		CredentialIDs: credentialIDs,
	}
	if err := store.Create(ctx, job); err != nil {
		t.Fatalf("Create returned unexpected error: %v", err)
	}

	data, err := json.Marshal(struct {
		JobID    string                  `json:"job_id"`
		Prompted credtype.PromptedInputs `json:"prompted,omitempty"`
	}{JobID: job.JobID, Prompted: prompted})
	if err != nil {
		t.Fatalf("failed to marshal job.requested payload: %v", err)
	}
	return event.Event{ID: uuid.New().String(), Type: "job.requested", Data: data}
}

// dispatchedPayload decodes the one dispatch a fan-out published.
func dispatchedPayload(t *testing.T, bus *capturingBus) wire.DispatchPayload {
	t.Helper()

	var payload wire.DispatchPayload
	if err := json.Unmarshal(bus.last().Data, &payload); err != nil {
		t.Fatalf("failed to decode published DispatchPayload: %v", err)
	}
	return payload
}

// TestWorkerInjectsBoundCredentialsOntoTheWire is the base case: a template
// binds a credential, and what it renders reaches the Runner.
func TestWorkerInjectsBoundCredentialsOntoTheWire(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	bus := newCapturingBus()
	device := sshCapableDevice("dev-id-ssh", "core-switch-1", "10.0.0.9", 22)
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{device}}

	resolver := &fakeResolver{creds: []credtype.Credential{cloudCredential(18, "prod api")}}
	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil,
		dispatch.WithCredentials(resolver, testInjector(t)))

	evt := requestBoundJob(t, ctx, store, []int{18}, nil)
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested returned unexpected error: %v", err)
	}

	payload := dispatchedPayload(t, bus)
	if payload.Injected == nil {
		t.Fatal("the dispatch carries no injected credential material")
	}
	if payload.Injected.Env["REST_API_TOKEN"] != "a-real-bearer-token" {
		t.Errorf("Injected.Env = %v, want the rendered token", payload.Injected.Env)
	}
	if payload.Injected.ExtraVars["ansible_api_url"] != "https://api.example.test" {
		t.Errorf("Injected.ExtraVars = %v, want the rendered url", payload.Injected.ExtraVars)
	}
	// A cloud credential is not an identity, so it must not have become one.
	if len(payload.Secrets) != 0 {
		t.Errorf("Secrets = %v, want none: a cloud credential is not a machine identity", payload.Secrets)
	}
	if len(resolver.gotIDs) != 1 || resolver.gotIDs[0] != 18 {
		t.Errorf("the fan-out resolved %v, want exactly the job's own binding", resolver.gotIDs)
	}
}

// TestMachineCredentialPrecedence is the one precedence decision this phase
// makes, and both directions of it matter.
//
// A machine credential bound to the TEMPLATE authenticates every device in
// the fan-out, which is AWX's semantics and what an operator migrating from
// it expects. The per-device store remains the fallback, which is what
// keeps every dispatch that worked before this phase working unchanged.
func TestMachineCredentialPrecedence(t *testing.T) {
	// A per-device credential in the Crawl-tier file store, through the real
	// adapter rather than a fake, so the fallback path under test is the
	// one production actually runs.
	newDeviceStore := func(t *testing.T) credential.Store {
		t.Helper()
		dir := t.TempDir()
		key, err := credential.ResolveMasterKey(dir)
		if err != nil {
			t.Fatalf("failed to resolve master key: %v", err)
		}
		if err := credential.SaveFileStore(dir, key, "core-switch-1",
			credential.Credential{Username: "per-device", Password: "per-device-password"}); err != nil {
			t.Fatalf("failed to save fixture credential: %v", err)
		}
		return credential.NewLazyFileStore(dir)
	}

	tests := []struct {
		name         string
		bound        []credtype.Credential
		wantUsername string
	}{
		{
			name:         "a bound machine credential supplies every device in the fan-out",
			bound:        []credtype.Credential{machineCredential(3, "template machine", "from-template", "template-password")},
			wantUsername: "from-template",
		},
		{
			name:         "with no machine credential bound, the per-device store still wins",
			bound:        []credtype.Credential{cloudCredential(18, "prod api")},
			wantUsername: "per-device",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			store := newTestJobStore(t)
			bus := newCapturingBus()
			device := sshCapableDevice("dev-id-ssh", "core-switch-1", "10.0.0.9", 22)
			repo := &fakeRepository{Devices: []pkginventory.InventoryItem{device}}

			resolver := &fakeResolver{creds: tt.bound}
			worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, newDeviceStore(t),
				dispatch.WithCredentials(resolver, testInjector(t)))

			ids := make([]int, 0, len(tt.bound))
			for _, c := range tt.bound {
				ids = append(ids, c.ID)
			}
			evt := requestBoundJob(t, ctx, store, ids, nil)
			if err := worker.HandleJobRequested(evt); err != nil {
				t.Fatalf("HandleJobRequested returned unexpected error: %v", err)
			}

			payload := dispatchedPayload(t, bus)
			if got := payload.Secrets[credential.SecretUsername]; got != tt.wantUsername {
				t.Errorf("Secrets[username] = %q, want %q", got, tt.wantUsername)
			}
		})
	}
}

// TestUnbindableCredentialFailsTheJobRatherThanEveryDevice covers where a
// credential failure lands, which is a deliberate departure from the
// per-device treatment everything else in the fan-out gets.
//
// A credential that cannot be resolved is not a property of any one device.
// Writing ten thousand identical JobTask rows saying so would bury the one
// sentence an operator needs, so it is treated the way an unresolvable
// definition already is: the job fails, with a reason.
func TestUnbindableCredentialFailsTheJobRatherThanEveryDevice(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	bus := newCapturingBus()
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{
		sshCapableDevice("dev-1", "core-switch-1", "10.0.0.9", 22),
		sshCapableDevice("dev-2", "core-switch-2", "10.0.0.10", 22),
	}}

	resolver := &fakeResolver{err: errors.New("credential 18 is bound but no longer exists")}
	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil,
		dispatch.WithCredentials(resolver, testInjector(t)))

	evt := requestBoundJob(t, ctx, store, []int{18}, nil)
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested returned unexpected error: %v", err)
	}

	job, tasks, err := store.Get(ctx, jobIDFromEvent(t, evt))
	if err != nil {
		t.Fatalf("Get returned unexpected error: %v", err)
	}
	if job.State != "failed" {
		t.Errorf("job state = %q, want failed", job.State)
	}
	if len(tasks) != 0 {
		t.Errorf("tasks = %+v, want none: no device was considered", tasks)
	}
	if job.FailureReason == "" {
		t.Error("the job records no reason for the failure")
	}
	if bus.count() != 0 {
		t.Errorf("%d events were published for a job that could not resolve its credentials", bus.count())
	}
}

// TestInjectionFailureNeverQuotesAValue covers the rule every message on
// this path follows. The reason lands on a job record an API caller reads.
func TestInjectionFailureNeverQuotesAValue(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	bus := newCapturingBus()
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{
		sshCapableDevice("dev-1", "core-switch-1", "10.0.0.9", 22),
	}}

	// Two credentials injecting the same environment variable, which the
	// injector refuses rather than silently picking a winner.
	first := cloudCredential(18, "first")
	second := cloudCredential(19, "second")
	resolver := &fakeResolver{creds: []credtype.Credential{first, second}}

	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil,
		dispatch.WithCredentials(resolver, testInjector(t)))

	evt := requestBoundJob(t, ctx, store, []int{18, 19}, nil)
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested returned unexpected error: %v", err)
	}

	job, _, err := store.Get(ctx, jobIDFromEvent(t, evt))
	if err != nil {
		t.Fatalf("Get returned unexpected error: %v", err)
	}
	if job.State != "failed" {
		t.Fatalf("job state = %q, want failed", job.State)
	}
	if strings.Contains(job.FailureReason, "a-real-bearer-token") {
		t.Errorf("the job's failure reason quotes a secret: %q", job.FailureReason)
	}
	// It still has to be useful: an operator reading it must learn which
	// two credentials clashed and over what.
	for _, want := range []string{"first", "second", "REST_API_TOKEN"} {
		if !strings.Contains(job.FailureReason, want) {
			t.Errorf("the failure reason does not name %q: %q", want, job.FailureReason)
		}
	}
}

// TestPromptedInputsReachTheInjectorAndAreForgotten covers the launch-time
// values, which travel on the event rather than on the job record because
// they must never be persisted.
func TestPromptedInputsReachTheInjectorAndAreForgotten(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	bus := newCapturingBus()
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{
		sshCapableDevice("dev-1", "core-switch-1", "10.0.0.9", 22),
	}}

	// A credential whose token is prompted rather than stored.
	cred := cloudCredential(18, "prod api")
	cred.Type.Inputs.Fields[0].AskAtRuntime = true
	cred.Inputs = map[string]string{"api_url": "https://api.example.test"}
	resolver := &fakeResolver{creds: []credtype.Credential{cred}}

	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil,
		dispatch.WithCredentials(resolver, testInjector(t)))

	evt := requestBoundJob(t, ctx, store, []int{18},
		credtype.PromptedInputs{18: {"api_token": "typed-at-launch"}})
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested returned unexpected error: %v", err)
	}

	payload := dispatchedPayload(t, bus)
	if payload.Injected == nil || payload.Injected.Env["REST_API_TOKEN"] != "typed-at-launch" {
		t.Fatalf("the prompted value did not reach the injector: %+v", payload.Injected)
	}

	// Nothing about it was written to the job record. This is the
	// never-persist rule, checked against the stored row rather than
	// against the type that carries it.
	job, _, err := store.Get(ctx, jobIDFromEvent(t, evt))
	if err != nil {
		t.Fatalf("Get returned unexpected error: %v", err)
	}
	encoded, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("failed to encode the job record: %v", err)
	}
	if strings.Contains(string(encoded), "typed-at-launch") {
		t.Errorf("the job record carries the prompted value: %s", encoded)
	}
}

// TestNoCredentialsBoundLeavesTheWireUntouched is the regression guard for
// every dispatch that existed before this phase.
//
// The payload of a job binding nothing must carry no "injected" key at all,
// not an empty object, because an older Runner decoding a payload it has
// never seen a field for is exactly the deployment-ordering hazard
// wire.Injected's own doc comment warns about.
func TestNoCredentialsBoundLeavesTheWireUntouched(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	bus := newCapturingBus()
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{
		sshCapableDevice("dev-1", "core-switch-1", "10.0.0.9", 22),
	}}

	resolver := &fakeResolver{}
	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil,
		dispatch.WithCredentials(resolver, testInjector(t)))

	evt := requestJob(t, ctx, store, "pb-1", "routers")
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested returned unexpected error: %v", err)
	}

	if len(resolver.gotIDs) != 0 {
		t.Errorf("the fan-out resolved %v for a job binding nothing", resolver.gotIDs)
	}
	if strings.Contains(string(bus.last().Data), `"injected"`) {
		t.Errorf("a credential-less dispatch carries an injected key: %s", bus.last().Data)
	}
}

// TestAMachineOnlyBindingCarriesNoInjectedBlock covers the other end of the
// same rule: a machine credential produces an identity and nothing else, so
// there is nothing for the injected block to hold and it must not appear
// empty.
func TestAMachineOnlyBindingCarriesNoInjectedBlock(t *testing.T) {
	ctx := t.Context()
	store := newTestJobStore(t)
	bus := newCapturingBus()
	repo := &fakeRepository{Devices: []pkginventory.InventoryItem{
		sshCapableDevice("dev-1", "core-switch-1", "10.0.0.9", 22),
	}}

	resolver := &fakeResolver{creds: []credtype.Credential{
		machineCredential(3, "template machine", "operator", "a-real-password"),
	}}
	worker := dispatch.NewWorker(store, repo, newTestRunbookSource(t), bus, nil,
		dispatch.WithCredentials(resolver, testInjector(t)))

	evt := requestBoundJob(t, ctx, store, []int{3}, nil)
	if err := worker.HandleJobRequested(evt); err != nil {
		t.Fatalf("HandleJobRequested returned unexpected error: %v", err)
	}

	payload := dispatchedPayload(t, bus)
	if payload.Injected != nil {
		t.Errorf("Injected = %+v, want nothing: a machine credential has its home in Secrets", payload.Injected)
	}
	if payload.Secrets[credential.SecretUsername] != "operator" {
		t.Errorf("Secrets = %v, want the bound machine identity", payload.Secrets)
	}
}
