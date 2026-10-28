// pleiades import-cml: a Cisco Modeling Labs lab's nodes, made generic_ssh
// devices at their management addresses, read from the controller's API.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Subject-Void-LLC/the-pleiades/internal/credential"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// cmlPasswordEnv names the environment variable the controller's password
// is read from when --password-stdin is not given.
const cmlPasswordEnv = "PLEIADES_CML_PASSWORD" // #nosec G101 -- the name of an environment variable, not a credential

// cmlUnmanaged are node definitions with no management side: they carry a
// lab's links, never a login.
var cmlUnmanaged = map[string]bool{"external_connector": true, "unmanaged_switch": true}

// cmlCiscoTypes maps the CML node definitions that run IOS or IOS XE to
// the Cisco device type each becomes, so net.ios.* reaches them. A node
// definition not named here is a generic device.
var cmlCiscoTypes = map[string]string{
	"iol-xe": "cisco_router", "iosv": "cisco_router", "csr1000v": "cisco_router", "cat8000v": "cisco_router",
	"ioll2-xe": "cisco_switch", "iosvl2": "cisco_switch", "cat9000v-q200": "cisco_switch", "cat9000v-uadp": "cisco_switch",
}

// cmlSpec is the device type and properties a node becomes at addr: a
// Cisco type for an IOS node definition, else a generic one; reached over
// SSH, or over Telnet with via "telnet" (a lab whose nodes run no SSH).
func cmlSpec(n cml.Node, addr netip.Addr, via string) (string, map[string]pkginventory.PropertyValue) {
	host := addr.String()
	if t, ok := cmlCiscoTypes[n.NodeDefinition]; ok {
		// transport is written either way, and a console's properties
		// taken away, so a node imported again with --replace keeps nothing
		// of how it was reached before.
		if via == "telnet" {
			return t, map[string]pkginventory.PropertyValue{"host": host, "transport": "telnet", "console_server": nil, "console_line": nil}
		}
		return t, map[string]pkginventory.PropertyValue{"host": host, "port": 22, "transport": "ssh", "console_server": nil, "console_line": nil}
	}
	if via == "telnet" {
		return gns3DeviceType, map[string]pkginventory.PropertyValue{"telnet_host": host, "telnet_port": 23}
	}
	return generic.TypeSSH, map[string]pkginventory.PropertyValue{"host": host, "port": 22}
}

