// Package cml: the calls that change a controller, for the virt.cml
// methods: labs, nodes and links made and removed, labs and nodes started
// and stopped, and the reads those calls are checked against. Measured
// against CML 2.7.2's own OpenAPI document (/api/v0/openapi.json).
package cml

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

// The states CML reports for a lab and a node.
const (
	// StateDefined is a lab or node that has never run, or was wiped.
	StateDefined = "DEFINED_ON_CORE"
	// StateStopped is a lab or node that ran and was stopped.
	StateStopped = "STOPPED"
	// StateStarted is a lab with nodes started; a node that is starting.
	StateStarted = "STARTED"
	// StateQueued is a node waiting to start.
	StateQueued = "QUEUED"
	// StateBooted is a node whose boot finished.
	StateBooted = "BOOTED"
)

// Running reports whether a node in state is started or on its way: what
// lab.start changes and lab.stop undoes.
func Running(state string) bool {
	return state == StateStarted || state == StateQueued || state == StateBooted
}

// Interface is one interface of a node.
type Interface struct {
	ID          string `json:"id"`
	Node        string `json:"node"`
	Label       string `json:"label"`
	Slot        *int   `json:"slot"`
	Type        string `json:"type"`
	IsConnected bool   `json:"is_connected"`
}

// Link is one link of a lab, between two interfaces of two nodes.
type Link struct {
	ID         string `json:"id"`
	InterfaceA string `json:"interface_a"`
	InterfaceB string `json:"interface_b"`
	NodeA      string `json:"node_a"`
	NodeB      string `json:"node_b"`
}

// NodeSpec is what a node is made from.
type NodeSpec struct {
	// Label is the node's name in its lab.
	Label string
	// Definition is the node definition's id ("iol-xe").
	Definition string
	// X and Y place it on the lab's canvas.
	X, Y int
	// Configuration is its day-0 configuration, or "" for the definition's.
	Configuration string
}

// labPath is the API path of lab id, escaped.
func labPath(id string) string { return "/labs/" + url.PathEscape(id) }

// nodePath is the API path of node id in lab, escaped.
func nodePath(lab, id string) string { return labPath(lab) + "/nodes/" + url.PathEscape(id) }

// send is a call that changes something once logged in, decoding the
// answer into into, or reading none when into is nil.
func (c *Client) send(ctx context.Context, method, path string, body any, into any) error {
	if c.token == "" {
		return errors.New("cml: Connect first")
	}
	var data []byte
	if body != nil {
		var err error
		if data, err = json.Marshal(body); err != nil {
			return err
		}
	}
	return c.call(ctx, method, path, data, into)
}

// Lab reads one lab by id.
func (c *Client) Lab(ctx context.Context, id string) (Lab, error) {
	var lab Lab
	if err := c.get(ctx, labPath(id), &lab); err != nil {
		return Lab{}, err
	}
	lab.ID = id
	return lab, nil
}

// CreateLab makes an empty lab titled title and returns it.
func (c *Client) CreateLab(ctx context.Context, title string) (Lab, error) {
	var lab Lab
	if err := c.send(ctx, http.MethodPost, "/labs", map[string]string{"title": title}, &lab); err != nil {
		return Lab{}, err
	}
	if lab.ID == "" {
		return Lab{}, errors.New("cml: the controller made a lab and named no id for it")
	}
	return lab, nil
}

// StartLab starts every node of lab id.
func (c *Client) StartLab(ctx context.Context, id string) error {
	return c.send(ctx, http.MethodPut, labPath(id)+"/start", nil, nil)
}

// StopLab stops every node of lab id.
func (c *Client) StopLab(ctx context.Context, id string) error {
	return c.send(ctx, http.MethodPut, labPath(id)+"/stop", nil, nil)
}

// WipeLab erases every node's disks in lab id, which must be stopped.
func (c *Client) WipeLab(ctx context.Context, id string) error {
	return c.send(ctx, http.MethodPut, labPath(id)+"/wipe", nil, nil)
}

// DeleteLab removes lab id, which must be stopped and wiped.
func (c *Client) DeleteLab(ctx context.Context, id string) error {
	return c.send(ctx, http.MethodDelete, labPath(id), nil, nil)
}

// CreateNode makes a node in lab from spec, with its definition's
// interfaces, and returns its id.
func (c *Client) CreateNode(ctx context.Context, lab string, spec NodeSpec) (string, error) {
	body := map[string]any{"label": spec.Label, "node_definition": spec.Definition, "x": spec.X, "y": spec.Y}
	if spec.Configuration != "" {
		body["configuration"] = spec.Configuration
	}
	var made struct {
		ID string `json:"id"`
	}
	if err := c.send(ctx, http.MethodPost, labPath(lab)+"/nodes?populate_interfaces=true", body, &made); err != nil {
		return "", err
	}
	if made.ID == "" {
		return "", errors.New("cml: the controller made a node and named no id for it")
	}
	return made.ID, nil
}

// StartNode starts node id of lab.
func (c *Client) StartNode(ctx context.Context, lab, id string) error {
	return c.send(ctx, http.MethodPut, nodePath(lab, id)+"/state/start", nil, nil)
}

// StopNode stops node id of lab.
func (c *Client) StopNode(ctx context.Context, lab, id string) error {
	return c.send(ctx, http.MethodPut, nodePath(lab, id)+"/state/stop", nil, nil)
}

// WipeNode erases node id's disks; the node must be stopped.
func (c *Client) WipeNode(ctx context.Context, lab, id string) error {
	return c.send(ctx, http.MethodPut, nodePath(lab, id)+"/wipe_disks", nil, nil)
}

