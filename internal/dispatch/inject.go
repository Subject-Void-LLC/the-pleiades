// Package dispatch: credential injection at fan-out, and the conversion
// between internal/credtype's Artifact and the wire form a Runner decodes.
//
// # Why injection happens here rather than at launch
//
// It would be easy to render a template's credentials once, at
// LaunchTemplate, and stamp the result onto the job. Four things make
// fan-out the right moment instead, and each of them is a real property
// rather than a preference.
//
// It keeps rendered secrets out of the jobs table entirely. A job records
// CredentialIDs and nothing else, so a database backup, a support dump, or
// a badly-scoped read of the job history contains no secret at all. A job
// carrying its rendered environment would put a live credential in a row
// with a seven-day-plus retention and no encryption, for the convenience of
// having rendered it slightly earlier.
//
// It is the same place a credential is already resolved. worker_devices.go
// already calls credentials.Lookup for the per-device Crawl-tier store here.
// Two just-in-time resolution points would eventually disagree about which
// one a given dispatch used.
//
// It makes external lookups genuinely just in time. A job queued behind a
// capacity limit holds a pointer to a secret rather than the secret, which
// is PLAN.md Section 17.4's whole point, and it only holds if resolution
// happens when the job actually dispatches.
//
// It makes a relaunch pick up a rotated secret. An operator who rotated a
// credential and relaunched a week-old job expects the new value. Rendering
// at launch and replaying the render would give them the old one, silently,
// and the run would fail against a credential they had already fixed.
package dispatch

