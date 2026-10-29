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

// Interfaces are the interfaces each definition is made with, as CML 2.7.2
// makes them (read back from a real controller in the blind lab's builds):
// IOL XE's Loopback0 (which has no slot) and four Ethernet ports in slots 0
// to 3, an unmanaged switch's port0 to port7, and a single interface,
// "port", for anything else (an external connector's).
var Interfaces = map[string][]string{
	"iol-xe":           {"Loopback0", "Ethernet0/0", "Ethernet0/1", "Ethernet0/2", "Ethernet0/3"},
	"unmanaged_switch": {"port0", "port1", "port2", "port3", "port4", "port5", "port6", "port7"},
}

// NodeLimitRefusal is what CML 2.7.2 answers, with 400, a node's start on a
// controller already running as many nodes as its license allows.
const NodeLimitRefusal = "Licensing issue: Maximum node count exceeded."

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

// Interface is one interface of a node; a loopback has no slot.
type Interface struct {
	ID, Label string
	Slot      *int
	Connected bool
}

// Link is one link between two interfaces.
type Link struct {
	ID, A, B, NodeA, NodeB string
	// State is the link's own state; empty reads as STARTED.
	State string
	// Capture is the link's packet capture, nil before one starts.
	Capture *Capture
}

// Capture is one link's packet capture as the controller keeps it.
type Capture struct {
	// Key fetches its file from /pcap/{key}.
	Key string
	// Running is whether it is still capturing.
	Running bool
	// MaxPackets, MaxTime and Filter are what it was started with.
	MaxPackets, MaxTime int
	Filter              string
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
	// Fail makes calls answer 500, as a controller failing partway
	// through a task does: a key "PUT /stop" fails every PUT whose path
	// (under /api/v0) ends in /stop, and a key "/nodes" every call whose
	// path ends there, whatever its method.
	Fail map[string]bool
	// Refuse makes calls answer the status each key maps to, with CML's
	// error shape, keyed as Fail is: "PUT /nodes/<id>/state/start": 400 is
	// a controller refusing one node's start, as a shared controller with
	// no room left does.
	Refuse map[string]int
	// NodeLimit, above zero, is how many nodes may run at once across
	// every lab: a node's start that would run one more answers 400 with
	// NodeLimitRefusal, as a controller at its license's limit does.
	NodeLimit int
	// PCAP is what /pcap/{key} answers for any capture's key; nil answers a
	// minimal empty pcap file.
	PCAP []byte
	next int
}

// emptyPCAP is a pcap file's global header and nothing else: little endian,
// version 2.4, Ethernet.
var emptyPCAP = []byte{0xd4, 0xc3, 0xb2, 0xa1, 2, 0, 4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff, 0, 0, 1, 0, 0, 0}

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
	case c.failing(r.Method, path):
		fail(w, http.StatusInternalServerError, "injected failure")
		return
	case c.refusal(r.Method, path) != 0:
		fail(w, c.refusal(r.Method, path), "refused by the test: %s %s", r.Method, path)
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
	case strings.HasPrefix(path, "/pcap/"):
		c.servePCAP(w, strings.TrimPrefix(path, "/pcap/"))
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

// failing reports whether a call of method on path matches a key of Fail.
func (c *Controller) failing(method, path string) bool {
	for key := range c.Fail {
		if matches(key, method, path) {
			return true
		}
	}
	return false
}

// refusal is the status a key of Refuse matching a call of method on path
// maps to, or 0 when none matches.
func (c *Controller) refusal(method, path string) int {
	for key, status := range c.Refuse {
		if matches(key, method, path) {
			return status
		}
	}
	return 0
}

// matches reports whether key ("METHOD suffix", or a bare suffix for any
// method) names a call of method on path.
func matches(key, method, path string) bool {
	want, suffix, hasMethod := strings.Cut(key, " ")
	if !hasMethod {
		want, suffix = "", key
	}
	return (want == "" || want == method) && strings.HasSuffix(path, suffix)
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
			out = append(out, map[string]string{"id": l.ID, "interface_a": l.A, "interface_b": l.B, "node_a": l.NodeA, "node_b": l.NodeB, "state": linkState(l)})
		}
		reply(w, out)
	case route == "links" && r.Method == http.MethodPost:
		c.createLink(w, r, lab)
	case route == "interfaces" && r.Method == http.MethodPost:
		c.createInterfaces(w, r, lab)
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
	case len(rest) >= 3 && rest[0] == "links":
		l, ok := lab.Links[rest[1]]
		if !ok {
			fail(w, http.StatusNotFound, "link not found: %s", rest[1])
			return
		}
		c.serveLink(w, r, l, strings.Join(rest[2:], "/"))
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
			labels = []string{"port"}
		}
		slot := 0
		for _, label := range labels {
			i := &Interface{ID: c.id(), Label: label}
			if !strings.HasPrefix(label, "Loopback") {
				s := slot
				i.Slot, slot = &s, slot+1
			}
			n.Interfaces = append(n.Interfaces, i)
		}
	}
	lab.Nodes[n.ID] = n
	reply(w, map[string]string{"id": n.ID})
}