// cmlImported is one node as the import reports it.
type cmlImported struct {
	Device         string `json:"device"`
	NodeDefinition string `json:"node_definition"`
	State          string `json:"state"`
	Host           string `json:"host,omitempty"`
	// Type is the device type the node became, and Via how it is reached.
	Type string `json:"type,omitempty"`
	Via  string `json:"via,omitempty"`
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
	controller := fs.String("controller", "", "a cml_controller device of this project: its api_base, TLS settings and stored login, in place of --server, --tls-*, --username and the password")
	username := fs.String("username", "", "the controller account to log in as")
	passwordStdin := fs.Bool("password-stdin", false, "read the password from the first line of standard input, instead of "+cmlPasswordEnv)
	fingerprint := fs.String("tls-fingerprint", "", "trust only the controller certificate with this SHA-256 fingerprint (sha256:<hex>), whatever its issuer or dates")
	caFile := fs.String("tls-ca", "", "a PEM file of the CAs the controller's certificate must chain to")
	allowPlaintext := fs.Bool("allow-plaintext", false, "send the password over plain HTTP to a controller that is not this machine")
	trust := fs.Bool("trust-first-connect", false, "trust whatever host keys answer at each node's address, unverified, as trust-host --first-connect does")
	onboardNodes := fs.Bool("onboard", false, "onboard each node once it is imported (needs a credential and trusted host keys)")
	via := fs.String("via", "ssh", "how the nodes are reached: ssh; telnet for a lab whose nodes run no SSH server (a login crossing in cleartext); or console, through the --controller device's console server, for nodes with no address reachable from here")
	replace := fs.Bool("replace", false, "replace the address of a node already in the inventory as the same device type")
	asJSON := fs.Bool("json", false, "print the result as one JSON document")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New("usage: pleiades import-cml (--controller <device> | --server <url> --username <user> [--password-stdin] [--tls-fingerprint sha256:<hex> | --tls-ca <pem>]) --lab <title|id> [--nodes a,b] [--address NODE=IP]... [--trust-first-connect] [--onboard] [--replace] [--json]")
	}
	switch {
	case *controller != "" && (*server != "" || *username != "" || *passwordStdin || *fingerprint != "" || *caFile != "" || *allowPlaintext):
		return errors.New("--controller reads the server, its TLS and the login from that cml_controller device: give none of --server, --username, --password-stdin, --tls-fingerprint, --tls-ca or --allow-plaintext with it")
	case *lab == "":
		return errors.New("--lab is required: the import reads the lab it names")
	case *controller == "" && (*server == "" || *username == ""):
		return errors.New("--server and --username are required without --controller: the import logs in to the controller it names")
	}
	if *via != "ssh" && *via != "telnet" && *via != "console" {
		return fmt.Errorf("--via is ssh, telnet or console, not %q", *via)
	}
	if *via == "console" {
		switch {
		case *controller == "":
			return errors.New("--via console needs --controller: each node is reached through that cml_controller's console server, and names it")
		case len(addresses) > 0 || *trust || *onboardNodes:
			return errors.New("a node reached through the console server has no address of its own: --address, --trust-first-connect and --onboard do not apply with --via console")
		}
	}
	if *via == "telnet" && (*trust || *onboardNodes) {
		return errors.New("--trust-first-connect and --onboard are SSH's; a node reached over Telnet has no host key and is used as imported")
	}
	if *fingerprint != "" && *caFile != "" {
		return errors.New("give --tls-fingerprint or --tls-ca, not both: a pinned certificate needs no CA")
	}
	if *trust && os.Getenv(remoteexec.KnownHostsEnv) == "" {
		return fmt.Errorf("--trust-first-connect needs %s set to a known_hosts file of the lab's own: a lab's host keys must not go in your own ~/.ssh/known_hosts", remoteexec.KnownHostsEnv)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var conn cmlConnection
	var err error
	if *controller != "" {
		conn, err = cmlByController(ctx, *dir, *controller)
	} else {
		conn, err = cmlByFlags(ctx, *server, *username, *passwordStdin, *fingerprint, *caFile, *allowPlaintext)
	}
	if err != nil {
		return err
	}
	client, version := conn.client, conn.version
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
	res := cmlResult{Server: conn.server, Version: version, Lab: picked.Title, Warnings: conn.warnings}
	tags := []string{"cml"}
	if clabNodeName.MatchString(picked.Title) {
		tags = append(tags, "cml-"+picked.Title)
	}
	for _, n := range nodes {
		out := cmlImported{Device: n.Label, NodeDefinition: n.NodeDefinition, State: n.State}
		if *via == "console" {
			deviceType, props, skip, err := cmlConsoleSpec(ctx, client, picked.ID, n, *controller)
			if err != nil {
				return err
			}
			if skip != "" {
				out.Skipped = skip
				res.Nodes = append(res.Nodes, out)
				continue
			}
			spec := inventory.HostSpec{ID: uuid.New().String(), Name: n.Label, Type: deviceType, Tags: tags, Properties: props}
			if hosts, err = labPlace(hosts, spec, *replace); err != nil {
				return err
			}
			out.Address, out.Type, out.Via = "console of "+*controller, deviceType, *via
			res.Nodes = append(res.Nodes, out)
			continue
		}
		addr, from, skip := cmlAddress(n, addresses)
		if skip != "" {
			out.Skipped = skip
			res.Nodes = append(res.Nodes, out)
			continue
		}
		deviceType, props := cmlSpec(n, addr, *via)
		spec := inventory.HostSpec{ID: uuid.New().String(), Name: n.Label, Type: deviceType, Tags: tags, Properties: props}
		if hosts, err = labPlace(hosts, spec, *replace); err != nil {
			return err
		}
		out.Host, out.Address, out.Type, out.Via = addr.String(), from, deviceType, *via
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
	fmt.Printf("CML %s at %s, lab %q\n", termsafe.EscapeLine(res.Version), res.Server, res.Lab)
	for _, n := range res.Nodes {
		// Every word from the controller is quoted: a control character in
		// a label or a definition is shown, not sent to the terminal.
		if n.Skipped != "" {
			fmt.Printf("skipped %q (%q): %s\n", n.Device, n.NodeDefinition, n.Skipped)
			continue
		}
		if n.Via == "console" {
			fmt.Printf("imported %s (%q, %s) as a %s reached through the %s\n", n.Device, n.NodeDefinition, termsafe.EscapeLine(n.State), n.Type, termsafe.EscapeLine(n.Address))
			continue
		}
		fmt.Printf("imported %s (%q, %s) as a %s at %s over %s, from %s\n", n.Device, n.NodeDefinition, termsafe.EscapeLine(n.State), n.Type, n.Host, n.Via, termsafe.EscapeLine(n.Address))
	}
	if follow != nil {
		return follow
	}
	switch {
	case *via == "console":
		fmt.Printf("next, for each node: pleiades add-credential (the node's own login, answered on its console line; the console server's login is %s's)\n", *controller)
	case *via == "telnet":
		fmt.Println("next, for each node: pleiades add-credential (with --enable-password for a router's enable secret); every method reaching one takes insecure_telnet: true among its parameters")
	case !*trust || !*onboardNodes:
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
		// A connector or an unmanaged switch never becomes a device (it has no
		// management address), so its label may be any CML allows, such as
		// "System Bridge CORE1"; only a node that could be imported is held to
		// a device's name.
		if !clabNodeName.MatchString(n.Label) && !cmlUnmanaged[n.NodeDefinition] {
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

// cmlConnection is a logged-in client and what the import reports about
// how it reached the controller.
type cmlConnection struct {
	client   *cml.Client
	version  string
	server   string
	warnings []string
}

// cmlByFlags logs in to the controller --server names, with --username and
// the password from standard input or cmlPasswordEnv, trusting what
// --tls-fingerprint or --tls-ca says, read by the same rule as a
// cml_controller record's properties so a flag and a record can never
// trust differently.
func cmlByFlags(ctx context.Context, server, username string, passwordStdin bool, fingerprint, caFile string, allowPlaintext bool) (cmlConnection, error) {
	if fingerprint != "" && caFile != "" {
		return cmlConnection{}, errors.New("give --tls-fingerprint or --tls-ca, not both: a pinned certificate needs no CA")
	}
	password, err := labPassword(passwordStdin, cmlPasswordEnv, os.Stdin)
	if err != nil {
		return cmlConnection{}, err
	}
	base, err := url.Parse(strings.TrimRight(server, "/"))
	if err != nil || base.Host == "" {
		return cmlConnection{}, fmt.Errorf("--server %q is not a URL", server)
	}
	warning, err := labPlaintext(base, true, allowPlaintext)
	if err != nil {
		return cmlConnection{}, err
	}
	props := map[string]pkginventory.PropertyValue{}
	if fingerprint != "" {
		props[devicetls.FingerprintProperty] = fingerprint
	}
	if caFile != "" {
		pem, err := os.ReadFile(caFile) // #nosec G304 -- the operator names the CA file
		if err != nil {
			return cmlConnection{}, fmt.Errorf("reading --tls-ca: %w", err)
		}
		props[devicetls.CAPEMProperty] = string(pem)
	}
	settings, err := devicetls.Parse(pkginventory.NewProperties(props))
	if err != nil {
		return cmlConnection{}, fmt.Errorf("--tls-fingerprint or --tls-ca: %w", err)
	}
	tlsConfig, err := settings.Config(nil)
	if err != nil {
		return cmlConnection{}, err
	}
	client, err := cml.New(server, labHTTPClient(tlsConfig), cml.Credentials{Username: username, Password: password})
	if err != nil {
		return cmlConnection{}, err
	}
	version, err := client.Connect(ctx)
	if err != nil {
		return cmlConnection{}, err
	}
	conn := cmlConnection{client: client, version: version, server: base.Redacted()}
	if warning != "" {
		conn.warnings = append(conn.warnings, warning)
	}
	return conn, nil
}

// cmlByController logs in to the controller the project's cml_controller
// device named name stands for, with its stored login and its own TLS
// settings, so the controller's password comes from the vault and never
// from standard input or the environment.
func cmlByController(ctx context.Context, dir, name string) (cmlConnection, error) {
	repo := inventory.NewFileRepository(filepath.Join(dir, inventory.DefaultInventoryFilename), inventory.NewItemFactory())
	device, err := repo.GetByName(ctx, name)
	if err != nil {
		return cmlConnection{}, fmt.Errorf("--controller %s: %w", name, err)
	}
	cred, err := credential.NewLazyFileStore(dir).Lookup(ctx, name)
	if errors.Is(err, credential.ErrNotFound) {
		return cmlConnection{}, fmt.Errorf("--controller %s has no login stored (pleiades add-credential %s --username <user> --password-stdin)", name, name)
	}
	if err != nil {
		return cmlConnection{}, fmt.Errorf("--controller %s: %w", name, err)
	}
	client, version, warnings, err := cml.ClientFor(ctx, device, name, credential.Flatten(cred))
	if err != nil {
		return cmlConnection{}, fmt.Errorf("--controller %s: %w", name, err)
	}
	server := ""
	if c, ok := device.(capability.CMLAPICapable); ok {
		server = c.CMLBaseURL()
	}
	return cmlConnection{client: client, version: version, server: server, warnings: warnings}, nil
}

// cmlConsoleSpec is the device a node becomes when it is reached through
// controller's console server: its Cisco type, with transport console,
// console_server naming controller and console_line the node's console
// UUID from the controller, which names exactly one node's line. A node
// that is not IOS or IOS XE is skipped, saying why.
func cmlConsoleSpec(ctx context.Context, client *cml.Client, lab string, n cml.Node, controller string) (string, map[string]pkginventory.PropertyValue, string, error) {
	deviceType, ok := cmlCiscoTypes[n.NodeDefinition]
	if !ok {
		return "", nil, "a console import is for IOS and IOS XE nodes, whose login it answers", nil
	}
	key, err := client.ConsoleKey(ctx, lab, n.ID)
	if err != nil {
		return "", nil, "", err
	}
	// An address left from an import over SSH or Telnet is taken away: a
	// console device has none.
	return deviceType, map[string]pkginventory.PropertyValue{"transport": "console", "console_server": controller, "console_line": key, "host": nil, "port": nil}, "", nil
}
