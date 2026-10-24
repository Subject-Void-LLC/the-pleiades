// Package cml: finding a lab or a node by what a person calls it, its id or
// its title or label, refusing a name two of them share rather than
// guessing which one is meant.
package cml

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ErrNotFound is FindLab's and FindNode's answer for a name nothing has.
var ErrNotFound = errors.New("cml: not found")

// FindLab returns the lab whose id or title is ref: the id when one
// matches, else the one lab with that title. A title two labs share is
// refused, naming their ids; a ref that names nothing is ErrNotFound.
func (c *Client) FindLab(ctx context.Context, ref string) (Lab, error) {
	if ref == "" {
		return Lab{}, errors.New("cml: no lab named")
	}
	lab, err := c.Lab(ctx, ref)
	var se *StatusError
	switch {
	case err == nil:
		return lab, nil
	case errors.As(err, &se) && (se.Status == http.StatusNotFound || se.Status == http.StatusBadRequest || se.Status == http.StatusUnprocessableEntity):
		// Not an id: a title, perhaps.
	default:
		return Lab{}, err
	}
	labs, err := c.Labs(ctx)
	if err != nil {
		return Lab{}, err
	}
	var matches []Lab
	for _, l := range labs {
		if l.Title == ref {
			matches = append(matches, l)
		}
	}
	switch len(matches) {
	case 0:
		return Lab{}, fmt.Errorf("%w: no lab has the id or title %q", ErrNotFound, ref)
	case 1:
		return matches[0], nil
	}
	ids := make([]string, len(matches))
	for i, l := range matches {
		ids[i] = l.ID
	}
	return Lab{}, fmt.Errorf("cml: %d labs are titled %q (%s): name one by its id", len(matches), ref, strings.Join(ids, ", "))
}

// FindNode returns the node of lab whose id or label is ref, from the
// lab's nodes as Nodes reads them. A label two nodes share is refused; a
// ref that names nothing is ErrNotFound.
func FindNode(nodes []Node, ref string) (Node, error) {
	var matches []Node
	for _, n := range nodes {
		if n.ID == ref {
			return n, nil
		}
		if n.Label == ref {
			matches = append(matches, n)
		}
	}
	switch len(matches) {
	case 0:
		return Node{}, fmt.Errorf("%w: no node has the id or label %q", ErrNotFound, ref)
	case 1:
		return matches[0], nil
	}
	return Node{}, fmt.Errorf("cml: %d nodes are labelled %q: name one by its id", len(matches), ref)
}

// FindInterface returns the interface of ifaces labelled label.
func FindInterface(ifaces []Interface, label string) (Interface, error) {
	for _, i := range ifaces {
		if i.Label == label {
			return i, nil
		}
	}
	names := make([]string, len(ifaces))
	for i, f := range ifaces {
		names[i] = f.Label
	}
	return Interface{}, fmt.Errorf("%w: no interface %q (it has %s)", ErrNotFound, label, strings.Join(names, ", "))
}

// Endpoint is one end of a link: a node and one of its interfaces.
type Endpoint struct {
	Node      Node
	Interface Interface
}

// FindEndpoint returns the interface labelled iface of the node of lab
// whose id or label is node, nodes being the lab's nodes as Nodes reads
// them.
func (c *Client) FindEndpoint(ctx context.Context, lab string, nodes []Node, node, iface string) (Endpoint, error) {
	n, err := FindNode(nodes, node)
	if err != nil {
		return Endpoint{}, err
	}
	ifaces, err := c.NodeInterfaces(ctx, lab, n.ID)
	if err != nil {
		return Endpoint{}, err
	}
	i, err := FindInterface(ifaces, iface)
	if err != nil {
		return Endpoint{}, fmt.Errorf("node %s: %w", n.Label, err)
	}
	return Endpoint{Node: n, Interface: i}, nil
}

// Between returns the link of links joining interfaces a and b, either way
// round, and false when none does.
func Between(links []Link, a, b string) (Link, bool) {
	for _, l := range links {
		if l.InterfaceA == a && l.InterfaceB == b || l.InterfaceA == b && l.InterfaceB == a {
			return l, true
		}
	}
	return Link{}, false
}
