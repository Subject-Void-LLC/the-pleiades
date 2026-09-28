// The operating systems virt.vbox.vm.install installs, one strategy each:
// the parameters only it takes, the answer medium it boots beside its ISO,
// and what it does once the VM is running.
//
// One method with an installer parameter rather than a method per system,
// because everything else is the same: the VM, its disk, its two DVD
// drives, the wait for the install to power the VM off, taking the media
// out and marking the VM installed. The parameter is declared by the task,
// never inferred from os_type, so a mistyped OS type cannot choose a
// different install.
package vm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/bsdinstall"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winunattend"
)

// The installer parameter and its values.
const (
	paramInstaller   = "installer"
	installerWindows = "windows"
	installerFreeBSD = "freebsd"
)

// installers are the installer parameter's values, in the order a
// message lists them.
var installers = []string{installerWindows, installerFreeBSD}

// installer is one operating system's unattended install.
type installer interface {
	// read takes this installer's own parameters and refuses the other
	// installers' ones, which it would otherwise ignore.
	read(params map[string]any) error
	// system names the operating system, for a message.
	system() string
	// minDiskGB is the smallest disk it installs onto.
	minDiskGB() int
	// answer is the DVD image it boots beside the installation ISO.
	answer(modified time.Time) ([]byte, error)
	// consoleLog says whether the install VM writes its serial console to
	// console.log in its folder, which started reads.
	consoleLog() bool
	// started runs once the VM is started and before the wait for it to
	// power off. dir is the VM's folder.
	started(ctx context.Context, h vboxmanage.Host, name, dir string) error
}

// readInstaller chooses the installer the task names.
func readInstaller(params map[string]any) (installer, error) {
	name, err := sdk.RequiredStringParam(params, paramInstaller)
	if err != nil {
		return nil, err
	}
	switch name {
	case installerWindows:
		return &windowsInstaller{}, nil
	case installerFreeBSD:
		return &freebsdInstaller{}, nil
	}
	return nil, fmt.Errorf("installer %q is not one of %v", name, installers)
}

// windowsInstaller installs Windows from Microsoft's media with an answer
// file (Autounattend.xml) Setup reads unasked, through audit mode, and
// generalizes it; its last step powers the VM off.
type windowsInstaller struct {
	install winunattend.Install
}

func (w *windowsInstaller) read(params map[string]any) error {
	image, err := sdk.RequiredStringParam(params, paramImage)
	if err != nil {
		return err
	}
	w.install = winunattend.Install{Image: image, Language: sdk.StringParam(params, paramLanguage)}
	if w.install.AuditPassword, err = winunattend.NewPassword(); err != nil {
		return err
	}
	return w.install.Validate()
}

func (w *windowsInstaller) system() string { return "Windows Server" }

// minDiskGB is the smallest disk Windows Server installs on.
func (w *windowsInstaller) minDiskGB() int { return 32 }

func (w *windowsInstaller) answer(modified time.Time) ([]byte, error) {
	return w.install.ISO(modified)
}

func (w *windowsInstaller) consoleLog() bool { return false }

// started does nothing: Setup finds its answer file on the second DVD by
// itself.
func (w *windowsInstaller) started(context.Context, vboxmanage.Host, string, string) error {
	return nil
}

// freebsdInstaller installs FreeBSD from its DVD with bsdinstall's script
// (installerconfig), which a released DVD cannot hold, so the script comes
// on a second DVD and is started from the installer's Welcome dialog once
// the serial console shows it; the typed command powers the VM off when
// the script succeeds.
type freebsdInstaller struct {
	install bsdinstall.Install
}

func (f *freebsdInstaller) read(params map[string]any) error {
	for _, windowsOnly := range []string{paramImage, paramLanguage} {
		if _, set := params[windowsOnly]; set {
			return fmt.Errorf("%s is for the windows installer; the freebsd installer installs the base system on the DVD", windowsOnly)
		}
	}
	return f.install.Validate()
}

func (f *freebsdInstaller) system() string { return "FreeBSD" }

// minDiskGB leaves the base system room for its swap partition and for
// what a clone adds.
func (f *freebsdInstaller) minDiskGB() int { return 8 }

