// Package main: no command that adds a host writes one named "", "." or
// "..". add-host and import-kubeconfig refuse the name they were given, with
// recordname's reason. The three lab imports (CML, GNS3, containerlab)
// already refused all three before this rule, through their own node-name
// grammar, and these tests pin that.
package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml/cmltest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
)

// unaddressableNames are the names no URL can carry.
var unaddressableNames = []string{"", ".", ".."}

// writtenHostNames reads the project's inventory and returns its host names.
func writtenHostNames(t *testing.T, dir string) map[string]bool {
	t.Helper()
	hosts, err := inventory.ReadHosts(filepath.Join(dir, inventory.DefaultInventoryFilename))
	if err != nil {
		t.Fatalf("ReadHosts: %v", err)
	}
	names := map[string]bool{}
	for _, h := range hosts {
		names[h.Name] = true
	}
	return names
}

// TestAddHost_RefusesANameNoURLCarries: each of the three is refused with
// the reason and nothing is written; three dots, a near miss, is added.
func TestAddHost_RefusesANameNoURLCarries(t *testing.T) {
	dir := t.TempDir()
	if err := runInit([]string{"--dir", dir}); err != nil {
		t.Fatalf("init: %v", err)
	}
	for _, name := range unaddressableNames {
		err := runAddHost([]string{name, "--dir", dir, "--type", "linux_server", "--set", "host=10.0.0.1"})
		if err == nil || !strings.Contains(err.Error(), "host not added") || !strings.Contains(err.Error(), "name") {
			t.Errorf("add-host %q = %v, want a refusal saying why", name, err)
		}
	}
	if names := writtenHostNames(t, dir); len(names) != 0 {
		t.Errorf("refused add-hosts wrote %v", names)
	}
	if err := runAddHost([]string{"...", "--dir", dir, "--type", "linux_server", "--set", "host=10.0.0.1"}); err != nil {
		t.Fatalf("add-host \"...\" = %v, want nil", err)
	}
}