// createInterfaces gives a node a physical interface in every slot up to
// and including the one asked for that it lacks, as CML 2.7.2 does,
// labelled as IOL names a slot (slot 4 is Ethernet1/0) or portN.
func (c *Controller) createInterfaces(w http.ResponseWriter, r *http.Request, lab *Lab) {
	var body struct {
		Node string
		Slot *int
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.Slot == nil || *body.Slot < 0 || *body.Slot > 128 {
		fail(w, http.StatusUnprocessableEntity, "node and a slot from 0 to 128 are required")
		return
	}
	n, ok := lab.Nodes[body.Node]
	if !ok {
		fail(w, http.StatusNotFound, "node not found: %s", body.Node)
		return
	}
	have := map[int]bool{}
	for _, i := range n.Interfaces {
		if i.Slot != nil {
			have[*i.Slot] = true
		}
	}
	for s := 0; s <= *body.Slot; s++ {
		if have[s] {
			continue
		}
		label := fmt.Sprintf("port%d", s)
		if strings.HasPrefix(n.Definition, "iol") {
			label = fmt.Sprintf("Ethernet%d/%d", s/4, s%4)
		}
		slot := s
		n.Interfaces = append(n.Interfaces, &Interface{ID: c.id(), Label: label, Slot: &slot})
	}
	reply(w, []map[string]any{})
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
	case route == "state/start" && c.NodeLimit > 0 && !running(n.State) && c.runningNodes() >= c.NodeLimit:
		// One more running node would pass the license's limit.
		fail(w, http.StatusBadRequest, "%s", NodeLimitRefusal)
	case route == "state/start" || route == "state/stop":
		_ = transition(n, strings.TrimPrefix(route, "state/"))
		w.WriteHeader(http.StatusNoContent)
	case route == "wipe_disks":
		if err := transition(n, "wipe"); err != "" {
			fail(w, http.StatusBadRequest, "%s", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case route == "keys/console":
		// A console's key, derived from the node's id so a test can
		// predict it.
		reply(w, "11111111-2222-4333-8444-"+n.ID[len(n.ID)-12:])
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

// running reports whether a node in state counts against the license:
// queued, started or booted, as pkg/cml.Running counts it.
func running(state string) bool {
	return state == "QUEUED" || state == "STARTED" || state == "BOOTED"
}

// runningNodes counts the nodes running across every lab.
func (c *Controller) runningNodes() int {
	count := 0
	for _, lab := range c.Labs {
		for _, n := range lab.Nodes {
			if running(n.State) {
				count++
			}
		}
	}
	return count
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

// linkState is a link's state as CML reports it.
func linkState(l *Link) string {
	if l.State == "" {
		return "STARTED"
	}
	return l.State
}

// serveLink answers a link's state and capture calls; route is the path
// after the link's id.
func (c *Controller) serveLink(w http.ResponseWriter, r *http.Request, l *Link, route string) {
	switch {
	case route == "state/start" && r.Method == http.MethodPut:
		l.State = "STARTED"
		w.WriteHeader(http.StatusNoContent)
	case route == "state/stop" && r.Method == http.MethodPut:
		l.State = "STOPPED"
		w.WriteHeader(http.StatusNoContent)
	case route == "capture/start" && r.Method == http.MethodPut:
		var body struct {
			MaxPackets int    `json:"maxpackets"`
			MaxTime    int    `json:"maxtime"`
			BPFilter   string `json:"bpfilter"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || (body.MaxPackets == 0 && body.MaxTime == 0) {
			fail(w, http.StatusBadRequest, "maxpackets or maxtime is required")
			return
		}
		if l.Capture != nil && l.Capture.Running {
			fail(w, http.StatusBadRequest, "a capture is already running on link %s", l.ID)
			return
		}
		l.Capture = &Capture{Key: c.id(), Running: true, MaxPackets: body.MaxPackets, MaxTime: body.MaxTime, Filter: body.BPFilter}
		reply(w, map[string]any{})
	case route == "capture/stop" && r.Method == http.MethodPut:
		if l.Capture == nil {
			fail(w, http.StatusBadRequest, "no capture on link %s", l.ID)
			return
		}
		l.Capture.Running = false
		reply(w, map[string]any{})
	case route == "capture/status" && r.Method == http.MethodGet:
		// As CML 2.7.2 answers it: the capture's config, packets and start
		// while it runs, and {} otherwise (measured 2026-10-25).
		if l.Capture == nil || !l.Capture.Running {
			reply(w, map[string]any{})
			return
		}
		reply(w, map[string]any{
			"config":          map[string]any{"bpfilter": l.Capture.Filter, "link_capture_key": l.Capture.Key, "maxpackets": l.Capture.MaxPackets, "maxtime": l.Capture.MaxTime},
			"packetscaptured": 0, "starttime": "2026-10-26T01:56:26Z",
		})
	case route == "capture/key" && r.Method == http.MethodGet:
		if l.Capture == nil {
			fail(w, http.StatusNotFound, "no capture on link %s", l.ID)
			return
		}
		reply(w, l.Capture.Key)
	default:
		fail(w, http.StatusNotFound, "no such call: %s %s", r.Method, r.URL.Path)
	}
}

// servePCAP answers a capture's file by its key.
func (c *Controller) servePCAP(w http.ResponseWriter, key string) {
	for _, lab := range c.Labs {
		for _, l := range lab.Links {
			if l.Capture != nil && l.Capture.Key == key {
				w.Header().Set("Content-Type", "application/cap")
				data := c.PCAP
				if data == nil {
					data = emptyPCAP
				}
				_, _ = w.Write(data)
				return
			}
		}
	}
	fail(w, http.StatusNotFound, "no capture with key %s", key)
}
