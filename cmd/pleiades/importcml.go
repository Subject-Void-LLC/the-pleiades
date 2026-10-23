// pleiades import-cml: a Cisco Modeling Labs lab's nodes, made generic_ssh
// devices at their management addresses, read from the controller's API.
package main

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// cmlPasswordEnv names the environment variable the controller's password
// is read from when --password-stdin is not given.
const cmlPasswordEnv = "PLEIADES_CML_PASSWORD"

// cmlUnmanaged are node definitions with no management side: they carry a
// lab's links, never a login.
var cmlUnmanaged = map[string]bool{"external_connector": true, "unmanaged_switch": true}

// cmlImported is one node as the import reports it.
type cmlImported struct {
	Device         string `json:"device"`
	NodeDefinition string `json:"node_definition"`
	State          string `json:"state"`
	Host           string `json:"host,omitempty"`
	// Address says where the host came from: "--address" or "observed".
	Address string `json:"address,omitempty"`
	// Skipped says why a node was not imported.
	Skipped   string `json:"skipped,omitempty"`
	Trusted   bool   `json:"trusted,omitempty"`
	Onboarded bool   `json:"onboarded,omitempty"`
}

// cmlResult is what the command reports.
type cmlResult struct {
	Server   string        `json:"server"`
	Version  string        `json:"version"`
	Lab      string        `json:"lab"`
	Nodes    []cmlImported `json:"nodes"`
	Warnings []string      `json:"warnings,omitempty"`
}

// addressFlags collects --address NODE=IP, each node once.
type addressFlags map[string]netip.Addr

func (a addressFlags) String() string { return fmt.Sprint(map[string]netip.Addr(a)) }

func (a addressFlags) Set(v string) error {
	node, ip, ok := strings.Cut(v, "=")
	addr, err := netip.ParseAddr(ip)
	if !ok || node == "" || err != nil {
		return fmt.Errorf("--address takes NODE=IP, got %q", v)
	}
	if _, dup := a[node]; dup {
		return fmt.Errorf("--address names %s twice", node)
	}
	a[node] = addr
	return nil
}

