// Starting a VM through the host account's VirtualBox autostart service,
// on a Windows host.
package vboxmanage

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// WindowsAutostart starts VMs on a Windows host through VBoxAutostartSvc,
// the service VirtualBox installs per account to start its VMs under a
// service logon.
//
// It exists because a VM cannot start from a WinRM logon: Windows' catalog
// signature check fails for a non-administrator, non-interactive logon,
// and VirtualBox's hardening then refuses the hypervisor API (VirtualBox
// ticket 20341). A VM the service starts runs under the service logon, and
// the VirtualBox server that logon starts becomes the one every later
// client of the same account is handed; while any of the account's VMs
// runs, a plain Start works through it (measured on VirtualBox 7.2.20).
type WindowsAutostart struct {
	Host Host
	// Poll is how often a wait looks again; zero means one second.
	Poll time.Duration
	// Timeout bounds each wait; zero means one minute.
	Timeout time.Duration
}

// The Windows programs a start runs, by absolute path, so no program of
// the same name elsewhere on the host's search path is run instead.
const (
	whoamiPath   = `C:\Windows\System32\whoami.exe`
	scPath       = `C:\Windows\System32\sc.exe`
	tasklistPath = `C:\Windows\System32\tasklist.exe`
)

// ServiceName returns the account's autostart service name, as
// VBoxAutostartSvc names it: its prefix, the domain (the computer name,
// for a local account) in lower case, and the account name.
func (a WindowsAutostart) ServiceName(ctx context.Context) (string, error) {
	domain, user, err := a.account(ctx)
	if err != nil {
		return "", err
	}
	return serviceName(domain, user), nil
}

// serviceName is VBoxAutostartSvc's name for domain\user.
func serviceName(domain, user string) string {
	return "VBoxAutostartSvc" + strings.ToLower(domain) + user
}

// account returns the account the runner logs in as, from whoami.
func (a WindowsAutostart) account(ctx context.Context) (domain, user string, err error) {
	out, err := a.Host.Runner.Run(ctx, whoamiPath, nil)
	if err != nil {
		return "", "", err
	}
	name := strings.TrimSpace(out.Stdout)
	domain, user, ok := strings.Cut(name, `\`)
	if out.ExitCode != 0 || !ok || domain == "" || user == "" {
		return "", "", fmt.Errorf("vboxmanage: whoami gave %q, not DOMAIN\\user", name)
	}
	return domain, user, nil
}

// Start starts vm, returning whether it went through the service. When any
// of the account's VMs already runs, it is a plain Start; otherwise the VM
// is marked for autostart, the account's own VirtualBox server is let exit,
// the service is started and waited for, and the VM's state is read.
//
// The order is the point. A VBoxManage call made before the service's
// server registers would start a server under this runner's logon and be
// the one the service is handed, and the VM would fail to start as before.
// So nothing calls VBoxManage from the moment the account's server has
// exited until the service has stopped again.
//
// The autostart mark is left on: VirtualBox does not change a running
// machine's settings. The caller clears it once the VM stops.
func (a WindowsAutostart) Start(ctx context.Context, vm string) (bool, error) {
	running, err := a.Host.Running(ctx)
	if err != nil {
		return false, err
	}
	if len(running) > 0 {
		return false, a.Host.Start(ctx, vm)
	}
	domain, user, err := a.account(ctx)
	if err != nil {
		return true, err
	}
	service := serviceName(domain, user)
	if err := a.Host.SetAutostart(ctx, vm, true); err != nil {
		return true, err
	}
	if err := a.wait(ctx, "the account's own VirtualBox server to exit", func() (bool, error) {
		out, err := a.Host.Runner.Run(ctx, tasklistPath, []string{
			"/FI", "IMAGENAME eq VBoxSVC.exe", "/FI", "USERNAME eq " + domain + `\` + user, "/NH", "/FO", "CSV"})
		return err == nil && !strings.Contains(out.Stdout, "VBoxSVC.exe"), err
	}); err != nil {
		return true, err
	}
	out, err := a.Host.Runner.Run(ctx, scPath, []string{"start", service})
	if err != nil {
		return true, err
	}
	if out.ExitCode != 0 {
		return true, fmt.Errorf("vboxmanage: starting %s: %s", service, firstErrorLine(out))
	}
	if err := a.wait(ctx, service+" to finish", func() (bool, error) {
		out, err := a.Host.Runner.Run(ctx, scPath, []string{"query", service})
		return err == nil && strings.Contains(out.Stdout, "STOPPED"), err
	}); err != nil {
		return true, err
	}
	m, err := a.Host.Machine(ctx, vm)
	if err != nil {
		return true, err
	}
	if m.State != StateRunning {
		return true, fmt.Errorf("vboxmanage: %s ran and %s is %s; its log is VBoxAutostart.log in the account's .VirtualBox folder", service, vm, m.State)
	}
	return true, nil
}

// wait polls done until it reports true, it fails, or the timeout passes.
func (a WindowsAutostart) wait(ctx context.Context, what string, done func() (bool, error)) error {
	poll, timeout := a.Poll, a.Timeout
	if poll <= 0 {
		poll = time.Second
	}
	if timeout <= 0 {
		timeout = time.Minute
	}
	deadline := time.Now().Add(timeout)
	for {
		ok, err := done()
		if err != nil {
			return fmt.Errorf("vboxmanage: waiting for %s: %w", what, err)
		}
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("vboxmanage: %s did not happen within %s", what, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}
