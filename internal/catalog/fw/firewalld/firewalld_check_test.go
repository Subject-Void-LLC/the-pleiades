// Package firewalld_test: tests of the fw.firewalld.* checks.
package firewalld_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/fw/firewalld"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// TestFirewalldChecks covers each method's check on the fake firewall-cmd,
// with the real run from the same state as its control: no add, remove or
// reload is sent; the change decision is the real run's, half by half
// (the permanent configuration and the runtime one); the predicted state
// sets exactly the halves the task asked for; and no undo is recorded.
func TestFirewalldChecks(t *testing.T) {
	port := map[string]any{"port": 8443, "protocol": "tcp"}
	withExtra := func(extra map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range port {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	both := map[string]any{"permanent_allowed": true, "runtime_allowed": true}
	neither := map[string]any{"permanent_allowed": false, "runtime_allowed": false}
	for _, tc := range []struct {
		name        string
		fqcn        string
		run         collection.Method
		state       ruleFixture
		params      map[string]any
		wantChanged bool
		wantAfter   map[string]any
	}{
		{"allow, denied", "fw.firewalld.allow", firewalld.Allow, deniedEverywhere, port, true, both},
		{"allow, allowed", "fw.firewalld.allow", firewalld.Allow, allowedEverywhere, port, false, both},
		{"allow, runtime only", "fw.firewalld.allow", firewalld.Allow, deniedEverywhere, withExtra(map[string]any{"permanent": false}), true, map[string]any{"permanent_allowed": false, "runtime_allowed": true}},
		{"allow, permanent only", "fw.firewalld.allow", firewalld.Allow, deniedEverywhere, withExtra(map[string]any{"immediate": false}), true, map[string]any{"permanent_allowed": true, "runtime_allowed": false}},
		{"deny, allowed", "fw.firewalld.deny", firewalld.Deny, allowedEverywhere, port, true, neither},
		{"deny, denied", "fw.firewalld.deny", firewalld.Deny, deniedEverywhere, port, false, neither},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, ok := collection.Lookup(tc.fqcn)
			if !ok || !d.Manifest.SupportsCheck || d.Check == nil {
				t.Fatalf("%s does not declare a check", tc.fqcn)
			}
			h := newHarness(t, tc.state)
			checked, err := d.Check(context.Background(), h.rc, h.device, h.params(tc.params))
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			if calls := h.invocations(t); len(calls) != 0 {
				t.Errorf("the check changed the firewall: %v", calls)
			}
			if _, recorded := h.rc.stats[sdk.StatInverse]; recorded {
				t.Error("the check recorded an undo instruction")
			}
			diff, _ := h.rc.stats[sdk.StatDiff].(map[string]any)
			if after, _ := diff["after"].(map[string]any); !reflect.DeepEqual(after, tc.wantAfter) {
				t.Errorf("predicted %v, want %v", after, tc.wantAfter)
			}
			run := newHarness(t, tc.state)
			ran, err := tc.run(context.Background(), run.rc, run.device, run.params(tc.params))
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if checked.Changed != tc.wantChanged || ran.Changed != checked.Changed {
				t.Errorf("check changed %v, run %v; want %v", checked.Changed, ran.Changed, tc.wantChanged)
			}
		})
	}
}

// TestReloadCheck covers fw.firewalld.reload's check: it never reloads,
// reports the change a real reload always reports, and fails, like a real
// reload, when firewalld is not running.
func TestReloadCheck(t *testing.T) {
	d, ok := collection.Lookup("fw.firewalld.reload")
	if !ok || !d.Manifest.SupportsCheck || d.Check == nil {
		t.Fatal("fw.firewalld.reload does not declare a check")
	}
	h := newHarness(t, deniedEverywhere)
	checked, err := d.Check(context.Background(), h.rc, h.device, h.params(nil))
	if err != nil || !checked.Changed {
		t.Fatalf("check = %+v, %v; want a predicted change", checked, err)
	}
	if calls := h.invocations(t); len(calls) != 0 {
		t.Errorf("the check reloaded: %v", calls)
	}
	t.Setenv("FAKE_NOT_RUNNING", "1")
	if _, err := d.Check(context.Background(), h.rc, h.device, h.params(nil)); err == nil || !strings.Contains(err.Error(), "252") {
		t.Errorf("a check with firewalld not running = %v, want it refused as a reload would be", err)
	}
}

// TestFirewalldChecks_FailWhenTheyCannotRecord covers an allow or deny
// check that cannot record its answer: the zone, or the diff holding the
// predicted halves. Either fails the check naming the method, as it fails
// the real run, rather than reporting a decision with nothing behind it,
// and nothing is sent to firewall-cmd that changes it.
func TestFirewalldChecks_FailWhenTheyCannotRecord(t *testing.T) {
	port := map[string]any{"port": 8443, "protocol": "tcp"}
	for _, tc := range []struct {
		name    string
		fqcn    string
		state   ruleFixture
		failKey string
	}{
		{"allow, the zone", "fw.firewalld.allow", deniedEverywhere, "zone"},
		{"allow, the diff", "fw.firewalld.allow", deniedEverywhere, sdk.StatDiff},
		{"deny, the zone", "fw.firewalld.deny", allowedEverywhere, "zone"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := collection.Lookup(tc.fqcn)
			h := newHarness(t, tc.state)
			h.rc.failOnKey = tc.failKey
			_, err := d.Check(context.Background(), h.rc, h.device, h.params(port))
			if err == nil || !strings.Contains(err.Error(), tc.fqcn+": ") || !strings.Contains(err.Error(), "injected failure recording") {
				t.Errorf("check = %v, want the injected failure, named for %s", err, tc.fqcn)
			}
			if calls := h.invocations(t); len(calls) != 0 {
				t.Errorf("the check changed the firewall: %v", calls)
			}
		})
	}
}
