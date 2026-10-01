// Tests for the SSH and NETCONF probes: the SSH probe's parser on known
// outputs and arbitrary ones, and both probes against a real SSH server.
package onboard

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/netconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec/remoteexectest"
)

// TestParseSSHProbe maps known probe outputs to what each proves, and
// each capability needs every fact behind it.
func TestParseSSHProbe(t *testing.T) {
	const debian = "pleiades-probe=1\nkernel=Linux\nkernel_release=6.1.0\nos_id=debian\nhas=apt-get\nhas=dpkg\nhas=systemctl\nhas=useradd\nhas=usermod\nhas=userdel\nhas=groupadd\nhas=groupmod\nhas=groupdel\nhas=getent\nsystemd_running=1\n"
	for _, tc := range []struct {
		name string
		out  string
		want []capability.Name
	}{
		{"debian", debian, []capability.Name{capability.NameShellExec, capability.NameLinux, capability.NamePOSIXFileSystem, capability.NameFactGatherer, capability.NameSystemd, capability.NameApt, capability.NamePosixAccount}},
		{"rhel with firewalld", "pleiades-probe=1\nkernel=Linux\nos_id=\"rhel\"\nhas=dnf\nhas=rpm\nhas=systemctl\nhas=firewall-cmd\nsystemd_running=1\n", []capability.Name{capability.NameShellExec, capability.NameLinux, capability.NamePOSIXFileSystem, capability.NameFactGatherer, capability.NameSystemd, capability.NameFirewalld, capability.NameDnf}},
		{"alpine busybox", "pleiades-probe=1\nkernel=Linux\nos_id=alpine\nhas=getent\n", []capability.Name{capability.NameShellExec, capability.NameLinux, capability.NamePOSIXFileSystem, capability.NameFactGatherer}},
		{"systemctl without systemd running", "pleiades-probe=1\nkernel=Linux\nhas=systemctl\nhas=firewall-cmd\n", []capability.Name{capability.NameShellExec, capability.NameLinux, capability.NamePOSIXFileSystem, capability.NameFactGatherer}},
		{"apt without dpkg", "pleiades-probe=1\nkernel=Linux\nhas=apt-get\n", []capability.Name{capability.NameShellExec, capability.NameLinux, capability.NamePOSIXFileSystem, capability.NameFactGatherer}},
		{"freebsd: files, and no Linux package manager", "pleiades-probe=1\nkernel=FreeBSD\nhas=apt-get\nhas=dpkg\nhas=systemctl\nsystemd_running=1\n", []capability.Name{capability.NameShellExec, capability.NamePOSIXFileSystem}},
		{"a kernel nothing was proven on", "pleiades-probe=1\nkernel=Darwin\nhas=apt-get\nhas=dpkg\n", []capability.Name{capability.NameShellExec}},
		{"a router's command line", "% Invalid input detected at '^' marker.\n", nil},
		{"a banner before the marker", "kernel=Linux\nhas=apt-get\nhas=dpkg\npleiades-probe=1\nkernel=OpenBSD\n", []capability.Name{capability.NameShellExec}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parseSSHProbe(tc.out)
			if !slices.Equal(sorted(got.Capabilities), sorted(tc.want)) {
				t.Errorf("capabilities %v, want %v", got.Capabilities, tc.want)
			}
		})
	}
	if got := parseSSHProbe(debian).Facts; got["os_id"] != "debian" || got["kernel_release"] != "6.1.0" {
		t.Errorf("facts %v", got)
	}
}

// FuzzParseSSHProbe: whatever a device prints, the parser does not panic,
// grants nothing a generic_ssh device may not hold, and keeps every fact
// within bounds.
func FuzzParseSSHProbe(f *testing.F) {
	f.Add("pleiades-probe=1\nkernel=Linux\nhas=apt-get\nhas=dpkg\nsystemd_running=1\nhas=systemctl\n")
	f.Add("pleiades-probe=1\nos_id=\x1b]0;x\x07\nkernel=Linux\n")
	f.Add("")
	allowed := generic.Discoverable(generic.TypeSSH)
	f.Fuzz(func(t *testing.T, out string) {
		got := parseSSHProbe(out)
		for _, c := range got.Capabilities {
			if !slices.Contains(allowed, c) {
				t.Fatalf("granted %s", c)
			}
		}
		for k, v := range got.Facts {
			s, ok := v.(string)
			if !ok || len(s) > maxFactText || strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
				t.Fatalf("fact %s = %q is not bounded text", k, v)
			}
		}
	})
}

