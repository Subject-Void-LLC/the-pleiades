// The services subset through the real binary against a real systemd host
// whose sshd is socket activated with Accept=yes, as Fedora CoreOS and
// Flatcar run it, so every gather is itself a new connection: two gathers
// of a host where only logins came and went must name the same services.
package main_test

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// busProbeSetup declares a bus name with no systemd unit of its own and asks
// for it, so dbus-broker starts it as a transient unit, as it does for such
// names on a Fedora or RHEL host.
const busProbeSetup = `cat > /usr/share/dbus-1/system-services/org.pleiades.Probe.service <<'EOF'
[D-BUS Service]
Name=org.pleiades.Probe
Exec=/usr/bin/sleep 600
User=root
EOF
cat > /etc/dbus-1/system.d/org.pleiades.Probe.conf <<'EOF'
<!DOCTYPE busconfig PUBLIC "-//freedesktop//DTD D-BUS Bus Configuration 1.0//EN" "http://www.freedesktop.org/standards/dbus/1.0/busconfig.dtd">
<busconfig>
  <policy user="root"><allow own="org.pleiades.Probe"/></policy>
  <policy context="default"><allow send_destination="org.pleiades.Probe"/></policy>
</busconfig>
EOF
busctl call org.freedesktop.DBus /org/freedesktop/DBus org.freedesktop.DBus ReloadConfig
(timeout 3 busctl call org.pleiades.Probe / org.freedesktop.DBus.Peer Ping >/dev/null 2>&1 &)`

// TestCLI_ServiceFactsHoldStillWhileLoginsComeAndGo gathers a Rocky Linux
// host's services twice. Before the first, sshd moves to its Accept=yes
// socket, a user lingers and a bus name runs as a transient unit; before the
// second, the user stops lingering and the bus name's process ends. The
// services named must be the same both times, getty@tty1 among them, and no
// key may carry a connection's peer address.
func TestCLI_ServiceFactsHoldStillWhileLoginsComeAndGo(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the services facts gate, which builds a Rocky Linux device under systemd, in short mode")
	}
	device := startTestdataDevice(t, "rocky-systemd-sshd", rockyGatePassword, systemdDevice)
	dir, home := device.manage(t, "rocky", "linux_server,rhel_family")
	sh := func(script string) string {
		t.Helper()
		out, code := device.exec(t, "sh", "-c", script)
		if code != 0 {
			t.Fatalf("%s: exit %d\n%s", script, code, out)
		}
		return out
	}
	sh("systemctl disable --now sshd.service && systemctl enable --now sshd.socket")
	sh("useradd -m gatherpeer && loginctl enable-linger gatherpeer")
	sh(busProbeSetup)
	// The premise, read beside the product: each unit the gate means to be
	// left out is loaded before the first gather.
	loaded := func() string {
		return sh("systemctl --type=service --all --no-legend --no-pager --plain list-units")
	}
	deadline := time.Now().Add(deviceSettleTimeout)
	for units := loaded(); !strings.Contains(units, "user@1000.service") || !strings.Contains(units, "dbus-:"); units = loaded() {
		if time.Now().After(deadline) {
			t.Fatalf("the lingering user's and the bus name's units never loaded:\n%s", units)
		}
		time.Sleep(time.Second)
	}

	gather := func() []string {
		t.Helper()
		rep, out, code := runPleiadesJSONWithHome(t, dir, home, "adhoc", "rocky", "facts.gather", "gather_subset:=[services]")
		if code != 0 || len(rep.Tasks) != 1 {
			t.Fatalf("facts.gather exited %d:\n%s", code, out)
		}
		var keys []string
		for key := range rep.Tasks[0].Stats {
			if strings.HasPrefix(key, "service.") {
				keys = append(keys, key)
			}
		}
		slices.Sort(keys)
		return keys
	}
	first := gather()

	sh("loginctl disable-linger gatherpeer && systemctl --type=service --no-legend --no-pager --plain list-units 'dbus-:*' | cut -d' ' -f1 | xargs -r systemctl stop")
	deadline = time.Now().Add(deviceSettleTimeout)
	for units := loaded(); strings.Contains(units, "user@1000.service") || strings.Contains(units, "dbus-:"); units = loaded() {
		if time.Now().After(deadline) {
			t.Fatalf("the lingering user's or the bus name's unit never went away:\n%s", units)
		}
		time.Sleep(time.Second)
	}
	second := gather()

	if !slices.Equal(first, second) {
		t.Errorf("the services changed on a host where only logins came and went:\n first: %v\nsecond: %v", first, second)
	}
	if !slices.Contains(first, "service.getty@tty1.service") || !slices.Contains(first, "service.sshd.service") {
		t.Errorf("a service that lasts as long as the host was left out: %v", first)
	}
	for _, key := range append(first, second...) {
		for _, passing := range []string{"sshd@", "user@", "user-runtime-dir@", "dbus-:"} {
			if strings.Contains(key, passing) {
				t.Errorf("%s lasts only as long as a login, a connection or a process", key)
			}
		}
	}
}
