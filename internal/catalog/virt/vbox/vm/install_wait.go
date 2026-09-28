// Waiting for virt.vbox.vm.install's VM to finish, finishing it, and
// resuming an install a run stopped waiting for.
package vm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

// resumeInstall answers for a VM already under the name. One marked
// installed is reported with no change. One this method started that is
// not finished (its answer file is still in a drive) is waited for again
// and finished, so a run that stopped waiting (a timeout, a failed read)
// is continued by running the task again. Any other VM is refused.
func resumeInstall(ctx context.Context, rc sdk.RunbookContext, h vboxmanage.Host, fqcn string, m vboxmanage.Machine, r installRequest, mode collection.Mode) (collection.Result, error) {
	extra, err := h.ExtraData(ctx, m.Name)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if extra[vboxmanage.ExtraInstalled] != "" {
		return collection.Result{}, recordInstalled(rc, fqcn, m)
	}
	dir, err := vmDir(m)
	if err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if len(holding(m, dir+`\`+answerFile)) == 0 {
		return collection.Result{}, fmt.Errorf("%s: %q exists but was not installed by this method, or its install was taken apart; delete it with virt.vbox.vm.delete and run this again", fqcn, m.Name)
	}
	if err := rc.SetStat(statSize, shapeOf(m).size()); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if mode == collection.ModeCheck {
		return collection.Result{Changed: true}, recordExists(rc, fqcn, true, true)
	}
	if err := waitForInstall(ctx, h, m.Name, dir, r.timeout); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := finishInstall(ctx, h, m.Name, dir, r.iso); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := rc.SetStat(statUUID, m.UUID); err != nil {
		return collection.Result{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return collection.Result{Changed: true}, recordExists(rc, fqcn, true, true)
}

// recordInstalled reports a VM marked installed.
func recordInstalled(rc sdk.RunbookContext, fqcn string, m vboxmanage.Machine) error {
	if err := rc.SetStat(statUUID, m.UUID); err != nil {
		return fmt.Errorf("%s: %w", fqcn, err)
	}
	if err := rc.SetStat(statSize, shapeOf(m).size()); err != nil {
		return fmt.Errorf("%s: %w", fqcn, err)
	}
	return recordExists(rc, fqcn, true, true)
}

// waitForInstall waits for the VM to shut itself down, which the answer
// file's last step does once Windows is generalized. A VM still running
// at the timeout is left running, with a picture of its screen saved in
// its folder.
func waitForInstall(ctx context.Context, h vboxmanage.Host, name, dir string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		m, err := h.Machine(ctx, name)
		// Another client's lock (a screenshot, a log read) is a moment,
		// not an answer: look again.
		if errors.Is(err, vboxmanage.ErrLocked) {
			m.State = vboxmanage.StateRunning
		} else if err != nil {
			return err
		}
		switch m.State {
		case vboxmanage.StatePoweroff:
			return nil
		case vboxmanage.StateAborted:
			return fmt.Errorf("%s stopped abnormally (aborted) during its install; its VBox.log in %s says why", name, dir)
		}
		if time.Now().After(deadline) {
			picture := dir + `\` + screenshotFile
			if err := h.Screenshot(ctx, name, picture); err != nil {
				return fmt.Errorf("%s's install had not finished within %s, and its screen could not be saved: %w", name, timeout, err)
			}
			return fmt.Errorf("%s's install had not finished within %s; it is left %s, and a picture of its screen is at %s", name, timeout, m.State, picture)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(installPoll):
		}
	}
}

// finishInstall clears the autostart mark the start left, takes the ISO
// and the answer file out of the drives, deletes the answer file, and
// marks the VM installed.
func finishInstall(ctx context.Context, h vboxmanage.Host, name, dir, iso string) error {
	if err := clearAutostart(ctx, h, name); err != nil {
		return err
	}
	m, err := h.Machine(ctx, name)
	if err != nil {
		return err
	}
	answer := dir + `\` + answerFile
	for _, slot := range m.Slots {
		if strings.EqualFold(slot.Medium, iso) || strings.EqualFold(slot.Medium, answer) {
			if err := h.Detach(ctx, name, slot); err != nil {
				return err
			}
		}
	}
	if err := h.CloseDVD(ctx, answer, true); err != nil {
		return err
	}
	return h.SetExtraData(ctx, name, vboxmanage.ExtraInstalled, now().UTC().Format(time.RFC3339))
}