// sshServer starts a real in-process SSH server, trusted through a
// known_hosts file the probe reads, as every real connection here is.
func sshServer(t *testing.T, opts remoteexectest.Options) *remoteexectest.Server {
	t.Helper()
	srv, err := remoteexectest.Start(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	path := filepath.Join(t.TempDir(), "known_hosts")
	line := knownhosts.Line([]string{net.JoinHostPort(srv.Host, strconv.Itoa(srv.Port))}, srv.HostKey)
	if err := os.WriteFile(path, []byte(line+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(remoteexec.KnownHostsEnv, path)
	return srv
}

// build builds a device of deviceType from props.
func build(t *testing.T, deviceType string, props map[string]inventory.PropertyValue) inventory.InventoryItem {
	t.Helper()
	ctor, _ := record.LookupType(deviceType)
	item, err := ctor(record.Record{ID: "d1", Name: "d1", Type: deviceType, Properties: props})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

// TestSSHProbe_RealShell runs the probe script through a real SSH login
// into this machine's own shell. What it finds depends on the machine,
// so the test asserts what every Linux machine proves (a POSIX shell and
// the Linux kernel) and that nothing beyond generic_ssh's set is granted.
func TestSSHProbe_RealShell(t *testing.T) {
	// The harness runs the probe's script on this machine, so the Linux
	// grants asserted below are this machine's kernel's answer.
	testsupport.Require(t, "linux", runtime.GOOS == "linux",
		"the probe's Linux grants are asserted against this machine's own kernel, and this one is "+runtime.GOOS)
	srv := sshServer(t, remoteexectest.Options{})
	dev := build(t, generic.TypeSSH, map[string]inventory.PropertyValue{"host": srv.Host, "port": srv.Port})
	got, err := sshProber{}.Probe(context.Background(), dev, srv.Secrets())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []capability.Name{capability.NameShellExec, capability.NameLinux} {
		if !slices.Contains(got.Capabilities, want) {
			t.Errorf("capabilities %v lack %s", got.Capabilities, want)
		}
	}
	allowed := generic.Discoverable(generic.TypeSSH)
	for _, c := range got.Capabilities {
		if !slices.Contains(allowed, c) {
			t.Errorf("granted %s", c)
		}
	}
	if got.Facts["kernel"] != "Linux" {
		t.Errorf("facts %v", got.Facts)
	}
	if cmds := srv.Commands(); len(cmds) != 1 || cmds[0] != sshProbeScript {
		t.Errorf("the device received %q, want the one constant script", cmds)
	}
}

// TestSSHProbe_WrongCredentialProvesNothing: a refused login is an error.
func TestSSHProbe_WrongCredentialProvesNothing(t *testing.T) {
	srv := sshServer(t, remoteexectest.Options{})
	dev := build(t, generic.TypeSSH, map[string]inventory.PropertyValue{"host": srv.Host, "port": srv.Port})
	if _, err := (sshProber{}).Probe(context.Background(), dev, map[string]string{"username": srv.Username, "password": "wrong"}); err == nil {
		t.Fatal("a refused login proved something")
	}
}

// TestNetconfProbe_ReadsTheHello opens a real NETCONF session and records
// the capabilities the server's hello announced.
func TestNetconfProbe_ReadsTheHello(t *testing.T) {
	srv := sshServer(t, remoteexectest.Options{Netconf: &remoteexectest.NetconfDevice{Capabilities: []string{netconf.CapabilityCandidate}}})
	dev := build(t, generic.TypeNetconf, map[string]inventory.PropertyValue{"host": srv.Host, "netconf_port": srv.Port})
	got, err := netconfProber{}.Probe(context.Background(), dev, srv.Secrets())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Capabilities, []capability.Name{capability.NameNetconf}) {
		t.Errorf("capabilities %v", got.Capabilities)
	}
	urns, _ := got.Facts["netconf_capabilities"].([]any)
	if !slices.Contains(urns, any(netconf.CapabilityCandidate)) || !slices.Contains(urns, any(netconf.CapabilityBase10)) {
		t.Errorf("recorded capabilities %v", urns)
	}
}

// TestNetconfProbe_NoSubsystemProvesNothing: an SSH server that declines
// the netconf subsystem is not a NETCONF device.
func TestNetconfProbe_NoSubsystemProvesNothing(t *testing.T) {
	srv := sshServer(t, remoteexectest.Options{})
	dev := build(t, generic.TypeNetconf, map[string]inventory.PropertyValue{"host": srv.Host, "netconf_port": srv.Port})
	if _, err := (netconfProber{}).Probe(context.Background(), dev, srv.Secrets()); err == nil {
		t.Fatal("a server without NETCONF proved NetconfCapable")
	}
}
