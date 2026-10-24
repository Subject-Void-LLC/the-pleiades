// Tests of the GNS3 client against real gns3-server 3.0.6 and 2.2.61
// servers, each in a container.
package gns3_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/testsupport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gns3"
)

// TestClient_AgainstRealServers: on each server line the client finds the
// API, logs in where it must, and reads a project's VPCS nodes with their
// telnet consoles, started or not.
func TestClient_AgainstRealServers(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and starts GNS3 servers")
	}
	for _, tc := range []struct{ version, api string }{
		{testsupport.GNS3Version3, gns3.APIv3},
		{testsupport.GNS3Version2, gns3.APIv2},
	} {
		t.Run(tc.version, func(t *testing.T) {
			srv := testsupport.StartGNS3(t, tc.version)
			project := srv.NewProject(t, "lab-a")
			pc1 := srv.AddVPCS(t, project, "pc1")
			srv.AddVPCS(t, project, "pc2")
			srv.StartNodes(t, project)

			ctx := context.Background()
			c, err := gns3.New(srv.URL, nil, gns3.Credentials{Username: testsupport.GNS3AdminUser, Password: testsupport.GNS3AdminPassword})
			if err != nil {
				t.Fatal(err)
			}
			v, err := c.Connect(ctx)
			if err != nil || v.API != tc.api || v.Version != tc.version {
				t.Fatalf("Connect = %+v, %v; want %s %s", v, err, tc.api, tc.version)
			}
			projects, err := c.Projects(ctx)
			if err != nil || !slices.ContainsFunc(projects, func(p gns3.Project) bool { return p.ID == project && p.Name == "lab-a" }) {
				t.Fatalf("Projects = %+v, %v; want lab-a", projects, err)
			}
			nodes, err := c.Nodes(ctx, project)
			if err != nil || len(nodes) != 2 {
				t.Fatalf("Nodes = %+v, %v; want two", nodes, err)
			}
			i := slices.IndexFunc(nodes, func(n gns3.Node) bool { return n.Name == "pc1" })
			if i < 0 {
				t.Fatalf("no pc1 in %+v", nodes)
			}
			n := nodes[i]
			if n.NodeType != "vpcs" || n.ConsoleType != "telnet" || n.Console == nil || *n.Console != pc1 || n.ConsoleHost != "0.0.0.0" || n.Status != "started" {
				t.Errorf("pc1 = %+v (console %v), want a started VPCS with a telnet console at %d", n, n.Console, pc1)
			}
			if _, err := c.Nodes(ctx, "00000000-0000-0000-0000-000000000000"); err == nil {
				t.Error("an unknown project's nodes were listed")
			}

			if tc.api != gns3.APIv3 {
				return
			}
			// A 3.x server answers nothing without a login, and refuses a
			// wrong password.
			anon, _ := gns3.New(srv.URL, nil, gns3.Credentials{})
			if _, err := anon.Connect(ctx); !errors.Is(err, gns3.ErrNeedsLogin) {
				t.Errorf("Connect with no login = %v, want ErrNeedsLogin", err)
			}
			wrong, _ := gns3.New(srv.URL, nil, gns3.Credentials{Username: testsupport.GNS3AdminUser, Password: "not-the-password"})
			if _, err := wrong.Connect(ctx); err == nil || !strings.Contains(err.Error(), "refused the login") {
				t.Errorf("Connect with a wrong password = %v, want the login refused", err)
			}
		})
	}
}
