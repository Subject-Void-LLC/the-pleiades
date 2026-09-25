// The SSH probe: one constant script, and what its output proves.
package onboard

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// sshProbeScript is the one command the SSH probe runs. It is a constant:
// no inventory value, device name or credential reaches it. It reports
// with printf rather than echo, since echo's handling of backslashes
// differs between shells, and reads os-release line by line rather than
// sourcing it, so nothing in the device's own files is executed.
//
// It prints a marker before anything else. A login that lands somewhere
// other than a POSIX shell (a router's command line) does not print it,
// and the probe then proves the SSH login and nothing more. Only lines
// after the marker count, so a banner a login script prints first is
// ignored rather than read as a fact.
const sshProbeScript = `printf 'pleiades-probe=1\n'
printf 'kernel=%s\n' "$(uname -s 2>/dev/null)"
printf 'kernel_release=%s\n' "$(uname -r 2>/dev/null)"
if [ -r /etc/os-release ]; then
  while IFS='=' read -r k v; do
    if [ "$k" = ID ]; then printf 'os_id=%s\n' "$v"; fi
  done < /etc/os-release
fi
for c in apt-get dpkg dnf rpm systemctl firewall-cmd useradd usermod userdel groupadd groupmod groupdel getent; do
  if command -v "$c" >/dev/null 2>&1; then printf 'has=%s\n' "$c"; fi
done
if [ -d /run/systemd/system ]; then printf 'systemd_running=1\n'; fi
exit 0`

// posixAccountTools are the commands identity.user.* and identity.group.*
// run; PosixAccountCapable is granted only when every one is present.
var posixAccountTools = []string{"useradd", "usermod", "userdel", "groupadd", "groupmod", "groupdel", "getent"}

type sshProber struct{}

func init() { Register(generic.TypeSSH, sshProber{}) }

func (sshProber) Protocol() string { return "ssh" }

// Probe logs in with the device's credential, verifying its host key
// against known_hosts as every other SSH connection here does, and runs
// sshProbeScript.
func (sshProber) Probe(ctx context.Context, device inventory.InventoryItem, secrets map[string]string) (Probed, error) {
	dev, ok := device.(capability.SSHTransportCapable)
	if !ok {
		return Probed{}, errors.New("the device is not reachable over SSH")
	}
	auth, err := remoteexec.AuthFromSecrets(secrets)
	if err != nil {
		return Probed{}, err
	}
	target := remoteexec.Target{Host: dev.SSHHost(), Port: dev.SSHPort()}
	res, err := remoteexec.Shared(remoteexec.Options{}).Run(ctx, nil, target, auth, sshProbeScript)
	if err != nil {
		return Probed{}, err
	}
	return parseSSHProbe(res.Stdout), nil
}

// parseSSHProbe maps the probe script's output to capabilities. Only the
// script's own key=value lines count, and each capability needs every
// fact behind it: a package manager needs its low-level tool too (dpkg for
// apt, rpm for dnf), firewalld needs a running systemd, and every Linux
// capability needs the kernel to say Linux.
func parseSSHProbe(out string) Probed {
	lines := strings.Split(out, "\n")
	start := slices.IndexFunc(lines, func(l string) bool { return strings.TrimSpace(l) == "pleiades-probe=1" })
	if start < 0 {
		return Probed{Facts: map[string]any{"shell": "not posix"}}
	}
	has := map[string]bool{}
	facts := map[string]any{"shell": "posix"}
	systemdRunning := false
	for _, line := range lines[start+1:] {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "kernel", "kernel_release":
			facts[key] = factText(value)
		case "os_id":
			facts[key] = factText(strings.Trim(value, `"'`))
		case "has":
			has[value] = true
		case "systemd_running":
			systemdRunning = value == "1"
		}
	}

	caps := []capability.Name{capability.NameShellExec}
	if facts["kernel"] != "Linux" {
		return Probed{Capabilities: caps, Facts: facts}
	}
	caps = append(caps, capability.NameLinux, capability.NamePOSIXFileSystem, capability.NameFactGatherer)
	systemd := systemdRunning && has["systemctl"]
	if systemd {
		caps = append(caps, capability.NameSystemd)
		if has["firewall-cmd"] {
			caps = append(caps, capability.NameFirewalld)
		}
	}
	if has["apt-get"] && has["dpkg"] {
		caps = append(caps, capability.NameApt)
	}
	if has["dnf"] && has["rpm"] {
		caps = append(caps, capability.NameDnf)
	}
	account := true
	for _, tool := range posixAccountTools {
		account = account && has[tool]
	}
	if account {
		caps = append(caps, capability.NamePosixAccount)
	}
	return Probed{Capabilities: caps, Facts: facts}
}
