// Package lab implements the virt.cml.lab.* methods: a Cisco Modeling
// Labs controller's labs listed, read, made, started, stopped and removed
// through its REST API (pkg/cml), against a cml_controller device. This
// file holds what they share: the parameters, finding the lab a task
// names, and choosing the nodes a start or a stop acts on.
package lab

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
)

// The parameter names the methods share.
const (
	paramLab     = "lab"
	paramNodes   = "nodes"
	paramTimeout = "timeout"
	paramTitle   = "title"
)

// labStats are a lab's stats as lab.list and lab.info report them.
func labStats(l cml.Lab) map[string]any {
	return map[string]any{"id": l.ID, "title": l.Title, "state": l.State}
}

// pick returns the nodes of nodes that refs names, by label or id, every
// node when refs is empty, in label order; a ref that names no node, or a
// label two nodes share, is refused.
func pick(nodes []cml.Node, refs []string) ([]cml.Node, error) {
	if len(refs) == 0 {
		out := append([]cml.Node(nil), nodes...)
		sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
		return out, nil
	}
	seen := map[string]bool{}
	var out []cml.Node
	for _, ref := range refs {
		n, err := cml.FindNode(nodes, ref)
		if err != nil {
			return nil, err
		}
		if !seen[n.ID] {
			seen[n.ID] = true
			out = append(out, n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out, nil
}

// labelsAndIDs returns nodes' labels and ids, in order.
func labelsAndIDs(nodes []cml.Node) (labels, ids []string) {
	labels, ids = []string{}, []string{}
	for _, n := range nodes {
		labels = append(labels, n.Label)
		ids = append(ids, n.ID)
	}
	return labels, ids
}

// findLab finds the lab a task names, naming fqcn in the error.
func findLab(ctx context.Context, client *cml.Client, ref, fqcn string) (cml.Lab, error) {
	lab, err := client.FindLab(ctx, ref)
	if err != nil {
		return cml.Lab{}, fmt.Errorf("%s: %w", fqcn, err)
	}
	return lab, nil
}

// missing reports whether err says a lab does not exist.
func missing(err error) bool { return errors.Is(err, cml.ErrNotFound) }