// DeleteNode removes node id of lab, which must be stopped and wiped.
func (c *Client) DeleteNode(ctx context.Context, lab, id string) error {
	return c.send(ctx, http.MethodDelete, nodePath(lab, id), nil, nil)
}

// NodeInterfaces lists node id's interfaces.
func (c *Client) NodeInterfaces(ctx context.Context, lab, id string) ([]Interface, error) {
	var out []Interface
	if err := c.get(ctx, nodePath(lab, id)+"/interfaces?data=true", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Links lists lab's links.
func (c *Client) Links(ctx context.Context, lab string) ([]Link, error) {
	var out []Link
	if err := c.get(ctx, labPath(lab)+"/links?data=true", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// MaxSlots is the most physical interface slots CML gives one node.
const MaxSlots = 128

// CreateInterfaces gives node in lab physical interfaces in every slot up to
// and including slot that it does not have yet, named as its definition
// names them (iol-xe's slot 4 is Ethernet1/0). A definition that allows
// fewer is refused by the controller.
func (c *Client) CreateInterfaces(ctx context.Context, lab, node string, slot int) error {
	if slot < 0 || slot > MaxSlots {
		return fmt.Errorf("cml: slot %d is outside 0 to %d", slot, MaxSlots)
	}
	return c.send(ctx, http.MethodPost, labPath(lab)+"/interfaces", map[string]any{"node": node, "slot": slot}, nil)
}

// CreateLink links interface a to interface b in lab and returns the
// link's id.
func (c *Client) CreateLink(ctx context.Context, lab, a, b string) (string, error) {
	var made struct {
		ID string `json:"id"`
	}
	if err := c.send(ctx, http.MethodPost, labPath(lab)+"/links", map[string]string{"src_int": a, "dst_int": b}, &made); err != nil {
		return "", err
	}
	if made.ID == "" {
		return "", errors.New("cml: the controller made a link and named no id for it")
	}
	return made.ID, nil
}

// DeleteLink removes link id of lab.
func (c *Client) DeleteLink(ctx context.Context, lab, id string) error {
	return c.send(ctx, http.MethodDelete, labPath(lab)+"/links/"+url.PathEscape(id), nil, nil)
}

// NodeDefinitions lists the ids of the node definitions the controller
// offers ("iol-xe", "iosv").
func (c *Client) NodeDefinitions(ctx context.Context) ([]string, error) {
	var defs []struct {
		ID string `json:"id"`
	}
	if err := c.get(ctx, "/simplified_node_definitions", &defs); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(defs))
	for _, d := range defs {
		if d.ID != "" {
			out = append(out, d.ID)
		}
	}
	return out, nil
}

// WaitBooted polls lab's nodes every interval until each of ids (every
// node, when ids is empty) is BOOTED, or ctx ends, which is then an error
// naming the nodes not yet booted and their states.
func (c *Client) WaitBooted(ctx context.Context, lab string, ids []string, interval time.Duration) error {
	return c.waitNodes(ctx, lab, ids, interval, "finish booting", func(state string) bool { return state == StateBooted })
}

// WaitStopped polls lab's nodes every interval until none of ids (every
// node, when ids is empty) is running, or ctx ends, which is then an error
// naming the nodes still running and their states.
func (c *Client) WaitStopped(ctx context.Context, lab string, ids []string, interval time.Duration) error {
	return c.waitNodes(ctx, lab, ids, interval, "stop", func(state string) bool { return !Running(state) })
}

// waitNodes polls lab's nodes every interval until every one of ids (every
// node, when ids is empty) is in a state done accepts, or ctx ends, which
// is then an error saying the lab did not what, naming the nodes not done.
func (c *Client) waitNodes(ctx context.Context, lab string, ids []string, interval time.Duration, what string, done func(string) bool) error {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	// waiting is what the last read found not done, so a deadline that
	// ends a read partway still names the nodes it was waiting for.
	var waiting []string
	for {
		nodes, err := c.Nodes(ctx, lab)
		if err != nil {
			if ctx.Err() != nil && waiting != nil {
				return fmt.Errorf("cml: lab %s did not %s: %v (%w)", lab, what, waiting, ctx.Err())
			}
			return err
		}
		if waiting = notDone(nodes, ids, done); len(waiting) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("cml: lab %s did not %s: %v (%w)", lab, what, waiting, ctx.Err())
		case <-tick.C:
		}
	}
}

// notDone names each node of ids (every node, when ids is empty) whose
// state done does not accept, as "label (STATE)"; a node of ids the lab
// does not hold is named as missing.
func notDone(nodes []Node, ids []string, done func(string) bool) []string {
	byID := map[string]Node{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	if len(ids) == 0 {
		for _, n := range nodes {
			ids = append(ids, n.ID)
		}
	}
	var out []string
	for _, id := range ids {
		n, ok := byID[id]
		switch {
		case !ok:
			out = append(out, id+" (missing)")
		case !done(n.State):
			out = append(out, fmt.Sprintf("%s (%s)", n.Label, n.State))
		}
	}
	return out
}

// consoleKey is what a console's key must be: the UUID the console server
// connects by.
var consoleKey = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ConsoleKey returns the UUID of node id's first console line, which the
// controller's console server connects to ("connect <uuid>") and which
// names exactly one node, where a lab title may name two.
func (c *Client) ConsoleKey(ctx context.Context, lab, id string) (string, error) {
	var key string
	if err := c.get(ctx, nodePath(lab, id)+"/keys/console?line=0", &key); err != nil {
		return "", err
	}
	if !consoleKey.MatchString(key) {
		return "", fmt.Errorf("cml: node %s's console key %q is not a UUID", id, key)
	}
	return key, nil
}