import (
	"context"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credtype"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// CredentialResolver returns credentials with their real input values.
//
// It is declared here rather than taken as internal/credstore/resolve's own
// interface for the reason every other port in this package is declared
// here: a consumer owns the interface it depends on, so this package is
// testable with a double that has no database behind it. The real
// implementation is resolve.NewEntResolver, wired by the composition root.
//
// This is one of exactly two packages permitted to hold one.
// internal/archtest fails the build if a third appears, and
// internal/api holding one would defeat the no-plaintext-read boundary
// credstore exists to draw.
type CredentialResolver interface {
	// Resolve returns the named credentials with real values, in the order
	// the ids were given.
	Resolve(ctx context.Context, ids []int) ([]credtype.Credential, error)
}

// injectFor resolves and renders this job's bound credentials.
//
// It returns the artifact and a sanitised reason, following the same
// two-return split targetSelector uses: the error is for an operator
// reading logs, the reason is what a JobTask row carries and must name only
// the credential and the input, never a value.
//
// A job binding no credentials, or a Worker with no resolver wired, returns
// an empty artifact and no error. That is what keeps every pre-Phase-22
// dispatch working unchanged: the per-device Crawl-tier credential store
// below this call is still the fallback, and a deployment that has never
// created a credential type never enters this path.
func (w *Worker) injectFor(ctx context.Context, job *Job) (credtype.Artifact, string, error) {
	if w.credentialResolver == nil || w.injector == nil || len(job.CredentialIDs) == 0 {
		return credtype.Artifact{}, "", nil
	}

	creds, err := w.credentialResolver.Resolve(ctx, job.CredentialIDs)
	if err != nil {
		return credtype.Artifact{}, "a credential this job is bound to could not be resolved", err
	}

	art, err := w.injector.Inject(creds, w.promptedFor(job.JobID))
	if err != nil {
		// The injector's own messages are written to this rule already:
		// they name the credential and the template target and never quote
		// a value (see internal/credtype's injector.go). So the reason is
		// its text rather than a second, vaguer sentence, which would hide
		// the one detail an operator needs to fix it.
		return credtype.Artifact{}, err.Error(), err
	}
	return art, "", nil
}

// InjectedFrom converts an Artifact into the wire form a Runner decodes.
//
// It is the ONE conversion between the two, which is what
// pkg/wire.Injected's own doc comment leans on: a field added to one and
// not the other is a compile failure here rather than a value silently
// dropped on the wire.
//
// Exported for one reason, and it is the reason that keeps the claim above
// true: cmd/runner's injection Release Gate builds a real dispatch payload
// from a real rendered artifact, and it has to do that through this
// function rather than through a second conversion of its own. A gate that
// hand-built its own wire form would be proving the adapter against a shape
// nothing in production produces.
//
// The machine credential is deliberately absent from the result. It has a
// home already in wire.DispatchPayload.Secrets, which every adapter and
// every Collection method already reads, and a second home would mean two
// answers to what a run authenticates as. The caller assigns it there.
func InjectedFrom(art credtype.Artifact) *wire.Injected {
	if art.Empty() {
		return nil
	}

	out := &wire.Injected{
		Env:       art.Env,
		ExtraVars: art.ExtraVars,
		// Which of the values below are secret, said explicitly, because
		// the adapter on the far side of this wire cannot work it out. See
		// wire.Injected.Mask for what goes wrong if it tries.
		Mask: art.SecretValues(),
	}
	for _, f := range art.Files {
		out.Files = append(out.Files, wire.InjectedFile{
			Label:   f.Label,
			Path:    f.Path,
			Content: f.Content,
			Mode:    f.Mode,
		})
	}
	for _, v := range art.Vault() {
		out.Vault = append(out.Vault, wire.InjectedVault{
			Identifier: v.Identifier,
			Path:       v.Path,
		})
	}

	// A machine-credential-only artifact produces nothing on this side of
	// the wire, and an empty Injected on a payload would be noise every
	// reader has to check for. Mask is not consulted here: it describes
	// the other fields, so a block holding only a mask describes nothing.
	if len(out.Env) == 0 && len(out.ExtraVars) == 0 && len(out.Files) == 0 && len(out.Vault) == 0 {
		return nil
	}
	return out
}

// applyInjection attaches an artifact to a payload, resolving the
// credential precedence question in the one place it arises.
//
// # The precedence rule
//
// A machine credential bound to the TEMPLATE supplies authentication for
// every device in the fan-out. The per-device store
// (credentials.Lookup(device.Name())) is consulted only when the template
// binds no machine credential.
//
// That is AWX's semantics and it is the behaviour an operator migrating
// from AWX expects: they bind one machine credential to a job template and
// every host in the inventory is reached with it. The per-device store
// staying as the fallback is what keeps every dispatch that exists today
// working unchanged, including the whole Crawl tier, which has no template
// and no binding.
//
// The order matters and the inverse would be worse: a per-device credential
// winning over an explicit template binding would mean an operator binding
// a credential, watching the run authenticate as something else, and having
// nothing in the record to tell them why.
func applyInjection(payload *wire.DispatchPayload, art credtype.Artifact) {
	payload.Injected = InjectedFrom(art)
	if machine, ok := art.Machine(); ok {
		payload.Secrets = machine
	}
}

// promptedFor returns the prompted inputs carried for one job.
//
// The values travel on the job.requested event rather than in a map on this
// Worker, and the asymmetry with stored credentials is worth stating
// plainly: PROMPTED inputs travel on the event, STORED inputs resolve at
// fan-out. An in-process map cannot work here, because the Worker runs on
// every controller replica (cmd/controller subscribes the handler directly
// rather than behind the leader election), so the replica that handled the
// launch and the replica that fans it out are routinely different
// processes.
//
// That means a prompted secret inherits the same JetStream exposure the
// dispatch payload already has, which is recorded as a residual rather than
// papered over. Inventing a second, separate channel for it here would be
// theatre: it would traverse the same broker with the same retention.
func (w *Worker) promptedFor(jobID string) credtype.PromptedInputs {
	w.promptedMu.Lock()
	defer w.promptedMu.Unlock()

	prompted, ok := w.prompted[jobID]
	if !ok {
		return nil
	}
	// Consumed on read. A prompted value is typed once and used once, so
	// holding it past the fan-out that needed it is holding a secret for no
	// reason.
	delete(w.prompted, jobID)
	return prompted
}

// rememberPrompted records the prompted inputs a job.requested event
// carried, for the fan-out this same handler invocation is about to run.
//
// The lifetime is one HandleJobRequested call: the event is decoded, the
// values are recorded here, the fan-out consumes them, and promptedFor
// deletes them. A handler that fails between the two leaves an entry
// behind, which forgetStale (called on every decode) clears.
func (w *Worker) rememberPrompted(jobID string, prompted credtype.PromptedInputs) {
	if len(prompted) == 0 {
		return
	}
	w.promptedMu.Lock()
	defer w.promptedMu.Unlock()

	if w.prompted == nil {
		w.prompted = make(map[string]credtype.PromptedInputs)
	}
	w.prompted[jobID] = prompted
}

// forgetPrompted drops a job's prompted inputs whether or not the fan-out
// reached them.
//
// Called on every exit from HandleJobRequested, so a fan-out that failed
// before injection, or was fenced out by a reclaim, does not leave a
// plaintext secret in this map until the process restarts.
func (w *Worker) forgetPrompted(jobID string) {
	w.promptedMu.Lock()
	defer w.promptedMu.Unlock()
	delete(w.prompted, jobID)
}
