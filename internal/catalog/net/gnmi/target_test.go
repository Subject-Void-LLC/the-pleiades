// Package gnmi_test: net.gnmi.config against a server that requires a
// request prefix target, as SONiC's does: the task's gnmi_target or the
// device's own reaches every read and the Set, the task's wins, and the undo
// names the target the write used.
package gnmi_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi/gnmitest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// ocYANG is SONiC's target for its OpenConfig tree.
const ocYANG = "OC-YANG"

// runTargeted invokes fqcn against s through a device whose record names
// deviceTarget.
func runTargeted(t *testing.T, fqcn string, s *gnmitest.Server, deviceTarget string, rc sdk.RunbookContext, params map[string]any) (collection.Result, error) {
	t.Helper()
	d, ok := collection.Lookup(fqcn)
	if !ok {
		t.Fatalf("%s is not registered", fqcn)
	}
	device := gnmitest.NewDevice(t, s)
	device.PrefixTarget = deviceTarget
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return d.Invoke(ctx, rc, device, params)
}

// allTargets reports whether every request s received named want.
func allTargets(s *gnmitest.Server, want string) bool {
	targets := s.Targets()
	for _, got := range targets {
		if got != want {
			return false
		}
	}
	return len(targets) > 0
}

// TestConfig_ATargetReachesEveryReadAndTheSet creates a node on a server
// requiring OC-YANG, through the task's parameter, the device's property,
// and the task's parameter over a device naming another, and with neither,
// which the server refuses as SONiC does. The undo names the target and
// deletes what the run created.
func TestConfig_ATargetReachesEveryReadAndTheSet(t *testing.T) {
	create := func(name string) map[string]any {
		return map[string]any{"update": []any{map[string]any{"path": "/interface[name=" + name + "]/description", "val": "managed"}}}
	}
	withTarget := func(params map[string]any, target string) map[string]any {
		params["gnmi_target"] = target
		return params
	}
	for _, tc := range []struct {
		name, device string
		params       map[string]any
	}{
		{"the task's", "", withTarget(create("lo1"), ocYANG)},
		{"the device's", ocYANG, create("lo2")},
		{"the task's over the device's", "CONFIG_DB", withTarget(create("lo3"), ocYANG)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := gnmitest.New(t, gnmitest.Options{Username: login.Username, Password: login.Password, RequireTarget: ocYANG})
			rc := newRecorder(gnmitest.Secrets(s))
			res, err := runTargeted(t, "net.gnmi.config", s, tc.device, rc, tc.params)
			if err != nil || !res.Changed || s.Sets() != 1 {
				t.Fatalf("config = %+v, %v after %d Sets", res, err, s.Sets())
			}
			if !allTargets(s, ocYANG) {
				t.Errorf("targets = %v, want %s on every read and the Set", s.Targets(), ocYANG)
			}
			p, _ := inverse(rc)["params"].(map[string]any)
			if p["gnmi_target"] != ocYANG {
				t.Fatalf("inverse params = %v, want the target the write used", p)
			}
			// The undo, run as the rollback would, through a device naming
			// none: the recorded target alone reaches the tree.
			undo := newRecorder(gnmitest.Secrets(s))
			if res, err := runTargeted(t, "net.gnmi.config", s, "", undo, p); err != nil || !res.Changed {
				t.Fatalf("the undo = %+v, %v", res, err)
			}
			if _, ok := s.Value(t, fmt.Sprint(p["delete"].([]any)[0])+"/description"); ok {
				t.Error("the undo left what the run created")
			}
		})
	}
	s := gnmitest.New(t, gnmitest.Options{Username: login.Username, Password: login.Password, RequireTarget: ocYANG})
	_, err := runTargeted(t, "net.gnmi.config", s, "", newRecorder(gnmitest.Secrets(s)), create("lo4"))
	if err == nil || !strings.Contains(err.Error(), gnmitest.EmptyTargetRefusal) || s.Sets() != 0 {
		t.Fatalf("config naming no target = %v after %d Sets, want SONiC's refusal and nothing written", err, s.Sets())
	}
}
