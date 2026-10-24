// Package cmltest is a CML 2 controller in memory, for the tests of
// pkg/cml and the virt.cml methods: labs, nodes, interfaces and links with
// the rules a real controller enforces (a lab or node is stopped before it
// is wiped and wiped before it is deleted, an interface takes one link),
// served over TLS with the shapes CML 2.7.2's API answers in. It stands in
// for the controller only where the DevNet sandbox cannot be made to show
// a path; the sandbox's own tests prove the real one.
package cmltest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
)

// Token is the bearer token the controller hands every login.
const Token = "cmltest-token"

// Interfaces are the interfaces each definition is made with: IOL XE's
// four Ethernet ports, and a single port for anything else.
var Interfaces = map[string][]string{
	"iol-xe": {"Loopback0", "Ethernet0/0", "Ethernet0/1", "Ethernet0/2", "Ethernet0/3"},
}

// Definitions are the node definitions the controller offers.
var Definitions = []string{"iol-xe", "iosv", "external_connector", "unmanaged_switch"}

// Lab is one lab the controller holds.
type Lab struct {
	ID, Title string
	Nodes     map[string]*Node
	Links     map[string]*Link
}

// Node is one node: its state and the configuration it was made with.
type Node struct {
	ID, Label, Definition, State, Configuration string
	// polls counts state reads since the node was started; it reports
	// BOOTED from BootPolls on.
	polls      int
	Interfaces []*Interface
}

// Interface is one interface of a node.
type Interface struct {
	ID, Label string
	Slot      int
	Connected bool
}

// Link is one link between two interfaces.
type Link struct {
	ID, A, B, NodeA, NodeB string
}

// Controller is the controller and what it holds, for a test to read and
// arrange under Mu.
type Controller struct {
	*httptest.Server
	// Mu guards everything below.
	Mu sync.Mutex
	// Labs are the labs by id.
	Labs map[string]*Lab
	// Username and Password are the one login it accepts.
	Username, Password string
	// BootPolls is how many reads of a started node pass before it reports
	// BOOTED (1: the first read after the start).
	BootPolls int
	// Calls are each request's method and path, in order.
	Calls []string
	next  int
}

// New starts a controller accepting username and password, closed when t
// ends.
func New(t *testing.T, username, password string) *Controller {
	t.Helper()
	c := &Controller{Labs: map[string]*Lab{}, Username: username, Password: password, BootPolls: 1}
	c.Server = httptest.NewTLSServer(http.HandlerFunc(c.serve))
	t.Cleanup(c.Close)
	return c
}

// id returns a new UUID-shaped id.
func (c *Controller) id() string {
	c.next++
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", c.next)
}

// fail answers status with CML's error shape.
func fail(w http.ResponseWriter, status int, format string, args ...any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": status, "description": fmt.Sprintf(format, args...)})
}

// reply answers v as JSON.
func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// serve routes one request.
func (c *Controller) serve(w http.ResponseWriter, r *http.Request) {
	c.Mu.Lock()
	defer c.Mu.Unlock()
	c.Calls = append(c.Calls, r.Method+" "+r.URL.Path)
	path := strings.TrimPrefix(r.URL.Path, "/api/v0")
	switch {
	case path == "/system_information":
		reply(w, map[string]any{"version": "2.7.2+build.26", "ready": true})
		return
	case path == "/authenticate":
		var login struct{ Username, Password string }
		if json.NewDecoder(r.Body).Decode(&login) != nil || login.Username != c.Username || login.Password != c.Password {
			fail(w, http.StatusForbidden, "authentication failed")
			return
		}
		reply(w, Token)
		return
	case r.Header.Get("Authorization") != "Bearer "+Token:
		fail(w, http.StatusUnauthorized, "no valid token")
		return
	case path == "/simplified_node_definitions":
		var defs []map[string]string
		for _, d := range Definitions {
			defs = append(defs, map[string]string{"id": d})
		}
		reply(w, defs)
		return
	case path == "/labs" && r.Method == http.MethodGet:
		ids := make([]string, 0, len(c.Labs))
		for id := range c.Labs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		reply(w, ids)
		return
	case path == "/labs" && r.Method == http.MethodPost:
		var body struct{ Title string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		lab := &Lab{ID: c.id(), Title: body.Title, Nodes: map[string]*Node{}, Links: map[string]*Link{}}
		c.Labs[lab.ID] = lab
		reply(w, c.labJSON(lab))
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "/labs/"), "/")
	lab, ok := c.Labs[parts[0]]
	if !ok || !strings.HasPrefix(path, "/labs/") {
		fail(w, http.StatusNotFound, "lab not found: %s", parts[0])
		return
	}
	c.serveLab(w, r, lab, parts[1:])
}

