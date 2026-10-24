// Release gate for pleiades import-gns3: through the real binary, against
// real GNS3 servers of both API lines, a project's VPCS nodes become
// console devices that a run reaches over their telnet consoles.
package main_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
)

// TestCLI_ImportGNS3ReachesANodesConsole is the release gate.
func TestCLI_ImportGNS3ReachesANodesConsole(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and starts GNS3 servers")
	}
	for _, version := range []string{testsupport.GNS3Version3, testsupport.GNS3Version2} {
		t.Run(version, func(t *testing.T) {
			srv := testsupport.StartGNS3(t, version)
			project := srv.NewProject(t, "routing-lab")
			pc1 := srv.AddVPCS(t, project, "pc1")
			srv.AddVPCS(t, project, "pc2")
			srv.StartNodes(t, project)

			dir := t.TempDir()
			env := map[string]string{"PLEIADES_GNS3_PASSWORD": testsupport.GNS3AdminPassword}
			pleiades := func(args ...string) (string, error) { return runPleiadesWithEnv(t, dir, env, args...) }
			if out, err := pleiades("init"); err != nil {
				t.Fatalf("init: %v\n%s", err, out)
			}
			login := []string{"--username", testsupport.GNS3AdminUser}
			importArgs := append([]string{"import-gns3", "--server", srv.URL, "--project", "routing-lab", "--json"}, login...)

			out, err := pleiades(importArgs...)
			if err != nil {
				t.Fatalf("import-gns3: %v\n%s", err, out)
			}
			var res struct {
				API     string `json:"api"`
				Version string `json:"version"`
				Nodes   []struct {
					Device, Host, Status string
					Port                 int
				} `json:"nodes"`
			}
			if err := json.NewDecoder(strings.NewReader(out[strings.Index(out, "{"):])).Decode(&res); err != nil {
				t.Fatalf("the report is not JSON: %v\n%s", err, out)
			}
			if res.Version != version || len(res.Nodes) != 2 || res.Nodes[0].Device != "pc1" || res.Nodes[0].Host != "127.0.0.1" || res.Nodes[0].Port != pc1 || res.Nodes[0].Status != "started" {
				t.Fatalf("import = %+v, want pc1 and pc2 at 127.0.0.1, pc1 on %d", res, pc1)
			}

			// The imported node is reached at its console through a run.
			out, err = pleiades("adhoc", "pc1", "telnet_exec", "command=show ip", "insecure_telnet=true", "--json")
			var run struct {
				Tasks []struct {
					Status string         `json:"status"`
					Stats  map[string]any `json:"stats"`
				} `json:"tasks"`
			}
			if err != nil || json.NewDecoder(strings.NewReader(out[max(strings.Index(out, "{"), 0):])).Decode(&run) != nil || len(run.Tasks) != 1 {
				t.Fatalf("telnet_exec on the imported pc1: %v\n%s", err, out)
			}
			if stdout, _ := run.Tasks[0].Stats["stdout"].(string); !strings.Contains(stdout, "VPCS[1]") || !strings.Contains(stdout, "IP/MASK") {
				t.Fatalf("pc1's console answered %q, want VPCS's show ip", stdout)
			}

			// A node already there is refused, and replaced only when asked.
			if out, err := pleiades(importArgs...); err == nil || !strings.Contains(out, "already exists") {
				t.Errorf("a second import = %v\n%s; want refused without --replace", err, out)
			}
			if out, err := pleiades(append(importArgs, "--replace", "--nodes", "pc2")...); err != nil {
				t.Errorf("a replacing import: %v\n%s", err, out)
			}
			if out, err := pleiades(append([]string{"import-gns3", "--server", srv.URL, "--project", "no-such-lab"}, login...)...); err == nil || !strings.Contains(out, `no project "no-such-lab"`) {
				t.Errorf("a project the server lacks = %v\n%s", err, out)
			}
			if version != testsupport.GNS3Version3 {
				return
			}
			wrong := map[string]string{"PLEIADES_GNS3_PASSWORD": "not-the-password"}
			if out, err := runPleiadesWithEnv(t, dir, wrong, importArgs...); err == nil || !strings.Contains(out, "refused the login") {
				t.Errorf("a wrong password = %v\n%s; want the login refused", err, out)
			}
		})
	}
}
