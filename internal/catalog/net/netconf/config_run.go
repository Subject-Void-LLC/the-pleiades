// Package netconf: net.netconf.config's run. The parameters are read and
// refused before a session opens; the session's discovery is recorded and the
// content held against it; the configuration the edit can touch is read
// before and after the edit, so changed says what the device's configuration
// did; and the undo is recorded where it is exact, before the edit wherever
// the read before decides it.
package netconf

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/datastore"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yangdoc"
)

// configFQCN is the method's name.
const configFQCN = "net.netconf.config"

// Config implements the "net.netconf.config" collection method.
func Config(ctx context.Context, rc sdk.RunbookContext, device inventory.InventoryItem, params map[string]any) (collection.Result, error) {
	req, err := readRequest(params)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", configFQCN, err)
	}
	// A secret reference names a stored secret (pleiades add-secret), such
	// as an IKE pre-shared key, so no runbook holds one written out; it is
	// resolved here, just before the session opens, and the content is never
	// recorded (run 8 of the blind CVD lab put its key on the CLI for want of
	// this).
	resolved, err := resolveContent(rc, req.content)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", configFQCN, err)
	}

	session, err := openSession(ctx, rc, device, params, configFQCN, req.target)
	if err != nil {
		return collection.Result{}, err
	}
	// Closed without the polite <close-session/> RPC, because that RPC
	// would need a context and this defer runs on the error path too,
	// where the context may already be done. Close on the underlying
	// stream is what actually releases the SSH channel.
	defer func() { _ = session.Close() }()

	discovered := session.Discovery()
	warnings, err := recordDiscovery(rc, params, discovered)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", configFQCN, err)
	}
	result, notes, err := apply(ctx, rc, session, discovered, req, resolved)
	warnings = append(warnings, notes...)
	for _, w := range session.TakeWarnings() {
		warnings = append(warnings, "the device warned: "+w.Error())
	}
	if len(warnings) > 0 {
		if werr := sdk.RecordWarnings(rc, warnings); werr != nil && err == nil {
			err = werr
		}
	}
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", configFQCN, err)
	}
	return result, nil
}

// statValidated and statApplied say what a candidate edit that was not
// committed did: whether the device validated it, and that it was not
// applied.
const (
	statValidated = "validated"
	statApplied   = "applied"
)

// apply runs req on an open session, choosing nothing the session's
// discovery did not allow, with resolved the content as sent (its secret
// references resolved). It returns the warnings the task should carry.
func apply(ctx context.Context, rc sdk.RunbookContext, session netconfSession, discovered netconf.Discovery, req configRequest, resolved string) (collection.Result, []string, error) {
	target := session.Target()
	candidate := target == netconf.Candidate
	if !req.commit && !candidate {
		return collection.Result{}, nil, commitFalseRefusal(target)
	}
	if candidate && req.lockSet && req.lock == lockNever {
		return collection.Result{}, nil, errLockNeverOnCandidate
	}
	unserved, err := unservedNamespaces(req.content, discovered)
	if err != nil {
		return collection.Result{}, nil, err
	}
	if len(unserved) > 0 {
		return collection.Result{}, nil, fmt.Errorf("%s names %s, which no YANG module of the %d the device implements has as its namespace; nothing was sent", paramContent, strings.Join(unserved, ", "), len(discovered.Modules))
	}

	// The candidate is always locked, so this task commits only its own
	// edit and discards only what it staged itself: a lock is refused while
	// another client's changes sit in the candidate.
	lock := &heldLock{ctx: ctx, session: session}
	if candidate || req.lock == lockAlways || (req.lock == lockIfSupported && session.HasCapability(netconf.CapabilityCandidate)) {
		if err := lock.take(ctx); err != nil && (candidate || req.lock == lockAlways) {
			return collection.Result{}, nil, fmt.Errorf("locking the %s datastore: %w", target, err)
		}
		defer lock.release()
	}
	// The discard runs on a context of its own, bounded: a task canceled or
	// timed out mid-edit still discards what it staged. One that fails keeps
	// the lock, and the session's close ends it (config_lock.go).
	discard := func(cause error) error {
		if candidate && lock.held {
			if err := lock.discard(); err != nil {
				return fmt.Errorf("%w; discarding the staged edit failed too, so the candidate's lock is kept and the session closed rather than unlocked, which leaves the edit to no other client: %v", cause, err)
			}
		}
		return cause
	}

	if req.backup {
		if err := recordBackup(ctx, rc, session); err != nil {
			return collection.Result{}, nil, err
		}
	}
	if !req.commit {
		return validateOnly(ctx, rc, session, req, resolved, lock, discard)
	}

	// What the edit can touch, read where it lands: running, through the
	// candidate's commit, or startup.
	source := netconf.Running
	if target == netconf.Startup {
		source = netconf.Startup
	}
	filter, err := netconf.EditFilter(req.content, req.defaultOperation == netconf.DefaultOperationReplace)
	if err != nil {
		return collection.Result{}, nil, err
	}
	before, err := session.ReadConfig(ctx, source, filter)
	if err != nil {
		return collection.Result{}, nil, fmt.Errorf("reading the configuration the edit touches before sending it: %w; nothing was sent", err)
	}
	u, err := newUndo(rc, session, req, before)
	if err != nil {
		return collection.Result{}, nil, err
	}

	if err := session.EditConfig(ctx, netconf.EditConfigRequest{Config: resolved, DefaultOperation: req.defaultOperation, ErrorOption: req.errorOption}); err != nil {
		return collection.Result{}, u.lost(), discard(err)
	}
	if candidate {
		if err := commitCandidate(ctx, session, discard); err != nil {
			return collection.Result{}, u.lost(), err
		}
	}
	after, err := session.ReadConfig(ctx, source, filter)
	if err != nil {
		return collection.Result{Changed: true}, u.lost(), fmt.Errorf("the device accepted the edit, and reading the configuration after it failed, so whether it changed is unknown: %w", err)
	}
	changed := yangdoc.CanonicalElements(before) != yangdoc.CanonicalElements(after)
	notes, err := u.finish(changed, after)
	return collection.Result{Changed: changed}, notes, err
}