// labJSON is lab as GET /labs/{id} answers it.
func (c *Controller) labJSON(lab *Lab) map[string]any {
	return map[string]any{"id": lab.ID, "lab_title": lab.Title, "state": labState(lab), "node_count": len(lab.Nodes), "link_count": len(lab.Links)}
}

// labState is a lab's state from its nodes': started while any runs,
// defined while all are, stopped otherwise.
func labState(lab *Lab) string {
	state := "DEFINED_ON_CORE"
	for _, n := range lab.Nodes {
		switch n.State {
		case "STARTED", "QUEUED", "BOOTED":
			return "STARTED"
		case "STOPPED":
			state = "STOPPED"
		}
	}
	return state
}

// serveLab routes a request under one lab; rest is the path after its id.
func (c *Controller) serveLab(w http.ResponseWriter, r *http.Request, lab *Lab, rest []string) {
	route := strings.Join(rest, "/")
	switch {
	case route == "" && r.Method == http.MethodGet:
		reply(w, c.labJSON(lab))
	case route == "" && r.Method == http.MethodDelete:
		for _, n := range lab.Nodes {
			if n.State != "DEFINED_ON_CORE" {
				fail(w, http.StatusBadRequest, "lab %s has node %s in state %s: stop and wipe it first", lab.ID, n.Label, n.State)
				return
			}
		}
		delete(c.Labs, lab.ID)
		w.WriteHeader(http.StatusNoContent)
	case route == "state":
		reply(w, labState(lab))
	case route == "start" || route == "stop" || route == "wipe":
		for _, n := range lab.Nodes {
			if err := transition(n, route); err != "" {
				fail(w, http.StatusBadRequest, "%s", err)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	case route == "layer3_addresses":
		reply(w, map[string]any{})
	case route == "nodes" && r.Method == http.MethodGet:
		reply(w, c.nodesJSON(lab))
	case route == "nodes" && r.Method == http.MethodPost:
		c.createNode(w, r, lab)
	case route == "links" && r.Method == http.MethodGet:
		var out []map[string]string
		for _, l := range sortedLinks(lab) {
			out = append(out, map[string]string{"id": l.ID, "interface_a": l.A, "interface_b": l.B, "node_a": l.NodeA, "node_b": l.NodeB})
		}
		reply(w, out)
	case route == "links" && r.Method == http.MethodPost:
		c.createLink(w, r, lab)
	case len(rest) == 2 && rest[0] == "links" && r.Method == http.MethodDelete:
		l, ok := lab.Links[rest[1]]
		if !ok {
			fail(w, http.StatusNotFound, "link not found: %s", rest[1])
			return
		}
		for _, n := range lab.Nodes {
			for _, i := range n.Interfaces {
				if i.ID == l.A || i.ID == l.B {
					i.Connected = false
				}
			}
		}
		delete(lab.Links, l.ID)
		w.WriteHeader(http.StatusNoContent)
	case len(rest) >= 2 && rest[0] == "nodes":
		n, ok := lab.Nodes[rest[1]]
		if !ok {
			fail(w, http.StatusNotFound, "node not found: %s", rest[1])
			return
		}
		c.serveNode(w, r, lab, n, strings.Join(rest[2:], "/"))
	default:
		fail(w, http.StatusNotFound, "no such call: %s %s", r.Method, r.URL.Path)
	}
}

// transition applies start, stop or wipe to n, returning why it cannot.
func transition(n *Node, verb string) string {
	switch verb {
	case "start":
		if n.State != "BOOTED" {
			n.State, n.polls = "STARTED", 0
		}
	case "stop":
		if n.State != "DEFINED_ON_CORE" {
			n.State = "STOPPED"
		}
	case "wipe":
		if n.State != "STOPPED" && n.State != "DEFINED_ON_CORE" {
			return fmt.Sprintf("node %s is %s: stop it before wiping", n.Label, n.State)
		}
		n.State = "DEFINED_ON_CORE"
	}
	return ""
}

// nodesJSON is lab's nodes as GET .../nodes?data=true answers them, each
// started node one poll nearer BOOTED.
func (c *Controller) nodesJSON(lab *Lab) []map[string]any {
	ids := make([]string, 0, len(lab.Nodes))
	for id := range lab.Nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		n := lab.Nodes[id]
		if n.State == "STARTED" {
			if n.polls++; n.polls >= c.BootPolls {
				n.State = "BOOTED"
			}
		}
		out = append(out, map[string]any{"id": n.ID, "label": n.Label, "node_definition": n.Definition, "state": n.State, "configuration": n.Configuration})
	}
	return out
}

// createNode makes a node from the request, with its definition's
// interfaces when populate_interfaces is asked for.
func (c *Controller) createNode(w http.ResponseWriter, r *http.Request, lab *Lab) {
	var body struct {
		Label, Configuration string
		Definition           string `json:"node_definition"`
		X, Y                 *int
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.Label == "" || body.Definition == "" || body.X == nil || body.Y == nil {
		fail(w, http.StatusUnprocessableEntity, "label, node_definition, x and y are required")
		return
	}
	known := false
	for _, d := range Definitions {
		known = known || d == body.Definition
	}
	if !known {
		fail(w, http.StatusBadRequest, "node definition not found: %s", body.Definition)
		return
	}
	n := &Node{ID: c.id(), Label: body.Label, Definition: body.Definition, State: "DEFINED_ON_CORE", Configuration: body.Configuration}
	if r.URL.Query().Get("populate_interfaces") == "true" {
		labels := Interfaces[body.Definition]
		if labels == nil {
			labels = []string{"port0"}
		}
		for slot, label := range labels {
			n.Interfaces = append(n.Interfaces, &Interface{ID: c.id(), Label: label, Slot: slot})
		}
	}
	lab.Nodes[n.ID] = n
	reply(w, map[string]string{"id": n.ID})
}

// createLink links two free interfaces of the lab.
func (c *Controller) createLink(w http.ResponseWriter, r *http.Request, lab *Lab) {
	var body struct {
		A string `json:"src_int"`
		B string `json:"dst_int"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	find := func(id string) (*Node, *Interface) {
		for _, n := range lab.Nodes {
			for _, i := range n.Interfaces {
				if i.ID == id {
					return n, i
				}
			}
		}
		return nil, nil
	}
	na, a := find(body.A)
	nb, b := find(body.B)
	switch {
	case a == nil || b == nil:
		fail(w, http.StatusBadRequest, "interface not found")
		return
	case a.Connected || b.Connected:
		fail(w, http.StatusBadRequest, "interface already connected")
		return
	}
	a.Connected, b.Connected = true, true
	l := &Link{ID: c.id(), A: a.ID, B: b.ID, NodeA: na.ID, NodeB: nb.ID}
	lab.Links[l.ID] = l
	reply(w, map[string]string{"id": l.ID})
}

// serveNode routes a request under one node; route is the path after its
// id.
func (c *Controller) serveNode(w http.ResponseWriter, r *http.Request, lab *Lab, n *Node, route string) {
	switch {
	case route == "" && r.Method == http.MethodDelete:
		if n.State != "DEFINED_ON_CORE" {
			fail(w, http.StatusBadRequest, "node %s is %s: stop and wipe it first", n.Label, n.State)
			return
		}
		for _, l := range lab.Links {
			if l.NodeA == n.ID || l.NodeB == n.ID {
				delete(lab.Links, l.ID)
			}
		}
		delete(lab.Nodes, n.ID)
		w.WriteHeader(http.StatusNoContent)
	case route == "state/start" || route == "state/stop":
		_ = transition(n, strings.TrimPrefix(route, "state/"))
		w.WriteHeader(http.StatusNoContent)
	case route == "wipe_disks":
		if err := transition(n, "wipe"); err != "" {
			fail(w, http.StatusBadRequest, "%s", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case route == "interfaces":
		var out []map[string]any
		for _, i := range n.Interfaces {
			out = append(out, map[string]any{"id": i.ID, "node": n.ID, "label": i.Label, "slot": i.Slot, "type": "physical", "is_connected": i.Connected})
		}
		reply(w, out)
	default:
		fail(w, http.StatusNotFound, "no such call: %s %s", r.Method, r.URL.Path)
	}
}

// sortedLinks is lab's links in id order.
func sortedLinks(lab *Lab) []*Link {
	out := make([]*Link, 0, len(lab.Links))
	for _, l := range lab.Links {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
