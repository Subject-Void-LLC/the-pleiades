// Tests of RunningNodes against cmltest's controller: the nodes running
// across every lab counted, a lab gone or closed to the login skipped, and
// any other failure reported; and of NodeLimitReached, which tells the
// controller's node-limit refusal from every other.
package cml_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cml/cmltest"
)

// TestRunningNodes_CountsEveryLabTheLoginCanRead: queued, started and
// booted nodes count and stopped or defined ones do not, across labs; a lab
// answering 403 or 404 for its nodes is skipped, a 500 fails the count, and
// a call before Connect is refused.
func TestRunningNodes_CountsEveryLabTheLoginCanRead(t *testing.T) {
	ctl := cmltest.New(t, "admin", "pw")
	c := connected(t, ctl)
	ctx := context.Background()
	a, _ := c.CreateLab(ctx, "a")
	b, _ := c.CreateLab(ctx, "b")
	empty, _ := c.CreateLab(ctx, "empty")
	for lab, states := range map[string][]string{a.ID: {cml.StateBooted, cml.StateStopped}, b.ID: {cml.StateQueued, cml.StateStarted, cml.StateDefined}} {
		for i, s := range states {
			id, err := c.CreateNode(ctx, lab, cml.NodeSpec{Label: string(rune('A' + i)), Definition: "iosv"})
			if err != nil {
				t.Fatal(err)
			}
			ctl.Mu.Lock()
			ctl.Labs[lab].Nodes[id].State = s
			ctl.Mu.Unlock()
		}
	}
	// The fake moves a STARTED node to BOOTED as it is read; either counts.
	if got, err := c.RunningNodes(ctx); err != nil || got != (cml.Capacity{Labs: 3, Running: 3}) {
		t.Errorf("RunningNodes = %+v, %v; want 3 labs, 3 running", got, err)
	}
	for status, want := range map[int]cml.Capacity{403: {Labs: 2, Running: 1}, 404: {Labs: 2, Running: 1}} {
		ctl.Mu.Lock()
		ctl.Refuse = map[string]int{"GET /labs/" + b.ID + "/nodes": status}
		ctl.Mu.Unlock()
		if got, err := c.RunningNodes(ctx); err != nil || got != want {
			t.Errorf("lab b answering %d: %+v, %v; want %+v", status, got, err, want)
		}
	}
	ctl.Mu.Lock()
	ctl.Refuse = nil
	ctl.Fail = map[string]bool{"GET /labs/" + empty.ID + "/nodes": true}
	ctl.Mu.Unlock()
	if _, err := c.RunningNodes(ctx); err == nil {
		t.Error("a lab answering 500 was counted")
	}
	ctl.Mu.Lock()
	ctl.Fail = map[string]bool{"GET /labs": true}
	ctl.Mu.Unlock()
	if _, err := c.RunningNodes(ctx); err == nil {
		t.Error("a failed list of labs was counted")
	}
	unconnected, _ := cml.New(ctl.URL, ctl.Client(), cml.Credentials{Username: "admin", Password: "pw"})
	if _, err := unconnected.RunningNodes(ctx); err == nil {
		t.Error("a count before Connect was answered")
	}
}

// TestNodeLimitReached_OnlyTheLicensesRefusal: the controller's own 400 at
// its node limit, read through a real start against cmltest's controller,
// is the limit; the same words with another status, another 400, an error
// that is no answer at all and nil are not.
func TestNodeLimitReached_OnlyTheLicensesRefusal(t *testing.T) {
	ctl := cmltest.New(t, "admin", "pw")
	c := connected(t, ctl)
	ctx := context.Background()
	lab, _ := c.CreateLab(ctx, "full")
	first, _ := c.CreateNode(ctx, lab.ID, cml.NodeSpec{Label: "R1", Definition: "iol-xe"})
	second, _ := c.CreateNode(ctx, lab.ID, cml.NodeSpec{Label: "R2", Definition: "iol-xe"})
	ctl.Mu.Lock()
	ctl.NodeLimit = 1
	ctl.Mu.Unlock()
	if err := c.StartNode(ctx, lab.ID, first); err != nil {
		t.Fatalf("a start under the limit: %v", err)
	}
	err := c.StartNode(ctx, lab.ID, second)
	if !cml.NodeLimitReached(err) {
		t.Fatalf("a start past the limit = %v, want the license's refusal", err)
	}
	if !strings.Contains(err.Error(), `400: "Licensing issue: Maximum node count exceeded."`) {
		t.Errorf("the refusal does not carry the controller's words: %v", err)
	}
	for name, other := range map[string]error{
		"another status":  &cml.StatusError{Status: 500, Body: `{"description": "Licensing issue: Maximum node count exceeded."}`},
		"another 400":     &cml.StatusError{Status: 400, Body: `{"description": "node R2 is not stopped"}`},
		"not an answer":   errors.New("maximum node count exceeded"),
		"no error at all": nil,
	} {
		if cml.NodeLimitReached(other) {
			t.Errorf("%s was taken for the node limit: %v", name, other)
		}
	}
}
