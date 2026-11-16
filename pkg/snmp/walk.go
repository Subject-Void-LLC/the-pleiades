// Package snmp: a walk of one subtree, bounded, and refusing an agent that
// does not move forward.
package snmp

import (
	"context"
	"errors"
	"fmt"
)

// WalkResult is what a walk read.
type WalkResult struct {
	// Bindings are the subtree's bindings, in the agent's order.
	Bindings []Varbind
	// Truncated says the walk stopped at its limit before it saw the
	// subtree's end, so Bindings may be only its first part.
	Truncated bool
	// Bytes is how many bytes the bindings hold, as the bound counts them.
	Bytes int
}

// ErrNotIncreasing is an agent's answer to a GETNEXT or GETBULK that names
// an OID not after the one asked from. Walking on would loop forever, so the
// walk stops with this rather than follow it.
var ErrNotIncreasing = errors.New("the agent answered an OID not after the one asked from, so the walk stopped rather than loop")

// Walk reads every binding in root's subtree (numeric), up to limit
// bindings (at most MaxWalkResults) and MaxWalkBytes of values, with GETBULK
// where the version and the device allow it and GETNEXT otherwise. It stops
// at the first binding past the subtree, at endOfMibView, or at the limit
// (Truncated). A root that is itself a scalar instance (sysName.0) is read
// whole by walking it: the walk returns nothing for it, since nothing is
// after it inside its own subtree, so a caller reading one value uses Get.
func (c *Client) Walk(ctx context.Context, root string, limit int) (WalkResult, error) {
	return c.WalkWithin(ctx, root, limit, MaxWalkBytes)
}

// WalkWithin is Walk bounded by maxBytes of values (at most MaxWalkBytes)
// rather than MaxWalkBytes, for a caller walking several subtrees under one
// bound: it hands each walk what the earlier ones left.
func (c *Client) WalkWithin(ctx context.Context, root string, limit, maxBytes int) (WalkResult, error) {
	if err := checkOIDs([]string{root}); err != nil {
		return WalkResult{}, err
	}
	if limit < 1 || limit > MaxWalkResults {
		return WalkResult{}, fmt.Errorf("a walk returns from 1 to %d bindings", MaxWalkResults)
	}
	if maxBytes < 1 || maxBytes > MaxWalkBytes {
		return WalkResult{}, fmt.Errorf("a walk keeps from 1 to %d bytes", MaxWalkBytes)
	}
	var res WalkResult
	from := root
	bulk := c.settings.Version != Version1 && !c.settings.NoBulk
	for {
		var got []Varbind
		var err error
		if bulk {
			got, err = c.GetBulk(ctx, []string{from}, 0, min(c.settings.MaxRepetitions, limit-len(res.Bindings)))
		} else {
			got, err = c.GetNext(ctx, []string{from})
		}
		if err != nil {
			var agent *AgentError
			// An SNMPv1 agent answers a GETNEXT past its last object with
			// noSuchName, which is the end of the walk, not a failure.
			if c.settings.Version == Version1 && errors.As(err, &agent) && agent.Status == "noSuchName" {
				return res, nil
			}
			return res, err
		}
		if len(got) == 0 {
			return res, nil
		}
		for _, v := range got {
			if v.Type == TypeEndOfMibView || !Under(v.OID, root) {
				return res, nil
			}
			if compareOIDs(v.OID, from) <= 0 {
				return res, ErrNotIncreasing
			}
			if res.Bytes+v.size() > maxBytes {
				res.Truncated = true
				return res, nil
			}
			res.Bytes += v.size()
			res.Bindings = append(res.Bindings, v)
			from = v.OID
			if len(res.Bindings) >= limit {
				res.Truncated = true
				return res, nil
			}
		}
	}
}
