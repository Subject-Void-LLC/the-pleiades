// pleiades import-gns3: a GNS3 project's nodes, made console_device devices
// reached at their telnet consoles, read from the server's own API.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gns3"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// gns3DeviceType is the type a GNS3 node becomes: a device reached at its
// console, which is what GNS3 knows how to reach.
const gns3DeviceType = "console_device"

// gns3PasswordEnv names the environment variable a password is read from
// when --password-stdin is not given.
const gns3PasswordEnv = "PLEIADES_GNS3_PASSWORD" // #nosec G101 -- the name of an environment variable, not a credential

// gns3Imported is one node as the import reports it.
type gns3Imported struct {
	Device   string `json:"device"`
	NodeType string `json:"node_type"`
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	Status   string `json:"status"`
	// Skipped says why a node was not imported.
	Skipped string `json:"skipped,omitempty"`
}

// gns3Result is what the command reports.
type gns3Result struct {
	Server   string         `json:"server"`
	API      string         `json:"api"`
	Version  string         `json:"version"`
	Project  string         `json:"project"`
	Nodes    []gns3Imported `json:"nodes"`
	Warnings []string       `json:"warnings,omitempty"`
}

// runImportGNS3 imports a GNS3 project's nodes. Only nodes with a telnet
// console are imported, each as a console_device at that console; every
// other node is reported with its console type and left out.
func runImportGNS3(args []string) error {
	fs := flag.NewFlagSet("import-gns3", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	server := fs.String("server", "", "the GNS3 server's base URL, e.g. http://gns3.lab:3080")
	project := fs.String("project", "", "the project to import, by name or id")
	only := fs.String("nodes", "", "comma-separated nodes to import; every node when empty")
	username := fs.String("username", "", "the account to log in as (a 3.x server needs one; a 2.2 server takes it as basic auth)")
	passwordStdin := fs.Bool("password-stdin", false, "read the password from the first line of standard input, instead of "+gns3PasswordEnv)
	allowPlaintext := fs.Bool("allow-plaintext", false, "send the password over plain HTTP to a server that is not this machine")
	replace := fs.Bool("replace", false, "replace the console address of a node already in the inventory as a console_device")
	asJSON := fs.Bool("json", false, "print the result as one JSON document")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New("usage: pleiades import-gns3 --server <url> --project <name|id> [--nodes a,b] [--username <user> [--password-stdin]] [--allow-plaintext] [--replace] [--json]")
	}
	if *server == "" || *project == "" {
		return errors.New("--server and --project are both required: the import reads the project it names on the server it names")
	}
	password, err := labPassword(*passwordStdin, gns3PasswordEnv, os.Stdin)
	if err != nil {
		return err
	}
	if *username == "" && password != "" {
		return errors.New("a password was given with no --username")
	}
	base, err := url.Parse(strings.TrimRight(*server, "/"))
	if err != nil || base.Host == "" {
		return fmt.Errorf("--server %q is not a URL", *server)
	}
	warning, err := labPlaintext(base, *username != "", *allowPlaintext)
	if err != nil {
		return err
	}
	client, err := gns3.New(*server, labHTTPClient(nil), gns3.Credentials{Username: *username, Password: password})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	version, err := client.Connect(ctx)
	if err != nil {
		return err
	}
	proj, err := gns3Pick(ctx, client, *project)
	if err != nil {
		return err
	}
	nodes, err := client.Nodes(ctx, proj.ID)
	if err != nil {
		return err
	}
	picked, err := gns3Select(nodes, proj.Name, *only)
	if err != nil {
		return err
	}

	invPath := filepath.Join(*dir, inventory.DefaultInventoryFilename)
	hosts, err := inventory.ReadHosts(invPath)
	if err != nil {
		return fmt.Errorf("failed to read inventory (did you run 'pleiades init'?): %w", err)
	}
	res := gns3Result{Server: base.Redacted(), API: version.API, Version: version.Version, Project: proj.Name}
	if warning != "" {
		res.Warnings = append(res.Warnings, warning)
	}
	tags := []string{"gns3"}
	if clabNodeName.MatchString(proj.Name) {
		tags = append(tags, "gns3-"+proj.Name)
	}
	for _, n := range picked {
		out := gns3Imported{Device: n.Name, NodeType: n.NodeType, Status: n.Status}
		host, port, skip := gns3Console(n, base.Hostname())
		if skip != "" {
			out.Skipped = skip
			res.Nodes = append(res.Nodes, out)
			continue
		}
		spec := inventory.HostSpec{ID: uuid.New().String(), Name: n.Name, Type: gns3DeviceType, Tags: tags,
			Properties: map[string]pkginventory.PropertyValue{"telnet_host": host, "telnet_port": port}}
		if hosts, err = labPlace(hosts, spec, *replace); err != nil {
			return err
		}
		out.Host, out.Port = host, port
		res.Nodes = append(res.Nodes, out)
	}
	if err := inventory.WriteHosts(invPath, hosts); err != nil {
		return err
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	for _, w := range res.Warnings {
		fmt.Fprintln(os.Stderr, w)
	}
	fmt.Printf("GNS3 %s (API %s) at %s, project %q\n", res.Version, res.API, res.Server, res.Project)
	for _, n := range res.Nodes {
		// Every word from the server is quoted: a control character in a
		// name or a type is shown, not sent to the terminal.
		if n.Skipped != "" {
			fmt.Printf("skipped %q (%q): %s\n", n.Device, n.NodeType, n.Skipped)
			continue
		}
		fmt.Printf("imported %s (%q, %s) at telnet %s:%d\n", n.Device, n.NodeType, n.Status, n.Host, n.Port)
	}
	fmt.Println("each is reached with telnet_exec and insecure_telnet: true; Telnet is unencrypted, so keep it on the lab's own network")
	return nil
}

// gns3Pick finds the project named by id exactly, or else by name, which
// must name one project only.
func gns3Pick(ctx context.Context, c *gns3.Client, want string) (gns3.Project, error) {
	projects, err := c.Projects(ctx)
	if err != nil {
		return gns3.Project{}, err
	}
	var byName []gns3.Project
	for _, p := range projects {
		if p.ID == want {
			return p, nil
		}
		if p.Name == want {
			byName = append(byName, p)
		}
	}
	switch len(byName) {
	case 0:
		return gns3.Project{}, fmt.Errorf("the server has no project %q", want)
	case 1:
		return byName[0], nil
	}
	return gns3.Project{}, fmt.Errorf("%d projects are named %q on this server; give the one to import by its id", len(byName), want)
}

// gns3Select returns the nodes to import, sorted by name: those only
// names, or every node. A name a device cannot take, or one two nodes
// share, is refused.
func gns3Select(nodes []gns3.Node, project, only string) ([]gns3.Node, error) {
	byName := map[string]gns3.Node{}
	for _, n := range nodes {
		if !clabNodeName.MatchString(n.Name) {
			return nil, fmt.Errorf("node %q of project %q is not a name a device can take", n.Name, project)
		}
		if _, dup := byName[n.Name]; dup {
			return nil, fmt.Errorf("two nodes of project %q are named %q", project, n.Name)
		}
		byName[n.Name] = n
	}
	var picked []gns3.Node
	if only == "" {
		picked = append(picked, nodes...)
	} else {
		for _, name := range strings.Split(only, ",") {
			n, ok := byName[strings.TrimSpace(name)]
			if !ok {
				return nil, fmt.Errorf("project %q has no node %q", project, strings.TrimSpace(name))
			}
			picked = append(picked, n)
		}
	}
	sort.Slice(picked, func(i, j int) bool { return picked[i].Name < picked[j].Name })
	return picked, nil
}

// gns3Console is where a node's console is reached, or why it is not
// imported. A console on every address of the server's machine is reached
// at the server's own host; one on the server's loopback is reachable
// from that machine alone, so it is imported only when this machine is it.
func gns3Console(n gns3.Node, serverHost string) (string, int, string) {
	if n.ConsoleType != "telnet" {
		ct := n.ConsoleType
		if ct == "" {
			ct = "none"
		}
		return "", 0, fmt.Sprintf("its console is %q, not telnet", ct)
	}
	if n.Console == nil || *n.Console < 1 || *n.Console > 65535 {
		return "", 0, "it has no console port"
	}
	host := n.ConsoleHost
	switch host {
	case "", "0.0.0.0", "::":
		host = serverHost
	}
	ip := net.ParseIP(host)
	switch {
	case ip == nil && !clabNodeName.MatchString(host):
		return "", 0, fmt.Sprintf("its console host %q is not an address or a host name", n.ConsoleHost)
	case ip != nil && ip.IsLoopback() && !isLoopbackHost(serverHost):
		return "", 0, fmt.Sprintf("its console listens on the server's loopback (%s), which only the server reaches", host)
	}
	return host, *n.Console, ""
}

// isLoopbackHost reports whether host names this machine's loopback.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
