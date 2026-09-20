package legacy

import (
	"context"
	"fmt"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"log/slog"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/event"
	"github.com/Subject-Void-LLC/the-pleiades/internal/launch"
	"github.com/Subject-Void-LLC/the-pleiades/internal/redact"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// inventoryContainerPath and playbookContainerPath are the fixed
// in-container paths Execute copies the generated inventory and resolved
// playbook to (via ContainerSpec.Files, never a bind-mounted host path:
// see orchestrator.go's own trust-boundary doc comment), and the same
// paths Argv below references. Named constants, not independently
// hand-typed string literals at each use site, so the two can never drift
// apart.
const (
	inventoryContainerPath = "/run/pleiades/inventory.json"
	playbookContainerPath  = "/run/pleiades/playbook.yml"
)

// Adapter implements runner.ExecutionAdapter for unconverted Ansible
// playbooks. It is the second real Strangler Fig participant alongside
// internal/adapters/native.Adapter: runner.Agent does not know or care
// which one it holds (PATTERNS.md's own Strangler Fig entry), though
// choosing between the two at runtime remains explicitly out of scope for
// this phase (cmd/runner/main.go's own comment) -- there is no Phase 21
// Launchable Kind registry yet to route dispatch on.
type Adapter struct {
	bus          event.Bus
	playbooks    PlaybookSource
	orchestrator ContainerOrchestrator
	image        string
	logger       *slog.Logger
	networks     []string
}

// AdapterOption configures optional Adapter behavior, mirroring
// internal/runner.AgentOption's own established shape in this codebase.
type AdapterOption func(*Adapter)

// WithNetworks attaches every container this Adapter runs to the named
// pre-existing Docker networks (ContainerSpec.Networks), in addition to
// Docker's own default bridge network. Empty by default and left unset in
// every real dispatch: a real target device is a real remote host reached
// over ordinary networking. This exists for this package's own Release
// Gate, which runs both ends of a real SSH connection as two ephemeral
// containers on the same Docker host and needs them on a shared network
// to reach each other portably.
func WithNetworks(networks []string) AdapterOption {
	return func(a *Adapter) { a.networks = networks }
}

// NewAdapter builds a legacy Adapter. playbooks resolves a dispatched
// RunbookID to a real playbook's raw bytes; orchestrator provisions the
// real ephemeral container each run happens inside (DockerOrchestrator in
// production and in this package's own Release Gate; a Kubernetes/ECS
// implementation is Phase 26's job, not built here). image is a pinned
// reference to the Ansible runner image to run (never "latest": see
// internal/testsupport/images.go's own stated rationale for why a pin
// matters), supplied by the caller rather than defaulted inside this
// package, the same way native.NewAdapter takes its own runbook.Source
// and *slog.Logger as required constructor arguments rather than
// reaching for a package-level default. A nil logger falls back to
// slog.Default(), matching internal/adapters/native.NewAdapter's own
// established convention.
func NewAdapter(bus event.Bus, playbooks PlaybookSource, orchestrator ContainerOrchestrator, image string, logger *slog.Logger, opts ...AdapterOption) *Adapter {
	if logger == nil {
		logger = slog.Default()
	}
	a := &Adapter{bus: bus, playbooks: playbooks, orchestrator: orchestrator, image: image, logger: logger}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Execute implements runner.ExecutionAdapter. It resolves payload's
// RunbookID to a real Ansible playbook, builds a single-host inventory
// document from payload (BuildInventoryJSON), runs a real ephemeral
// container executing that playbook against payload's own device, parses
// its real captured stdout (ParseStdout), and publishes the resulting
// wire.JobEvent sequence to the same job-log bus
// internal/adapters/native.Adapter already uses.
//
// This is a post-hoc batch parse, not a live stream: every event this
// method publishes is produced after the container has already exited.
// Phase 25 (The Ansible Callback Bridge) is what replaces this with true,
// incremental per-task streaming; see ParseStdout's own doc comment for
// the full reasoning.
//
// It runs exactly one container against exactly one device, matching
// runner.Agent's own one-payload-per-device dispatch model
// (internal/dispatch, Phase 14). A real Ansible play's own "one process,
// many hosts" shape is a real mismatch this method deliberately does not
// solve; PLAN.md Section 30.3 names it as Phase 24's (Dependency Manager
// & Capacity Admission) open problem.
func (a *Adapter) Execute(ctx context.Context, payload wire.DispatchPayload) (wire.Outcome, error) {
	// A playbook is never run as a check: ansible-playbook --check still
	// runs a task marked check_mode: false for real, and reports a module
	// with no check support as skipped, as if it passed. Launch resolution
	// already refuses a check of a playbook; this is the Runner-side
	// backstop, before anything starts.
	if payload.Mode != "" && payload.Mode != string(collection.ModeExecute) {
		return wire.Outcome{}, fmt.Errorf("refusing to run playbook %q in mode %q: an Ansible playbook cannot be run as a check, only for real", payload.RunbookID, payload.Mode)
	}

	started := wire.JobEvent{Status: "started", Host: payload.DeviceHost, Task: "playbook:" + payload.RunbookID}
	started.Timestamp = time.Now().UTC().Format(time.RFC3339)
	started.EventData.Message = fmt.Sprintf("started ansible playbook %q on %s", payload.RunbookID, payload.DeviceName)
	if err := a.publish(ctx, payload.JobID, started); err != nil {
		return wire.Outcome{}, fmt.Errorf("failed to publish started event: %w", err)
	}

	playbook, err := a.playbooks.Get(ctx, payload.RunbookID)
	if err != nil {
		return wire.Outcome{}, fmt.Errorf("failed to resolve playbook %q: %w", payload.RunbookID, err)
	}

	inventory, err := BuildInventoryJSON(payload)
	if err != nil {
		return wire.Outcome{}, fmt.Errorf("failed to build inventory for %s: %w", payload.DeviceName, err)
	}

	files := []ContainerFile{
		{Content: inventory, ContainerPath: inventoryContainerPath, Mode: 0o600},
		{Content: playbook, ContainerPath: playbookContainerPath, Mode: 0o600},
	}
	if key, ok := payload.Secrets[credential.SecretPrivateKeyPEM]; ok {
		files = append(files, ContainerFile{Content: []byte(key), ContainerPath: sshPrivateKeyContainerPath, Mode: 0o600})
	}

	// The bound credentials' generated files, then their environment, then
	// their extra variables. Every one of these can refuse, and a refusal
	// here fails the dispatch before a container starts rather than letting
	// a run proceed with material this adapter could not honour.
	injected := payload.Injected
	// Exactly the values this dispatch says are secret, registered before
	// anything is built: everything below can appear in an error, and an
	// error is logged. See registerInjectedSecrets for why it is this list
	// rather than every injected value.
	registerInjectedSecrets(injected)

	credentialFiles, err := injectedFiles(injected)
	if err != nil {
		return wire.Outcome{}, fmt.Errorf("failed to prepare injected credential files for %s: %w", payload.DeviceName, err)
	}
	files = append(files, credentialFiles...)

	secretEnv, err := injectedEnv(injected)
	if err != nil {
		return wire.Outcome{}, fmt.Errorf("failed to prepare the injected environment for %s: %w", payload.DeviceName, err)
	}

	extraVars, err := mergeExtraVars(payload.ExtraVars, injected)
	if err != nil {
		return wire.Outcome{}, fmt.Errorf("failed to merge injected extra variables for %s: %w", payload.DeviceName, err)
	}

	// Extra variables reach ansible-playbook as a FILE, always, never on
	// argv. See buildArgv's own doc comment for the leak that fixes and why
	// it is unconditional.
	if len(extraVars) > 0 {
		encoded, err := encodeExtraVars(extraVars)
		if err != nil {
			return wire.Outcome{}, fmt.Errorf("failed to encode extra vars for %s: %w", payload.DeviceName, err)
		}
		files = append(files, ContainerFile{Content: encoded, ContainerPath: extraVarsContainerPath, Mode: 0o600})
	}

	fields := launch.Fields(payload.Fields)
	argv := buildArgv(fields, len(extraVars) > 0, vaultsOf(injected), inventoryContainerPath, playbookContainerPath)

	spec := ContainerSpec{
		Image:     a.image,
		Argv:      argv,
		SecretEnv: secretEnv,
		Env: map[string]string{
			"ANSIBLE_FORCE_COLOR": "false",
			"ANSIBLE_NOCOLOR":     "1",
			// Deliberate Phase 17 MVP simplification, not an oversight:
			// disabling host key checking is the only way this stateless,
			// ephemeral container (which holds no known_hosts of its own
			// and has no source for one -- wire.DispatchPayload carries
			// no host key material) can connect at all. Contrast
			// internal/catalog/net/ssh/ping.go's own hostKeyCallback,
			// which enforces real host key verification against a
			// populated $HOME/.ssh/known_hosts on the native Go path.
			// Closing this gap for the Ansible path needs a real answer
			// to "where does a target's known host key come from inside
			// a stateless container," which this phase does not invent.
			"ANSIBLE_HOST_KEY_CHECKING": "false",
		},
		Files:    files,
		Networks: a.networks,
	}

	// A launch's "timeout" field is a whole-run abandon deadline for this
	// kind (runTimeout's own doc comment), so it wraps the one call that
	// actually blocks for the run's duration; runCtx, not ctx, is what
	// reaches the orchestrator.
	runCtx := ctx
	if timeout := runTimeout(fields); timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	a.logger.Debug("running ansible-playbook container",
		slog.String("job_id", payload.JobID), slog.String("device", payload.DeviceName), slog.String("image", a.image))
	result, err := a.orchestrator.Run(runCtx, spec)
	if err != nil {
		return wire.Outcome{}, fmt.Errorf("failed to run ansible-playbook container: %w", err)
	}

	// Every value worth masking out of the captured Ansible output: what
	// the Controller attached as this device's identity, plus exactly what
	// the injection said was secret. A module's own JSON result can echo
	// either one straight back.
	secrets := secretValues(payload.Secrets)
	if injected != nil {
		secrets = append(secrets, injected.Mask...)
	}
	status, message := "ok", ""
	sawCompletion := false
	for _, evt := range ParseStdout(result.Output, time.Now()) {
		evt.EventData.Message = redact.Text(secrets, evt.EventData.Message)
		if err := a.publish(ctx, payload.JobID, evt); err != nil {
			return wire.Outcome{}, fmt.Errorf("failed to publish parsed event: %w", err)
		}
		if evt.Task == "task.completed" {
			status, message = evt.Status, evt.EventData.Message
			sawCompletion = true
		}
	}

	// A container that exited non-zero without ever printing a parseable
	// PLAY RECAP (a hard crash, an unhandled Python exception before any
	// play ran) must not silently report success just because ParseStdout
	// found no per-host summary to derive one from.
	if !sawCompletion && result.ExitCode != 0 {
		status = "failed"
		message = fmt.Sprintf("ansible-playbook exited %d with no parseable summary", result.ExitCode)
		completed := wire.JobEvent{Status: status, Host: payload.DeviceHost, Task: "task.completed"}
		completed.Timestamp = time.Now().UTC().Format(time.RFC3339)
		completed.EventData.Message = redact.Text(secrets, message)
		if err := a.publish(ctx, payload.JobID, completed); err != nil {
			return wire.Outcome{}, fmt.Errorf("failed to publish completion event: %w", err)
		}
	}

	if status == "failed" {
		return wire.Outcome{}, fmt.Errorf("execution failed: %s", message)
	}
	return wire.Outcome{}, nil
}

// secretValues flattens payload.Secrets' own values into the []string
// redact.Text expects, mirroring
// internal/adapters/native.Adapter.Execute's own identical collection
// (adapter.go): every secret worth masking out of captured Ansible
// output is exactly what the Controller attached to this dispatch, since
// a module's own JSON result can echo any of them straight back.
func secretValues(secrets map[string]string) []string {
	values := make([]string, 0, len(secrets))
	for _, v := range secrets {
		values = append(values, v)
	}
	return values
}

// publish wraps evt and publishes it to topology.LogSubject(jobID),
// returning any error to the caller rather than logging and swallowing
// it, the identical contract internal/adapters/native.Adapter.publish
// already establishes: a job-log publish failure is a real signal, not
// an observability side effect this Adapter is entitled to hide.
func (a *Adapter) publish(ctx context.Context, jobID string, evt wire.JobEvent) error {
	return publishJobEvent(ctx, a.bus, jobID, evt)
}