// runImportCML imports a CML lab's nodes. A node becomes a generic_ssh
// device at the one address the controller observed on it, or at the one
// --address gives; any other node is reported with why, and with the
// addresses its own configuration names, which are only ever a hint.
func runImportCML(args []string) error {
	fs := flag.NewFlagSet("import-cml", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory")
	server := fs.String("server", "", "the CML controller's base URL, e.g. https://cml.lab")
	lab := fs.String("lab", "", "the lab to import, by title or id")
	only := fs.String("nodes", "", "comma-separated nodes to import; every node when empty")
	addresses := addressFlags{}
	fs.Var(addresses, "address", "NODE=IP: the node's management address, where the controller observed none or several (repeatable)")
	username := fs.String("username", "", "the controller account to log in as")
	passwordStdin := fs.Bool("password-stdin", false, "read the password from the first line of standard input, instead of "+cmlPasswordEnv)
	fingerprint := fs.String("tls-fingerprint", "", "trust only the controller certificate with this SHA-256 fingerprint (sha256:<hex>), whatever its issuer or dates")
	caFile := fs.String("tls-ca", "", "a PEM file of the CAs the controller's certificate must chain to")
	allowPlaintext := fs.Bool("allow-plaintext", false, "send the password over plain HTTP to a controller that is not this machine")
	trust := fs.Bool("trust-first-connect", false, "trust whatever host keys answer at each node's address, unverified, as trust-host --first-connect does")
	onboardNodes := fs.Bool("onboard", false, "onboard each node once it is imported (needs a credential and trusted host keys)")
	replace := fs.Bool("replace", false, "replace the address of a node already in the inventory as a generic_ssh device")
	asJSON := fs.Bool("json", false, "print the result as one JSON document")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New("usage: pleiades import-cml --server <url> --lab <title|id> --username <user> [--password-stdin] [--tls-fingerprint sha256:<hex> | --tls-ca <pem>] [--nodes a,b] [--address NODE=IP]... [--trust-first-connect] [--onboard] [--replace] [--json]")
	}
	if *server == "" || *lab == "" || *username == "" {
		return errors.New("--server, --lab and --username are all required: the import logs in to the controller it names and reads the lab it names")
	}
	if *fingerprint != "" && *caFile != "" {
		return errors.New("give --tls-fingerprint or --tls-ca, not both: a pinned certificate needs no CA")
	}
	if *trust && os.Getenv(remoteexec.KnownHostsEnv) == "" {
		return fmt.Errorf("--trust-first-connect needs %s set to a known_hosts file of the lab's own: a lab's host keys must not go in your own ~/.ssh/known_hosts", remoteexec.KnownHostsEnv)
	}
	password, err := labPassword(*passwordStdin, cmlPasswordEnv, os.Stdin)
	if err != nil {
		return err
	}
	base, err := url.Parse(strings.TrimRight(*server, "/"))
	if err != nil || base.Host == "" {
		return fmt.Errorf("--server %q is not a URL", *server)
	}
	warning, err := labPlaintext(base, true, *allowPlaintext)
	if err != nil {
		return err
	}
	var roots *x509.CertPool
	if *caFile != "" {
		pem, err := os.ReadFile(*caFile) // #nosec G304 -- the operator names the CA file
		if err != nil {
			return fmt.Errorf("reading --tls-ca: %w", err)
		}
		roots = x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pem) {
			return fmt.Errorf("--tls-ca %s holds no PEM certificate", *caFile)
		}
	}
	tlsConfig, err := cml.TLSConfig(base.Hostname(), *fingerprint, roots)
	if err != nil {
		return err
	}
	client, err := cml.New(*server, labHTTPClient(tlsConfig), cml.Credentials{Username: *username, Password: password})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	version, err := client.Connect(ctx)
	if err != nil {
		return err
	}
	picked, err := cmlPickLab(ctx, client, *lab)
	if err != nil {
		return err
	}
	all, err := client.Nodes(ctx, picked.ID)
	if err != nil {
		return err
	}
	nodes, err := cmlSelect(all, picked.Title, *only, addresses)
	if err != nil {
		return err
	}

	invPath := filepath.Join(*dir, inventory.DefaultInventoryFilename)
	hosts, err := inventory.ReadHosts(invPath)
	if err != nil {
		return fmt.Errorf("failed to read inventory (did you run 'pleiades init'?): %w", err)
	}
	res := cmlResult{Server: base.Redacted(), Version: version, Lab: picked.Title}
	if warning != "" {
		res.Warnings = append(res.Warnings, warning)
	}
	tags := []string{"cml"}
	if clabNodeName.MatchString(picked.Title) {
		tags = append(tags, "cml-"+picked.Title)
	}
	for _, n := range nodes {
		out := cmlImported{Device: n.Label, NodeDefinition: n.NodeDefinition, State: n.State}
		addr, from, skip := cmlAddress(n, addresses)
		if skip != "" {
			out.Skipped = skip
			res.Nodes = append(res.Nodes, out)
			continue
		}
		spec := inventory.HostSpec{ID: uuid.New().String(), Name: n.Label, Type: generic.TypeSSH, Tags: tags,
			Properties: map[string]pkginventory.PropertyValue{"host": addr.String(), "port": 22}}
		if hosts, err = labPlace(hosts, spec, *replace); err != nil {
			return err
		}
		out.Host, out.Address = addr.String(), from
		res.Nodes = append(res.Nodes, out)
	}
	if err := inventory.WriteHosts(invPath, hosts); err != nil {
		return err
	}

	var follow error
	withStdout(*asJSON, func() {
		for i, n := range res.Nodes {
			if n.Skipped != "" {
				continue
			}
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
	for _, w := range res.Warnings {
		fmt.Fprintln(os.Stderr, w)
	}
	fmt.Printf("CML %s at %s, lab %q\n", res.Version, res.Server, res.Lab)
	for _, n := range res.Nodes {
		// Every word from the controller is quoted: a control character in
		// a label or a definition is shown, not sent to the terminal.
		if n.Skipped != "" {
			fmt.Printf("skipped %q (%q): %s\n", n.Device, n.NodeDefinition, n.Skipped)
			continue
		}
		fmt.Printf("imported %s (%q, %s) at %s, from %s\n", n.Device, n.NodeDefinition, n.State, n.Host, n.Address)
	}
	if follow != nil {
		return follow
	}
	if !*trust || !*onboardNodes {
		fmt.Println("next, for each node: pleiades add-credential, pleiades trust-host, pleiades onboard")
	}
	return nil
}

// cmlPickLab finds the lab named by id exactly, or else by title, which
// must name one lab only.
func cmlPickLab(ctx context.Context, c *cml.Client, want string) (cml.Lab, error) {
	labs, err := c.Labs(ctx)
	if err != nil {
		return cml.Lab{}, err
	}
	var byTitle []cml.Lab
	for _, l := range labs {
		if l.ID == want {
			return l, nil
		}
		if l.Title == want {
			byTitle = append(byTitle, l)
		}
	}
	switch len(byTitle) {
	case 0:
		return cml.Lab{}, fmt.Errorf("this login sees no lab %q", want)
	case 1:
		return byTitle[0], nil
	}
	return cml.Lab{}, fmt.Errorf("%d labs are titled %q; give the one to import by its id", len(byTitle), want)
}

// cmlSelect returns the nodes to import, sorted by label: those only names,
// or every node. A label a device cannot take, one two nodes share, a node
// the lab lacks, and an --address for a node not imported are refused.
func cmlSelect(nodes []cml.Node, lab, only string, addresses addressFlags) ([]cml.Node, error) {
	byLabel := map[string]cml.Node{}
	for _, n := range nodes {
		if !clabNodeName.MatchString(n.Label) {
			return nil, fmt.Errorf("node %q of lab %q is not a name a device can take", n.Label, lab)
		}
		if _, dup := byLabel[n.Label]; dup {
			return nil, fmt.Errorf("two nodes of lab %q are labelled %q", lab, n.Label)
		}
		byLabel[n.Label] = n
	}
	var picked []cml.Node
	if only == "" {
		picked = append(picked, nodes...)
	} else {
		for _, name := range strings.Split(only, ",") {
			n, ok := byLabel[strings.TrimSpace(name)]
			if !ok {
				return nil, fmt.Errorf("lab %q has no node %q", lab, strings.TrimSpace(name))
			}
			picked = append(picked, n)
		}
	}
	sort.Slice(picked, func(i, j int) bool { return picked[i].Label < picked[j].Label })
	for node := range addresses {
		n, ok := byLabel[node]
		switch {
		case !ok:
			return nil, fmt.Errorf("--address names %s, which lab %q has no node of", node, lab)
		case cmlUnmanaged[n.NodeDefinition]:
			return nil, fmt.Errorf("--address names %s, a %s, which has no management address", node, n.NodeDefinition)
		}
	}
	return picked, nil
}

// cmlAddress is the address a node is imported at and where it came from,
// or why it is not imported. A configuration's addresses are never taken
// on their own: a router's names one per interface, and a wrong one would
// send the node's credential to another machine.
func cmlAddress(n cml.Node, addresses addressFlags) (netip.Addr, string, string) {
	if a, ok := addresses[n.Label]; ok {
		return a, "--address", ""
	}
	if cmlUnmanaged[n.NodeDefinition] {
		return netip.Addr{}, "", "it has no management address"
	}
	hint := ""
	if len(n.ConfiguredAddresses) > 0 {
		var s []string
		for _, a := range n.ConfiguredAddresses {
			s = append(s, a.String())
		}
		hint = fmt.Sprintf("; its configuration names %s: pass --address %s=<one> if one of them is its management address", strings.Join(s, ", "), n.Label)
	}
	switch len(n.Addresses) {
	case 1:
		return n.Addresses[0], "observed", ""
	case 0:
		return netip.Addr{}, "", "the controller observed no address on it" + hint
	}
	var s []string
	for _, a := range n.Addresses {
		s = append(s, a.String())
	}
	return netip.Addr{}, "", fmt.Sprintf("the controller observed several addresses on it (%s); pass --address %s=<one>", strings.Join(s, ", "), n.Label)
}