func (f *freebsdInstaller) answer(modified time.Time) ([]byte, error) {
	return f.install.ISO(modified)
}

func (f *freebsdInstaller) consoleLog() bool { return true }

// freebsdReadyTimeout bounds the wait from starting the VM to the
// installer's Welcome dialog: the loader's countdown and a kernel boot
// from DVD, well under a minute on the lab host.
var freebsdReadyTimeout = 10 * time.Minute

// freebsdStartTimeout bounds the wait from typing the command to the
// guest's own word that it ran (bsdinstall.Started on the serial port):
// mounting a DVD and one echo.
var freebsdStartTimeout = 2 * time.Minute

// freebsdKeyPause is how long to let the guest catch up before each typed
// step; a test shortens it.
var freebsdKeyPause = 3 * time.Second

// screenProbe is the picture of the screen the installer is watched
// through, in the VM's folder; each look replaces it.
const screenProbe = "install-screen.png"

// screenshotMax bounds reading a screenshot back: a text-mode screen is a
// few kilobytes.
const screenshotMax = 4 << 20

// started starts the script, unless the serial console already says it
// ran, so a run that resumes an install never types into one that is
// under way. It waits for the installer's screen (the serial console is
// silent until the command below speaks), chooses Shell, types the
// command, and waits for the guest to say on the serial port that the
// script started. Whatever stops it saves a picture of the screen.
func (f *freebsdInstaller) started(ctx context.Context, h vboxmanage.Host, name, dir string) error {
	log := dir + `\` + consoleFile
	console, err := h.ReadTail(ctx, log, consoleTail)
	if err != nil && !errors.Is(err, vboxmanage.ErrNoFile) {
		return err
	}
	if bsdinstall.StartedOn(console) {
		return nil
	}
	if err := waitForWelcome(ctx, h, name, dir); err != nil {
		return withScreen(ctx, h, name, dir, err)
	}
	// Each step waits for the guest before typing: the dialog finishing
	// drawing, then the shell starting.
	for _, written := range []string{bsdinstall.ShellKeys, bsdinstall.ScriptCommand} {
		strokes, err := vboxmanage.Keys(written)
		if err != nil {
			return err
		}
		strokes = append([]vboxmanage.Keystroke{{Pause: freebsdKeyPause}}, strokes...)
		if err := h.Type(ctx, name, strokes); err != nil {
			return err
		}
	}
	said := func(console []byte) (bool, error) { return bsdinstall.StartedOn(console), nil }
	if err := watchConsole(ctx, h, name, log, "sign that the install script started", "its VBox.log in "+dir+" says why",
		false, freebsdStartTimeout, said); err != nil {
		return withScreen(ctx, h, name, dir, err)
	}
	return nil
}

// waitForWelcome looks at the VM's screen until it shows the installer
// (bsdinstall.WelcomeOn). A VM that stops is not waited for.
func waitForWelcome(ctx context.Context, h vboxmanage.Host, name, dir string) error {
	probe := dir + `\` + screenProbe
	deadline := time.Now().Add(freebsdReadyTimeout)
	for {
		m, err := h.Machine(ctx, name)
		if err != nil && !errors.Is(err, vboxmanage.ErrLocked) {
			return err
		}
		if err == nil && m.State != vboxmanage.StateRunning {
			return fmt.Errorf("%q is %s before its installer came up; its VBox.log in %s says why", name, m.State, dir)
		}
		if err == nil {
			if err := h.Screenshot(ctx, name, probe); err != nil {
				return err
			}
			picture, err := h.ReadTail(ctx, probe, screenshotMax)
			if err != nil {
				return err
			}
			if up, err := bsdinstall.WelcomeOn(picture); err != nil {
				return err
			} else if up {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%q's screen did not show the FreeBSD installer within %s", name, freebsdReadyTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// withScreen adds a picture of the VM's screen to err, saved in its
// folder, when one can be taken: a VM that stopped has no screen.
func withScreen(ctx context.Context, h vboxmanage.Host, name, dir string, err error) error {
	picture := dir + `\` + screenshotFile
	if shotErr := h.Screenshot(ctx, name, picture); shotErr == nil {
		return fmt.Errorf("%w; a picture of its screen is at %s", err, picture)
	}
	return err
}
