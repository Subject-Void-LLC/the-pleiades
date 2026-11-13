// A Windows clone proven to have taken its answer file, and a failed
// clone thrown away with its seed: the answer file holds the
// Administrator's password in clear, so no failure leaves it on the host.
package vm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// defaultProveTimeout is how long a Windows clone's first boot is given to
// let its seeded Administrator in. Measured on the lab host on 2026-11-13:
// a clone of the evaluation VHDX at one vCPU and 1536 MB did in about four
// minutes from its start.
const defaultProveTimeout = 1200

// proveInterval is the pause between tries at the clone's login; a test
// shortens it.
var proveInterval = 15 * time.Second

// proveTry bounds one try at the clone's login.
const proveTry = 30 * time.Second

// proveGuest logs in to the Windows machine at address as the built-in
// Administrator with password, over WinRM on its default HTTP port, and
// returns the computer name it reports. A test swaps it for a scripted
// guest; the lab test reaches a real clone.
var proveGuest = func(ctx context.Context, address, password string) (string, error) {
	res, err := winrmexec.Run(ctx, winrmexec.Target{Host: address}, winrmexec.Auth{Username: windowsAdministrator, Password: password},
		winrmexec.ShellCmd, "hostname", winrmexec.Options{Timeout: proveTry})
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("hostname exited %d", res.ExitCode)
	}
	return strings.TrimSpace(res.Stdout), nil
}

// proveWindowsClone starts the clone and waits for its seeded
// Administrator to log in at its fixed address, reporting the computer
// name the answer file set. A base that is not generalized ignores the
// answer file without a word and boots with its own account, so a clone
// that never lets the seeded login in, or names itself otherwise, is a
// failure: the caller powers it off and throws it away.
func proveWindowsClone(ctx context.Context, h vboxmanage.Host, name string, c cloneRequest, login seedLogin) error {
	m, err := h.Machine(ctx, name)
	if err != nil {
		return err
	}
	if err := fitsNow(ctx, h, m); err != nil {
		return err
	}
	// Started as virt.vbox.vm.start starts a VM, so it outlives the WinRM
	// session that started it.
	if _, err := (vboxmanage.WindowsAutostart{Host: h}).Start(ctx, name); err != nil {
		return err
	}
	address := c.address.Addr().String()
	deadline := time.Now().Add(c.proveTimeout)
	for {
		computer, err := proveGuest(ctx, address, login.password)
		if err == nil {
			if !strings.EqualFold(computer, c.hostname) {
				return fmt.Errorf("%s let its seeded Administrator in at %s but names itself %q, not %q: the answer file did not take, or another machine answers at that address", name, address, computer, c.hostname)
			}
			return nil
		}
		m, merr := h.Machine(ctx, name)
		if merr != nil {
			return merr
		}
		if m.State != vboxmanage.StateRunning {
			return fmt.Errorf("%s stopped (%s) before its seeded Administrator could log in at %s; the last try said: %v", name, m.State, address, err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not let its seeded Administrator log in at %s within %s; a base that is not generalized ignores the answer file and keeps its own account and name, and the last try said: %v", name, address, c.proveTimeout, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(proveInterval):
		}
	}
}

// settleTimeout bounds the shutdown after a Windows clone is proven.
const settleTimeout = 5 * time.Minute

// settleWindowsClone shuts a proven Windows clone down through its power
// button, cutting the power if it has not stopped within settleTimeout,
// and then takes its seed out and deletes it: Windows has read the answer
// file, which holds the Administrator's password in clear, and a stopped
// VM no longer locks the image. The clone is left as a Linux one is, made
// and not running.
func settleWindowsClone(ctx context.Context, h vboxmanage.Host, name, seed string) error {
	if err := stopVM(ctx, h, name, modeACPI, settleTimeout); err != nil {
		if err := h.PowerOff(ctx, name); err != nil && !errors.Is(err, vboxmanage.ErrNotRunning) {
			return err
		}
	}
	// A start through the autostart service marks the VM to start with
	// it, which a VM that is to stay stopped must not keep.
	m, err := h.Machine(ctx, name)
	if err != nil {
		return err
	}
	if m.AutostartEnabled {
		if err := clearAutostart(ctx, h, name); err != nil {
			return err
		}
	}
	return dropSeed(ctx, h, name, seed)
}

// discardClone throws away a clone that failed after VirtualBox made it,
// so a later run makes it again rather than taking a half-made one for
// finished: it powers the VM off if it runs, and deletes it with its seed.
// When the delete fails, the seed is still taken out of its drive and
// deleted on its own, since it holds a Windows Administrator's password in
// clear or a hash of a Linux login's. cause is the failure, and the error
// returned says what became of the VM and of the seed.
func discardClone(ctx context.Context, h vboxmanage.Host, name, seed string, cause error) error {
	if m, err := h.Machine(ctx, name); err == nil && (m.State == vboxmanage.StateRunning || m.State == vboxmanage.StatePaused) {
		if err := h.PowerOff(ctx, name); err != nil && !errors.Is(err, vboxmanage.ErrNotRunning) {
			cause = fmt.Errorf("%w; powering it off failed: %v", cause, err)
		}
	}
	cleanup := deleteWhenUnlocked(ctx, h, name)
	if cleanup == nil {
		return fmt.Errorf("%w; the half-made %s was deleted, its seed with it", cause, name)
	}
	if seed == "" {
		return fmt.Errorf("%w; deleting the half-made %s then failed too: %v", cause, name, cleanup)
	}
	if err := dropSeed(ctx, h, name, seed); err != nil {
		return fmt.Errorf("%w; deleting the half-made %s then failed too: %v; and its seed %s, which holds its login, could not be deleted: %v", cause, name, cleanup, seed, err)
	}
	return fmt.Errorf("%w; deleting the half-made %s then failed too: %v; its seed %s was deleted", cause, name, cleanup, seed)
}

// deleteWhenUnlocked is deleteVM, tried again a few times while VirtualBox
// says the machine is locked, as it is for a moment after a power off
// while the session lets go of it.
func deleteWhenUnlocked(ctx context.Context, h vboxmanage.Host, name string) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = deleteVM(ctx, h, name); !errors.Is(err, vboxmanage.ErrLocked) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
	return err
}

// dropSeed takes the seed at path out of every drive of name holding it,
// forgets the image and deletes the file. A VM VirtualBox no longer knows,
// or an image it never registered, is not a failure: the file is deleted
// all the same.
func dropSeed(ctx context.Context, h vboxmanage.Host, name, path string) error {
	if m, err := h.Machine(ctx, name); err == nil {
		for _, slot := range holding(m, path) {
			if err := h.EjectDVD(ctx, name, slot); err != nil {
				return err
			}
		}
		if err := closeSeed(ctx, h, path); err != nil {
			return err
		}
	}
	return h.Remove(ctx, path, false)
}
