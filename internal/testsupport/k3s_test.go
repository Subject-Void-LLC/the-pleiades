// Tests for the k3s fixture itself: it starts, its node is Ready, and what
// it hands a test (the authority, the administrator's identity) is real.
package testsupport

import (
	"context"
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
}
