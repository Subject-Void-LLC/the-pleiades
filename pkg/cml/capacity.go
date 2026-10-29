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
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
)

// NodeLimitWords are the words a controller's refusal holds when it runs
// as many nodes as its license allows, as CML 2.7.2 answers a node's start
// then: 400, "Licensing issue: Maximum node count exceeded." (measured on a
// shared controller, the blind lab's run 7).
const NodeLimitWords = "maximum node count exceeded"

// NodeLimitReached reports whether err is the controller refusing a start
// because it already runs as many nodes as its license allows: a 400 whose
// own words (farside.Words) hold NodeLimitWords, in any case. Any other
// refusal is not, so a caller never tells an agent to free capacity when
// the controller said something else.
func NodeLimitReached(err error) bool {
	var se *StatusError
	if !errors.As(err, &se) || se.Status != http.StatusBadRequest {
		return false
	}
	return strings.Contains(strings.ToLower(farside.Words(se.Body)), NodeLimitWords)
}

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
