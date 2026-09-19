// Package svc_test: tests of the generic service methods' checks.
package svc_test

import (
	"context"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/svc"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// TestChecksReachTheConcreteCheckAndSendNoVerb is the check half of
// TestDispatchesToSystemd, through the same real SSH server and fake
// systemctl (a stopped, disabled unit). Each generic check must resolve the
// device's manager exactly as the real run does, predict the real run's
// decision, and reach the far end with nothing but the read: a check that
// sent the verb would have made the change it was only asked to preview.
// The real run from a fresh harness is the control for each decision.
func TestChecksReachTheConcreteCheckAndSendNoVerb(t *testing.T) {
	tests := []struct {
		name   string
		check  collection.Method
		invoke collection.Method
	}{
		{name: "start", check: svc.CheckStart, invoke: svc.Start},
		{name: "stop", check: svc.CheckStop, invoke: svc.Stop},
		{name: "restart", check: svc.CheckRestart, invoke: svc.Restart},
		{name: "enable", check: svc.CheckEnable, invoke: svc.Enable},
		{name: "disable", check: svc.CheckDisable, invoke: svc.Disable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, "systemd")
			checked, err := tt.check(context.Background(), h.rc, h.dev, params())
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			if sent := h.verbs(t); len(sent) != 0 {
				t.Errorf("the check sent %v to systemctl; a check may only read", sent)
			}

			control := newHarness(t, "systemd")
			ran, err := tt.invoke(context.Background(), control.rc, control.dev, params())
			if err != nil {
				t.Fatalf("the real run: %v", err)
			}
			if checked.Changed != ran.Changed {
				t.Errorf("the check predicts changed %v, the real run reported %v", checked.Changed, ran.Changed)
			}
		})
	}
}
