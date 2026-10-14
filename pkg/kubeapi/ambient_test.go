// Tests that nothing ambient reaches a cluster: an in-cluster Runner's
// environment and a KUBECONFIG are ignored.
package kubeapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// fakeItem is a device with a name and nothing else: every other method is
// the nil embedded interface's, so a call the code under test should not
// make panics instead of answering.
type fakeItem struct {
	inventory.InventoryItem
	name string
}

func (f fakeItem) Name() string { return f.name }

// clusterDevice is the least a session needs from a device: its name, and
// the cluster accessors.
type clusterDevice struct {
	fakeItem
	server string
}

func (c clusterDevice) KubeAPIServer() string { return c.server }
func (c clusterDevice) KubeNamespace() string { return "default" }

// TestForDevice_IgnoresEverythingAmbient sets what an in-cluster Runner and
// a developer's shell would offer (the in-cluster service address, and a
// KUBECONFIG naming a working kubeconfig with a token) and proves a device
// with no stored credential is still refused: neither is ever read.
func TestForDevice_IgnoresEverythingAmbient(t *testing.T) {
	kubeconfig := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(kubeconfig, []byte(kubeconfigWithToken("ambient-token")), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	t.Setenv("KUBERNETES_SERVICE_PORT", "443")
	t.Setenv("KUBECONFIG", kubeconfig)

	_, err := ForDevice(clusterDevice{fakeItem: fakeItem{name: "cluster1"}, server: "https://10.0.0.1:6443"}, map[string]string{}, "")
	if err == nil || !strings.Contains(err.Error(), "no credential") {
		t.Fatalf("ForDevice with no stored credential = %v, want a refusal: nothing ambient may stand in", err)
	}
}

// kubeconfigWithToken is a working-looking kubeconfig holding token.
func kubeconfigWithToken(token string) string {
	return kubeconfig("", "    token: "+token+"\n")
}
