// The packages subset's rpm probe against a real RHEL-family database: the
// probe's own command run in a Rocky Linux container and its output read by
// the real parser (owner, 2026-11-03: containers where one can stand in for
// a lab machine). The SSH path around a probe is proven by the release gate.
package facts

import (
	"context"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
)

// rockyImage is a real RHEL-family system with an rpm database.
const rockyImage = "rockylinux/rockylinux:9.6-minimal"

// containerRun runs command through sh in c and returns its standard output.
func containerRun(t *testing.T, c testcontainers.Container, command string) string {
	t.Helper()
	code, r, err := c.Exec(context.Background(), []string{"sh", "-c", command}, tcexec.Multiplexed())
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("%s exited %d: %s", command, code, out)
	}
	return string(out)
}

func TestGatherPackages_ReadsARealRPMDatabase(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a Rocky Linux container")
	}
	c, err := testcontainers.GenericContainer(context.Background(), testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{Image: rockyImage, Cmd: []string{"sleep", "infinity"}},
		Started:          true,
	})
	if err != nil {
		_ = testcontainers.TerminateContainer(c)
		t.Fatalf("starting %s: %v", rockyImage, err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })

	var rpmProbe gatherProbe
	for _, p := range linuxPackageProbes() {
		if strings.Contains(p.command, "rpm -qa") {
			rpmProbe = p
		}
	}
	got := map[string]map[string]any{}
	for _, f := range rpmProbe.parse(containerRun(t, c, rpmProbe.command)) {
		got[f.key] = f.value.(map[string]any)
	}
	want, err := strconv.Atoi(strings.TrimSpace(containerRun(t, c, "rpm -qa | grep -vc gpg-pubkey")))
	if err != nil || len(got) != want || want == 0 {
		t.Fatalf("%d package facts; rpm lists %d (%v)", len(got), want, err)
	}
	version := strings.TrimSpace(containerRun(t, c, "rpm -q --qf '%{VERSION}-%{RELEASE}' bash"))
	arch := strings.TrimSpace(containerRun(t, c, "rpm -q --qf '%{ARCH}' bash"))
	if b := got["package.bash"]; b["version"] != version || b["arch"] != arch || b["source"] != "rpm" {
		t.Errorf("package.bash = %v; rpm says %s %s", b, version, arch)
	}
	// The dpkg and apk probes print nothing here, being guarded.
	for _, p := range linuxPackageProbes() {
		if p.command != rpmProbe.command && strings.TrimSpace(containerRunAllowFail(t, c, p.command)) != "" {
			t.Errorf("%q printed something on a system without its tool", p.command)
		}
	}
}

// containerRunAllowFail is containerRun for a command whose exit status is
// not the point (a guarded probe exits non-zero where its tool is absent).
func containerRunAllowFail(t *testing.T, c testcontainers.Container, command string) string {
	t.Helper()
	_, r, err := c.Exec(context.Background(), []string{"sh", "-c", command}, tcexec.Multiplexed())
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(r)
	return string(out)
}
