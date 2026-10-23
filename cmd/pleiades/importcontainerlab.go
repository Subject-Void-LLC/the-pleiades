// pleiades import-containerlab: a containerlab lab's nodes, made
// generic_ssh devices, and optionally trusted and onboarded.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/record"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// maxTopologyRead bounds the topology file read.
const maxTopologyRead = 4 << 20

// clabNodeName is what a lab node's name may be to become a device's.
var clabNodeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// clabTopology is the part of containerlab's topology-data.json the import
// reads.
type clabTopology struct {
	Name  string              `json:"name"`
	Nodes map[string]clabNode `json:"nodes"`
}

// clabNode is one node of a lab.
type clabNode struct {
	ShortName    string            `json:"shortname"`
	Kind         string            `json:"kind"`
	Image        string            `json:"image"`
	MgmtIPv4     string            `json:"mgmt-ipv4-address"`
	Labels       map[string]string `json:"labels"`
	PortBindings []struct {
		HostIP   string `json:"host-ip"`
		HostPort int    `json:"host-port"`
		Port     int    `json:"port"`
		Protocol string `json:"protocol"`
	} `json:"port-bindings"`
}

// clabImported is one node as the import reports it.
type clabImported struct {
	Device    string `json:"device"`
	Kind      string `json:"kind"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Address   string `json:"address"`
	Trusted   bool   `json:"trusted"`
	Onboarded bool   `json:"onboarded"`
	// Capabilities are what the device holds once onboarded.
	Capabilities []string `json:"capabilities,omitempty"`
}

// clabResult is what the command reports.
type clabResult struct {
	Lab   string         `json:"lab"`
	Nodes []clabImported `json:"nodes"`
}

// runImportContainerlab imports a lab's nodes. The topology is the file
// containerlab writes on deploy (clab-<lab>/topology-data.json), and only
// nodes it lists as the named lab's are taken: a node whose labels name
// another lab, or a lab name that is not the file's, is refused.
func runImportContainerlab(args []string) error {
	fs := flag.NewFlagSet("import-containerlab", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	path := fs.String("topology", "", "containerlab's topology-data.json for the lab, written on deploy")
	lab := fs.String("lab", "", "the lab's name, which the file must be for")
	only := fs.String("nodes", "", "comma-separated nodes to import; every node when empty")
	username := fs.String("username", "", "with --key, the account every node is reached as")
	keyPath := fs.String("key", "", "a private key file stored as every imported node's credential")
	trust := fs.Bool("trust-first-connect", false, "trust whatever host keys answer at each node's address, unverified, as trust-host --first-connect does")
	onboardNodes := fs.Bool("onboard", false, "onboard each node once it is imported (needs a credential and trusted host keys)")
	replace := fs.Bool("replace", false, "replace the address of a node already in the inventory as a generic_ssh device")
	asJSON := fs.Bool("json", false, "print the result as one JSON document")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("usage: pleiades import-containerlab --topology <topology-data.json> --lab <name> [--nodes a,b] [--username <user> --key <path>] [--trust-first-connect] [--onboard] [--replace] [--json]")
	}
	if *path == "" || *lab == "" {
		return errors.New("--topology and --lab are both required: the import reads the file it is given, for the lab it names")
	}
	if (*username == "") != (*keyPath == "") {
		return errors.New("--username and --key go together")
	}
	if *trust && os.Getenv(remoteexec.KnownHostsEnv) == "" {
		// A lab makes new host keys on every deploy, at the same
		// addresses, so its keys belong in a file of the lab's own, never
		// in the user's (the clean-slate lab's G7).
		return fmt.Errorf("--trust-first-connect needs %s set to a known_hosts file of the lab's own, e.g. export %s=$PWD/known_hosts: a lab makes new host keys on every deploy, so they must not go in your own ~/.ssh/known_hosts", remoteexec.KnownHostsEnv, remoteexec.KnownHostsEnv)
	}
	topo, err := readTopology(*path)
	if err != nil {
		return err
	}
	if topo.Name != *lab {
		return fmt.Errorf("%s is the topology of lab %q, not %q", *path, topo.Name, *lab)
	}
	names, err := clabSelect(topo, *lab, *only)
	if err != nil {
		return err
	}

	invPath := filepath.Join(*dir, inventory.DefaultInventoryFilename)
	hosts, err := inventory.ReadHosts(invPath)
	if err != nil {
		return fmt.Errorf("failed to read inventory (did you run 'pleiades init'?): %w", err)
	}
	res := clabResult{Lab: *lab}
	for _, n := range names {
		node := topo.Nodes[n]
		host, port, from, err := clabAddress(node)
		if err != nil {
			return fmt.Errorf("node %s: %w", n, err)
		}
		props := map[string]pkginventory.PropertyValue{"host": host, "port": port}
		index := slices.IndexFunc(hosts, func(h inventory.HostSpec) bool { return h.Name == n })
		spec := inventory.HostSpec{ID: uuid.New().String(), Name: n, Type: generic.TypeSSH, Tags: []string{"containerlab", "lab-" + *lab}, Properties: props}
		switch {
		case index >= 0 && !*replace:
			return fmt.Errorf("host %q already exists; pass --replace to replace its address", n)
		case index >= 0 && hosts[index].Type != generic.TypeSSH:
			return fmt.Errorf("host %q is a %s, not a %s, so an import cannot replace it", n, hosts[index].Type, generic.TypeSSH)
		case index >= 0:
			// A replacement keeps the record's identity, tags and every
			// property but the address; a changed address leaves what
			// onboarding proved stale, as it should.
			kept := map[string]pkginventory.PropertyValue{}
			for k, v := range hosts[index].Properties {
				kept[k] = v
			}
			kept["host"], kept["port"] = host, port
			spec.ID, spec.Tags, spec.Properties = hosts[index].ID, hosts[index].Tags, kept
		}
		if _, err := inventory.NewItemFactory().Build(record.Record{ID: pkginventory.DeviceID(spec.ID), Name: n, Type: spec.Type, Properties: spec.Properties}); err != nil {
			return fmt.Errorf("node %s not imported: %w", n, err)
		}
		if index >= 0 {
			hosts[index] = spec
		} else {
			hosts = append(hosts, spec)
		}
		res.Nodes = append(res.Nodes, clabImported{Device: n, Kind: node.Kind, Host: host, Port: port, Address: from})
	}

	if *keyPath != "" {
		pem, err := os.ReadFile(*keyPath) // #nosec G304 -- the operator names the key file
		if err != nil {
			return fmt.Errorf("reading --key: %w", err)
		}
		key, err := credential.ResolveMasterKey(*dir)
		if err != nil {
			return fmt.Errorf("failed to resolve master key: %w", err)
		}
		for _, n := range res.Nodes {
			if err := credential.SaveFileStore(*dir, key, n.Device, credential.Credential{Username: *username, PrivateKeyPEM: pem}); err != nil {
				return fmt.Errorf("failed to save %s's credential: %w", n.Device, err)
			}
		}
	}
	if err := inventory.WriteHosts(invPath, hosts); err != nil {
		return err
	}

	// trust-host and onboard print their own reports; with --json those
	// go to standard error, so standard output holds the one document.
	var follow error
	withStdout(*asJSON, func() {
		for i, n := range res.Nodes {
			if *trust {
				if err := runTrustHost([]string{n.Device, "--first-connect", "--dir", *dir}); err != nil {
					follow = fmt.Errorf("trusting %s: %w", n.Device, err)
					return
				}
				res.Nodes[i].Trusted = true
			}
			if *onboardNodes {
				if err := runOnboard([]string{n.Device, "--dir", *dir}); err != nil {
					follow = fmt.Errorf("onboarding %s: %w", n.Device, err)
					return
				}
				res.Nodes[i].Onboarded = true
				repo := inventory.NewFileRepository(invPath, inventory.NewItemFactory())
				if item, err := repo.GetByName(context.Background(), n.Device); err == nil {
					for _, c := range item.Capabilities() {
						res.Nodes[i].Capabilities = append(res.Nodes[i].Capabilities, string(c))
					}
				}
			}
		}
	})
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(res); err != nil {
			return err
		}
		return follow
	}
	for _, n := range res.Nodes {
		fmt.Printf("imported %s (%s) at %s:%d, from its %s\n", n.Device, n.Kind, n.Host, n.Port, n.Address)
	}
	if follow != nil {
		return follow
	}
	if !*trust || !*onboardNodes {
		fmt.Println("next, for each node: pleiades add-credential (unless --key gave one), pleiades trust-host, pleiades onboard")
	}
	return nil
}

// readTopology reads and decodes a topology file within its bound.
func readTopology(path string) (clabTopology, error) {
	f, err := os.Open(path) // #nosec G304 -- the operator names the topology to import
	if err != nil {
		return clabTopology{}, fmt.Errorf("reading the topology: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxTopologyRead+1))
	if err != nil {
		return clabTopology{}, fmt.Errorf("reading the topology: %w", err)
	}
	if len(data) > maxTopologyRead {
		return clabTopology{}, fmt.Errorf("the topology is over %d bytes", maxTopologyRead)
	}
	var topo clabTopology
	if err := json.Unmarshal(data, &topo); err != nil {
		return clabTopology{}, fmt.Errorf("the topology is not containerlab's topology-data.json: %w", err)
	}
	if topo.Name == "" || len(topo.Nodes) == 0 {
		return clabTopology{}, errors.New("the topology names no lab or no nodes")
	}
	return topo, nil
}

// clabSelect returns the nodes to import, sorted: those only names, or
// every node, each refused unless it is the named lab's.
func clabSelect(topo clabTopology, lab, only string) ([]string, error) {
	var names []string
	if only == "" {
		for n := range topo.Nodes {
			names = append(names, n)
		}
	} else {
		for _, n := range strings.Split(only, ",") {
			n = strings.TrimSpace(n)
			if _, ok := topo.Nodes[n]; !ok {
				return nil, fmt.Errorf("lab %s has no node %q", lab, n)
			}
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		node := topo.Nodes[n]
		if !clabNodeName.MatchString(n) || (node.ShortName != "" && node.ShortName != n) {
			return nil, fmt.Errorf("node %q is not a name a device can take", n)
		}
		if got := node.Labels["containerlab"]; got != lab {
			return nil, fmt.Errorf("node %s is labelled as lab %q, not %q, so it is not imported", n, got, lab)
		}
	}
	return names, nil
}

// clabAddress is where a node's SSH is reached: the host port its 22/tcp
// is published on (a wildcard bind read as this machine's loopback), or
// else its management address, which only a machine on the lab's
// management network reaches.
func clabAddress(node clabNode) (string, int, string, error) {
	for _, b := range node.PortBindings {
		if b.Port != 22 || (b.Protocol != "" && b.Protocol != "tcp") || b.HostPort < 1 || b.HostPort > 65535 {
			continue
		}
		host := b.HostIP
		switch host {
		case "", "0.0.0.0":
			host = "127.0.0.1"
		case "::":
			host = "::1"
		}
		if _, err := netip.ParseAddr(host); err != nil {
			return "", 0, "", fmt.Errorf("its published SSH port binds %q, which is not an address", b.HostIP)
		}
		return host, b.HostPort, "published SSH port", nil
	}
	addr, err := netip.ParseAddr(node.MgmtIPv4)
	if err != nil {
		return "", 0, "", errors.New("it publishes no SSH port and has no management address")
	}
	return addr.String(), 22, "management address", nil
}

// withStdout runs f with standard output sent to standard error when
// quiet, so a JSON document stays the only thing on standard output.
func withStdout(quiet bool, f func()) {
	if !quiet {
		f()
		return
	}
	saved := os.Stdout
	os.Stdout = os.Stderr
	defer func() { os.Stdout = saved }()
	f()
}
