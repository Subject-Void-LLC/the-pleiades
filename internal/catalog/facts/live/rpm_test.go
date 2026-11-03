// The packages subset's rpm probe against a real RHEL-family database: the
// probe's own command run in a Rocky Linux container and its output read by
// the real parser (owner, 2026-11-03: containers where one can stand in for
// a lab machine). The SSH path around a probe is proven by the release gate.
package live

import (
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/facts"
)

// rockyImage is a real RHEL-family system with an rpm database.
const rockyImage = "rockylinux/rockylinux:9.6-minimal"

func TestGatherPackages_ReadsARealRPMDatabase(t *testing.T) {
	c := start(t, rockyImage)
	probes := facts.Probes("Linux", "packages", "rpm -qa")
	if len(probes) != 1 {
		t.Fatalf("%d rpm probes", len(probes))
	}
	got := probes[0].Read(run(t, c, probes[0].Command))
	want, err := strconv.Atoi(strings.TrimSpace(run(t, c, "rpm -qa | grep -vc gpg-pubkey")))
	if err != nil || len(got) != want || want == 0 {
		t.Fatalf("%d package facts; rpm lists %d (%v)", len(got), want, err)
	}
	version := strings.TrimSpace(run(t, c, "rpm -q --qf '%{VERSION}-%{RELEASE}' bash"))
	arch := strings.TrimSpace(run(t, c, "rpm -q --qf '%{ARCH}' bash"))
	if b, _ := got["package.bash"].(map[string]any); b["version"] != version || b["arch"] != arch || b["source"] != "rpm" {
		t.Errorf("package.bash = %v; rpm says %s %s", b, version, arch)
	}
	// The dpkg and apk probes print nothing here, being guarded.
	for _, p := range facts.Probes("Linux", "packages", "") {
		if p.Command == probes[0].Command {
			continue
		}
		if out, _ := runAs(t, c, "", p.Command); strings.TrimSpace(out) != "" {
			t.Errorf("%q printed something on a system without its tool", p.Command)
		}
	}
}