// TestImportKubeconfig_RefusesANameNoURLCarries imports a valid kubeconfig
// under each name. The refusal comes before the kubeconfig is read and so
// before the cluster's credential is stored, which the import does before it
// writes the host: refused only by the hosts.yaml backstop, it left a
// credential for a device that was never written.
func TestImportKubeconfig_RefusesANameNoURLCarries(t *testing.T) {
	dir := t.TempDir()
	if err := runInit([]string{"--dir", dir}); err != nil {
		t.Fatalf("init: %v", err)
	}
	path := filepath.Join(t.TempDir(), "config")
	kubeconfig := kubeconfigText("https://k.example:6443", clusterCAPEM(t), "", "    token: imported-token-value\n")
	if err := os.WriteFile(path, []byte(kubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	before := readOrEmpty(t, credentialsPath(dir))
	for _, name := range []string{".", ".."} {
		err := runImportKubeconfig([]string{name, "--dir", dir, "--kubeconfig", path})
		if err == nil || !strings.Contains(err.Error(), "cluster not imported") || !strings.Contains(err.Error(), "cannot be used") {
			t.Errorf("import-kubeconfig %q = %v, want the name refused before the kubeconfig is read", name, err)
		}
	}
	// What init leaves in the credential file is the baseline, so a stored
	// credential would show as a change to it.
	if after := readOrEmpty(t, credentialsPath(dir)); after != before {
		t.Errorf("a refused import changed the credential file:\n%s", after)
	}
}

// credentialsPath is where a project's file credential store lives.
func credentialsPath(dir string) string {
	return filepath.Join(dir, ".pleiades", "credentials.yaml")
}

// readOrEmpty is path's content, or "" when it does not exist.
func readOrEmpty(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- a path under the test's own temporary directory
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

// TestImportCML_RefusesANodeNoURLCarries imports a lab holding R1 and a node
// labelled "..". The import is refused before anything is written, by the
// node-name grammar cmlSelect already held every importable node to.
func TestImportCML_RefusesANodeNoURLCarries(t *testing.T) {
	ctl := cmltest.New(t, "admin", "pw")
	client, err := cml.New(ctl.URL, ctl.Client(), cml.Credentials{Username: "admin", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := client.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	lab, err := client.CreateLab(ctx, "cvd")
	if err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"R1", ".."} {
		if _, err := client.CreateNode(ctx, lab.ID, cml.NodeSpec{Label: label, Definition: "iol-xe"}); err != nil {
			t.Fatalf("creating node %q: %v", label, err)
		}
	}

	dir := t.TempDir()
	pin := devicetls.Fingerprint(ctl.Certificate())
	for _, step := range [][]string{
		{"init", "--dir", dir},
		{"add-host", "cml", "--dir", dir, "--type", "cml_controller", "--set", "api_base=" + ctl.URL, "--set", "tls_fingerprint=" + pin},
		{"add-credential", "cml", "--dir", dir, "--username", "admin", "--password", "pw"},
	} {
		run := map[string]func([]string) error{"init": runInit, "add-host": runAddHost, "add-credential": runAddCredential}[step[0]]
		if err := run(step[1:]); err != nil {
			t.Fatalf("%s: %v", strings.Join(step, " "), err)
		}
	}
	err = runImportCML([]string{"--dir", dir, "--controller", "cml", "--lab", "cvd", "--address", "R1=10.10.20.171", "--via", "telnet"})
	if err == nil || !strings.Contains(err.Error(), "not a name a device can take") {
		t.Fatalf("import-cml = %v, want the node \"..\" refused", err)
	}
	if names := writtenHostNames(t, dir); names["R1"] || names[".."] {
		t.Errorf("a refused import wrote %v", names)
	}
}

// TestImportGNS3_RefusesANodeNoURLCarries imports a project holding pc1 and
// a node named "." from a stub server. gns3Select refuses the import before
// anything is written.
func TestImportGNS3_RefusesANodeNoURLCarries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/version":
			_, _ = w.Write([]byte(`{"version":"2.2.61"}`))
		case "/v2/projects":
			_, _ = w.Write([]byte(`[{"project_id":"p","name":"lab","status":"opened"}]`))
		case "/v2/projects/p/nodes":
			_, _ = w.Write([]byte(`[{"node_id":"n1","name":"pc1","node_type":"vpcs","console":5000,"console_type":"telnet","console_host":"127.0.0.1","status":"started"},` +
				`{"node_id":"n2","name":".","node_type":"vpcs","console":5001,"console_type":"telnet","console_host":"127.0.0.1","status":"started"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	if err := runInit([]string{"--dir", dir}); err != nil {
		t.Fatalf("init: %v", err)
	}
	err := runImportGNS3([]string{"--dir", dir, "--server", srv.URL, "--project", "lab"})
	if err == nil || !strings.Contains(err.Error(), "not a name a device can take") {
		t.Fatalf("import-gns3 = %v, want the node \".\" refused", err)
	}
	if names := writtenHostNames(t, dir); names["pc1"] || names["."] {
		t.Errorf("a refused import wrote %v", names)
	}
}

// TestClabSelect_RefusesANameNoURLCarries pins what containerlab's import
// already did before this rule: a node name outside its grammar, which none
// of the three fits, refuses the import before anything is written.
func TestClabSelect_RefusesANameNoURLCarries(t *testing.T) {
	for _, name := range unaddressableNames {
		topo := clabTopology{Name: "lab", Nodes: map[string]clabNode{
			"r1": {Labels: map[string]string{"containerlab": "lab"}},
			name: {Labels: map[string]string{"containerlab": "lab"}},
		}}
		if _, err := clabSelect(topo, "lab", ""); err == nil || !strings.Contains(err.Error(), "not a name a device can take") {
			t.Errorf("clabSelect with a node named %q = %v, want it refused", name, err)
		}
	}
}
