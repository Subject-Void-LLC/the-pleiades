// Package netconf: a check's own sequence through the candidate datastore:
// its turn on the device, the lock taken first, the candidate proven clean,
// the edit staged and validated, and the discard proven before the lock is
// released. A discard that fails or is not proven keeps the lock, and the
// session's close ends it (config_lock.go).
package netconf

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yangdoc"
)

// oneCheckPerDevice waits for any check of device running in this process to
// end, as long as ctx allows, and returns the release of its own turn.
func oneCheckPerDevice(ctx context.Context, device inventory.InventoryItem) (func(), error) {
	turn := make(chan struct{})
	for {
		held, busy := checkingDevices.LoadOrStore(device.ID(), turn)
		if !busy {
			return func() { checkingDevices.Delete(device.ID()); close(turn) }, nil
		}
		select {
		case <-held.(chan struct{}):
		case <-ctx.Done():
			return nil, fmt.Errorf("%s: waiting for another check of %s to end: %w", configFQCN, device.Name(), ctx.Err())
		}
	}
}

// checkThroughCandidate takes the candidate's lock first, proves it clean in
// what the edit touches, stages the edit, has the device validate it, reads
// the candidate, discards the edit and proves the candidate as it was, holding
// the lock for at most bound. It reports whether the edit changed the
// candidate. A refused lock is not checked, with nothing sent; any failure
// once the edit is sent fails the check, as unverified unless the discard is
// proven, and an unproven discard keeps the lock for the session's close.
func checkThroughCandidate(ctx context.Context, session netconfSession, req configRequest, bound time.Duration) (bool, error) {
	lctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	lock := &heldLock{ctx: ctx, session: session}
	if err := lock.take(lctx); err != nil {
		var refused netconf.RPCErrors
		if !errors.As(err, &refused) {
			return false, fmt.Errorf("locking the candidate: %w", err)
		}
		if holder, denied := netconf.LockHolder(err); denied && holder != "" {
			return false, collection.CannotCheck("the candidate datastore is locked by session " + holder + "; nothing was sent")
		}
		return false, collection.CannotCheck("the device refused the candidate's lock (" + err.Error() + "), as it does while the candidate holds changes not committed; nothing was sent")
	}
	defer lock.release()

	filter, err := netconf.EditFilter(req.content, req.defaultOperation == netconf.DefaultOperationReplace)
	if err != nil {
		return false, err
	}
	candidate, err := session.ReadConfig(lctx, netconf.Candidate, filter)
	if err != nil {
		return false, fmt.Errorf("reading the candidate before the check: %w; nothing was sent", err)
	}
	running, err := session.ReadConfig(lctx, netconf.Running, filter)
	if err != nil {
		return false, fmt.Errorf("reading running before the check: %w; nothing was sent", err)
	}
	before := yangdoc.CanonicalElements(candidate)
	if before != yangdoc.CanonicalElements(running) {
		return false, collection.CannotCheck("the candidate datastore holds changes not committed in what this task touches, which a check would discard; nothing was sent")
	}

	// From the edit on, the lock is released only once the discard is
	// proven.
	lock.staged = true
	edited, sendErr := stageAndValidate(lctx, session, req, filter)
	// The discard is sent whatever happened, on a context of its own, and
	// proven: a check never leaves its edit staged for the next commit.
	if err := lock.discard(); err != nil {
		return false, unverified(errors.Join(sendErr, fmt.Errorf("discard-changes: %w", err)))
	}
	lock.staged = true
	cctx, ccancel := cleanupContext(ctx)
	defer ccancel()
	after, err := session.ReadConfig(cctx, netconf.Candidate, filter)
	if err != nil {
		return false, unverified(errors.Join(sendErr, fmt.Errorf("reading the candidate after the discard: %w", err)))
	}
	if yangdoc.CanonicalElements(after) != before {
		return false, unverified(errors.Join(sendErr, errors.New("after the discard, the candidate does not read as it did before the check")))
	}
	lock.staged = false
	var refused netconf.RPCErrors
	switch {
	case errors.As(sendErr, &refused):
		return false, fmt.Errorf("the device refused the document, so a real run would fail: %w", sendErr)
	case sendErr != nil:
		return false, fmt.Errorf("%w; the candidate was discarded and proven to read as it did before", sendErr)
	}
	return edited != before, nil
}

// stageAndValidate sends the edit to the candidate, has the device validate
// it and reads the candidate through filter, returning what it read, or the
// first failure, named by its stage.
func stageAndValidate(ctx context.Context, session netconfSession, req configRequest, filter string) (string, error) {
	if err := session.EditConfig(ctx, netconf.EditConfigRequest{Config: req.content, DefaultOperation: req.defaultOperation, ErrorOption: req.errorOption}); err != nil {
		return "", fmt.Errorf("staging the edit in the candidate: %w", err)
	}
	if err := session.Validate(ctx); err != nil {
		return "", fmt.Errorf("validating the candidate: %w", err)
	}
	read, err := session.ReadConfig(ctx, netconf.Candidate, filter)
	if err != nil {
		return "", fmt.Errorf("reading the candidate after the edit: %w", err)
	}
	return yangdoc.CanonicalElements(read), nil
}

// unverified is a check that failed between sending the edit and proving the
// discard: the candidate may still hold the edit, so the lock is kept and the
// session closed rather than unlocked.
func unverified(cause error) error {
	return fmt.Errorf("failed: candidate state unverified (%w); the candidate's lock was kept and the session closed rather than unlocked, which a server measured to qualify ends by discarding the candidate: read it, and discard-changes from a session of your own if it still holds this check's edit", cause)
}
