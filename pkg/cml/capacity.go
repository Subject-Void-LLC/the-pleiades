// Package cml: how many nodes run on the controller, counted across every
// lab this login can see, so a plan can see how full a shared controller is
// before it stops or starts a node. It is what this login sees, which may
// not be the controller's own limit: the licensing endpoint that names the
// limit answers 403 to an ordinary user.
package cml

import (
	"context"
	"errors"
	"net/http"
)

// Capacity is what RunningNodes counted.
type Capacity struct {
	// Labs is how many labs were counted.
	Labs int
	// Running is how many of their nodes are started, queued or booted.
	Running int
}

// nodeState is one node's id and state, read without the node's
// configuration.
type nodeState struct {
	ID    string `json:"id"`
	State string `json:"state"`
}

// RunningNodes counts the nodes running (Running) in every lab this login
// can see: the labs GET /labs?show_all=true lists, each one's nodes read
// without their configurations. A lab removed, or closed to this login,
// between the list and its read (404 or 403) is skipped rather than failing
// the count, since it is no longer one this login can see.
func (c *Client) RunningNodes(ctx context.Context) (Capacity, error) {
	var ids []string
	if err := c.get(ctx, "/labs?show_all=true", &ids); err != nil {
		return Capacity{}, err
	}
	var out Capacity
	for _, id := range ids {
		var nodes []nodeState
		err := c.get(ctx, labPath(id)+"/nodes?data=true&exclude_configurations=true", &nodes)
		var se *StatusError
		switch {
		case errors.As(err, &se) && (se.Status == http.StatusNotFound || se.Status == http.StatusForbidden):
			// Gone, or not this login's to read: not counted.
			continue
		case err != nil:
			return Capacity{}, err
		}
		out.Labs++
		for _, n := range nodes {
			if Running(n.State) {
				out.Running++
			}
		}
	}
	return out, nil
}
