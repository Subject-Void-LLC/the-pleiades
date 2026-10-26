// Package cml: one link of a lab found the way a task names it, by its id or
// by its two ends, for every virt.cml method that acts on a link it did not
// make (its state, a capture on it).
package cml

import (
	"context"
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// The parameters a link is named by.
const (
	ParamLink       = "link"
	ParamA          = "a"
	ParamAInterface = "a_interface"
	ParamB          = "b"
	ParamBInterface = "b_interface"
)

// LinkRef names a link by its id or by its two ends: a node (label or id)
// and its interface (label) at each end, either way round.
type LinkRef struct {
	ID                           string
	A, AInterface, B, BInterface string
}

// ReadLinkRef reads a link's name from params: link, or all four of a,
// a_interface, b and b_interface, and never both.
func ReadLinkRef(params map[string]any) (LinkRef, error) {
	r := LinkRef{
		ID: sdk.StringParam(params, ParamLink),
		A:  sdk.StringParam(params, ParamA), AInterface: sdk.StringParam(params, ParamAInterface),
		B: sdk.StringParam(params, ParamB), BInterface: sdk.StringParam(params, ParamBInterface),
	}
	named := r.A != "" || r.AInterface != "" || r.B != "" || r.BInterface != ""
	all := r.A != "" && r.AInterface != "" && r.B != "" && r.BInterface != ""
	switch {
	case r.ID != "" && named:
		return LinkRef{}, fmt.Errorf("name the link by %s or by its two ends, not both", ParamLink)
	case r.ID == "" && !all:
		return LinkRef{}, fmt.Errorf("name the link by %s, or by %s, %s, %s and %s, all four", ParamLink, ParamA, ParamAInterface, ParamB, ParamBInterface)
	}
	return r, nil
}

// String names the link for a message.
func (r LinkRef) String() string {
	if r.ID != "" {
		return "link " + r.ID
	}
	return fmt.Sprintf("the link between %s %s and %s %s", r.A, r.AInterface, r.B, r.BInterface)
}

// FindLink returns the link of lab ref names, from the lab's nodes and
// links: ErrNotFound when there is none (an unknown node or interface
// included).
func (c *Client) FindLink(ctx context.Context, lab string, nodes []Node, links []Link, ref LinkRef) (Link, error) {
	if ref.ID != "" {
		for _, l := range links {
			if l.ID == ref.ID {
				return l, nil
			}
		}
		return Link{}, fmt.Errorf("%s is not in the lab: %w", ref, ErrNotFound)
	}
	a, err := c.FindEndpoint(ctx, lab, nodes, ref.A, ref.AInterface)
	if err != nil {
		return Link{}, err
	}
	b, err := c.FindEndpoint(ctx, lab, nodes, ref.B, ref.BInterface)
	if err != nil {
		return Link{}, err
	}
	if l, ok := Between(links, a.Interface.ID, b.Interface.ID); ok {
		return l, nil
	}
	return Link{}, fmt.Errorf("%s is not in the lab: %w", ref, ErrNotFound)
}

// LabAndLink finds the lab labRef names and the link lr names in it: what
// every method acting on an existing link begins with.
func (c *Client) LabAndLink(ctx context.Context, labRef string, lr LinkRef) (Lab, Link, error) {
	lab, err := c.FindLab(ctx, labRef)
	if err != nil {
		return Lab{}, Link{}, err
	}
	nodes, err := c.Nodes(ctx, lab.ID)
	if err != nil {
		return lab, Link{}, err
	}
	links, err := c.Links(ctx, lab.ID)
	if err != nil {
		return lab, Link{}, err
	}
	link, err := c.FindLink(ctx, lab.ID, nodes, links, lr)
	return lab, link, err
}
