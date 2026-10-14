// Tests for the k3s fixture itself: it starts, its node is Ready, and what
// it hands a test (the authority, the administrator's identity) is real.
package testsupport

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
)

// TestK3s_StartsAndHandsOverItsIdentity starts the newest pinned k3s and
// checks, through its own kubectl, that the node is Ready and that the
// kubeconfig the fixture rewrote points at the address it reports.
func TestK3s_StartsAndHandsOverItsIdentity(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real k3s server")
	}
	k := StartK3s(t, K3sOptions{})
	out, err := k.Kubectl(context.Background(), "get", "nodes", "-o", "jsonpath={.items[*].status.conditions[?(@.type=='Ready')].status}")
	if err != nil || strings.TrimSpace(out) != "True" {
		t.Fatalf("node Ready = %q, %v", out, err)
	}
	for name, v := range map[string]string{"CA": k.CAPEM, "client certificate": k.ClientCert, "client key": k.ClientKey} {
		if !strings.Contains(v, "-----BEGIN") {
			t.Errorf("%s is not PEM: %.40q", name, v)
		}
	}
	if !strings.Contains(k.Kubeconfig, k.Server) {
		t.Errorf("the kubeconfig does not name %s", k.Server)
	}
	if tok := k.ServiceAccountToken(t, "", "probe", "cluster-admin"); strings.Count(tok, ".") != 2 {
		t.Errorf("token %.20q... is not a JWT", tok)
	}
	// No probe entered the root cgroup before k3s left it, so it enabled
	// every controller there.
	logs, err := k.Container.Logs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(logs)
	_ = logs.Close()
	if strings.Contains(string(b), "failed to enable controller") {
		t.Errorf("k3s could not enable a cgroup controller:\n%s", k3sLastWords(string(b)))
	}
}

// TestK3sLastWords_PutsTheCgroupWarningBeforeTheTail checks that a failed
// start's message carries k3s's early warning that it could not enable a
// controller, which kubelet's last words never name as the cause, ahead of
// the last fifteen lines and only once.
func TestK3sLastWords_PutsTheCgroupWarningBeforeTheTail(t *testing.T) {
	warn := `level=warning msg="failed to enable controller \"cpu\"" error="write /sys/fs/cgroup/cgroup.subtree_control: device or resource busy"`
	lines := []string{"Starting", warn}
	for i := range 20 {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	got := strings.Split(k3sLastWords(strings.Join(lines, "\n")), "\n")
	if len(got) != 16 || got[0] != warn || got[1] != "line 5" || got[15] != "line 19" {
		t.Errorf("last words = %q, want the warning then lines 5 to 19", got)
	}

	short := "Starting\n" + warn + "\nFailed to start ContainerManager"
	if got := k3sLastWords(short + "\n"); got != short {
		t.Errorf("a short log = %q, want it whole with the warning once", got)
	}
	if got := k3sLastWords(""); got != "" {
		t.Errorf("no log = %q", got)
	}
}
