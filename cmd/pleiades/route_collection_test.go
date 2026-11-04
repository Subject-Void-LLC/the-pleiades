// A Collection method's own SSH connection (sdk.Connect) reaches a device
// through its route of hops, as ssh_exec does, never directly.
package main_test

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go/network"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// TestCLI_ACollectionMethodReachesARoutedDeviceThroughItsBastion: a device
// known only by its container name on a Docker network this machine is not
// on, behind an sshd bastion on that network. With no route, exec.command
// (built on sdk.Connect) cannot reach it; with the route, it runs there,
// the target's host key verified under the name the bastion dials, and
// refused when known_hosts does not name it.
func TestCLI_ACollectionMethodReachesARoutedDeviceThroughItsBastion(t *testing.T) {
	if testing.Short() {
		t.Skip("starts two real sshd containers")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	nw, err := network.New(ctx)
	if err != nil {
		t.Fatalf("creating a network: %v", err)
	}
	t.Cleanup(func() { _ = nw.Remove(context.Background()) })

	bastion, err := testsupport.StartSSHDOn(ctx, nw.Name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bastion.Terminate(context.Background()) })
	target, err := testsupport.StartSSHDOn(ctx, nw.Name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Terminate(context.Background()) })
	rawName, err := target.Container.Name(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Docker's network DNS answers the container's name on the network; this
	// machine cannot resolve it.
	name := strings.TrimPrefix(rawName, "/")
	hostname, err := target.RootExec(ctx, "hostname")
	if err != nil {
		t.Fatal(err)
	}
	hostname = strings.TrimSpace(hostname)

	knownHosts := routedKnownHosts(t, ctx, bastion, target, name)
	env := map[string]string{"PLEIADES_KNOWN_HOSTS": knownHosts}

	dir := t.TempDir()
	if out, err := runPleiades(t, dir, "init"); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "add-host", "bastion", "--type", "linux_server", "--set", "host="+bastion.Host, "--set", "port="+strconv.Itoa(bastion.Port)); err != nil {
		t.Fatalf("add-host bastion: %v\n%s", err, out)
	}
	if out, err := runPleiades(t, dir, "add-host", "inner", "--type", "linux_server", "--set", "host="+name, "--set", "port="+strconv.Itoa(testsupport.SSHDInnerPort)); err != nil {
		t.Fatalf("add-host inner: %v\n%s", err, out)
	}
	for _, device := range []string{"bastion", "inner"} {
		if out, err := runPleiadesWithStdin(t, dir, testsupport.SSHDPassword, "add-credential", device, "--username", testsupport.SSHDUser, "--password-stdin"); err != nil {
			t.Fatalf("add-credential %s: %v\n%s", device, err, out)
		}
	}

	adhoc := func() (string, error) {
		return runPleiadesWithEnv(t, dir, env, "adhoc", "inner", "exec.command", "cmd=hostname", "--json")
	}
	if out, err := adhoc(); err == nil {
		t.Fatalf("exec.command reached a device this machine cannot resolve, with no route:\n%s", out)
	}

	// The route, as hosts.yaml carries one.
	hostsPath := filepath.Join(dir, inventory.DefaultInventoryFilename)
	hosts, err := inventory.ReadHosts(hostsPath)
	if err != nil {
		t.Fatal(err)
	}
	for i := range hosts {
		if hosts[i].Name == "inner" {
			hosts[i].Properties["route"] = []any{"bastion"}
		}
	}
	if err := inventory.WriteHosts(hostsPath, hosts); err != nil {
		t.Fatal(err)
	}
	// The target's host key is checked through the route as a direct
	// connection's is: a known_hosts naming only the bastion refuses it.
	bastionOnly, err := bastion.KnownHosts(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if out, err := runPleiadesWithEnv(t, dir, map[string]string{"PLEIADES_KNOWN_HOSTS": bastionOnly}, "adhoc", "inner", "exec.command", "cmd=hostname", "--json"); err == nil || !strings.Contains(out, "key is unknown") {
		t.Fatalf("exec.command through the bastion to a target whose host key is unknown: %v\n%s; want a host key refusal", err, out)
	}
	out, err := adhoc()
	if err != nil {
		t.Fatalf("exec.command through the bastion: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"stdout": "`+hostname+`"`) {
		t.Fatalf("exec.command through the bastion did not run on the target %q:\n%s", hostname, out)
	}
}

// routedKnownHosts writes one known_hosts naming the bastion's keys under
// the addresses this machine dials it by, and the target's keys under name
// on the inner port, the address the bastion dials for the route.
func routedKnownHosts(t *testing.T, ctx context.Context, bastion, target *testsupport.SSHD, name string) string {
	t.Helper()
	bastionFile, err := bastion.KnownHosts(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	targetFile, err := target.KnownHosts(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bastionLines, err := os.ReadFile(bastionFile) // #nosec G304 -- the test's own temporary file
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	out.Write(bastionLines)
	f, err := os.Open(targetFile) // #nosec G304 -- the test's own temporary file
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	scanner := bufio.NewScanner(f)
	inner := "[127.0.0.1]:" + strconv.Itoa(testsupport.SSHDInnerPort)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		// Each of the target's keys, as named on its inner port, again under
		// the name the bastion dials.
		if len(fields) == 3 && fields[0] == inner {
			out.WriteString("[" + name + "]:" + strconv.Itoa(testsupport.SSHDInnerPort) + " " + fields[1] + " " + fields[2] + "\n")
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(path, []byte(out.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