// commitCandidate validates the candidate where the device offers :validate,
// then commits it, discarding the edit on any failure. A candidate edit has
// changed nothing on the device until it is committed, so a change is
// reported only after this.
func commitCandidate(ctx context.Context, session netconfSession, discard func(error) error) error {
	if session.CanValidate() {
		if err := session.Validate(ctx); err != nil {
			return discard(fmt.Errorf("validating the candidate: %w", err))
		}
	}
	if err := session.Commit(ctx); err != nil {
		var refused netconf.RPCErrors
		if !errors.As(err, &refused) {
			// No answer came back (a lost session, a deadline): the commit
			// may have been applied, and the running configuration is the
			// only witness of whether it was.
			return discard(fmt.Errorf("committing: no answer came back, so whether the commit was applied is unknown; read the running configuration to tell: %w", err))
		}
		return discard(fmt.Errorf("committing: %w", err))
	}
	return nil
}

// validateOnly is commit: false on the candidate: the edit validated and
// discarded, so it is checked by the device and applied nowhere. The discard
// of the validated edit runs on a context of its own, as every discard does.
func validateOnly(ctx context.Context, rc sdk.RunbookContext, session netconfSession, req configRequest, resolved string, lock *heldLock, discard func(error) error) (collection.Result, []string, error) {
	if err := session.EditConfig(ctx, netconf.EditConfigRequest{Config: resolved, DefaultOperation: req.defaultOperation, ErrorOption: req.errorOption}); err != nil {
		return collection.Result{}, nil, discard(err)
	}
	validated := false
	if session.CanValidate() {
		if err := session.Validate(ctx); err != nil {
			return collection.Result{}, nil, discard(fmt.Errorf("validating the candidate: %w", err))
		}
		validated = true
	}
	if err := lock.discard(); err != nil {
		return collection.Result{}, nil, fmt.Errorf("discarding the validated edit failed, so the candidate's lock is kept and the session closed rather than unlocked: %w", err)
	}
	if err := rc.SetStat(statValidated, validated); err != nil {
		return collection.Result{}, nil, err
	}
	return collection.Result{Changed: false}, nil, rc.SetStat(statApplied, false)
}

// recordBackup records the running datastore's full contents, masked, before
// anything is applied: the configuration in effect is running's, whichever
// datastore the task writes, and a backup masking cannot vouch for stops the
// task here, so the change never goes ahead without the backup it asked for.
func recordBackup(ctx context.Context, rc sdk.RunbookContext, session netconfSession) error {
	payload, err := session.GetConfigFrom(ctx, netconf.Running, datastore.Path{})
	if err != nil {
		return fmt.Errorf("capturing backup: %w", err)
	}
	masked, err := maskedBackup(rc, string(payload.Bytes))
	if err != nil {
		return fmt.Errorf("capturing backup: %w", err)
	}
	return rc.SetStat(statBackup, masked)
}

// cleanupBudget bounds the candidate's discard and unlock after the task's
// own context ended.
const cleanupBudget = 10 * time.Second

// cleanupContext is a context for cleanup that outlives the task's: not
// canceled with it, bounded by cleanupBudget.
func cleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), cleanupBudget)
}
