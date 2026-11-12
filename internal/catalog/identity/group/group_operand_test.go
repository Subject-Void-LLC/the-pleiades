// Tests that every identity.group method holds the group name to the
// operand rule before it connects (FAILURE_PATTERNS 1174), that each
// declares it collection.ParamFormatOperand for validation, the engine and
// the rollback planner, and that only getent's exit 2 reads as an absent
// group.
package group_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/identity/group"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/operand"
)

// groupMethods are the identity.group methods, each with the smallest
// params it accepts beside a name.
var groupMethods = map[string]map[string]any{
	"identity.group.create": {},
	"identity.group.modify": {"gid": 4000},
	"identity.group.remove": {},
}

// TestNames_DeclaredOperands: the declaration is what validation, the
// engine and the rollback planner read.
func TestNames_DeclaredOperands(t *testing.T) {
	for fqcn := range groupMethods {
		d, ok := collection.Lookup(fqcn)
		if !ok {
			t.Fatalf("%s is not registered", fqcn)
		}
		for _, p := range d.Manifest.Doc.Params {
			if p.Name == "name" && p.Format != collection.ParamFormatOperand {
				t.Errorf("%s's name declares format %q, want %q", fqcn, p.Format, collection.ParamFormatOperand)
			}
		}
	}
}

// TestNames_RefusedBeforeTheConnection calls each method, and its check,
// with no device, so a call that got as far as connecting fails saying so.
func TestNames_RefusedBeforeTheConnection(t *testing.T) {
	for fqcn, base := range groupMethods {
		d, _ := collection.Lookup(fqcn)
		with := func(name string) map[string]any {
			params := map[string]any{"name": name}
			for k, v := range base {
				params[k] = v
			}
			return params
		}
		for _, value := range []string{"-i", "--badname", "adm*", "g?", "web[12]"} {
			for mode, call := range map[string]collection.Method{"run": d.Invoke, "check": d.Check} {
				rc := &ctxStub{secrets: map[string]string{}, stats: map[string]any{}}
				res, err := call(context.Background(), rc, nil, with(value))
				if err == nil || !strings.Contains(err.Error(), "name is refused") {
					t.Errorf("%s %s with name %q = %v, want it refused before connecting", fqcn, mode, value, err)
					continue
				}
				if res.Changed || strings.Contains(err.Error(), value) || len(rc.stats) != 0 {
					t.Errorf("%s %s: changed %v, stats %v, error %q: want nothing recorded and no value in the error", fqcn, mode, res.Changed, rc.stats, err)
				}
			}
		}
		// The control: a real name gets as far as the connection.
		rc := &ctxStub{secrets: map[string]string{}, stats: map[string]any{}}
		if _, err := d.Invoke(context.Background(), rc, nil, with("admins")); err == nil || !strings.Contains(err.Error(), "no target device") {
			t.Errorf("%s with a real name = %v, want it to reach the connection", fqcn, err)
		}
	}
}

// TestGetent_OnlyExitTwoReadsAsAbsent: an exit getent gives for anything
// but "no such entry" must fail the task, never read as an absent group
// (which would send groupadd, or report a removal done), and nothing
// changes the device.
func TestGetent_OnlyExitTwoReadsAsAbsent(t *testing.T) {
	for _, exit := range []int{1, 3, 64} {
		t.Run(fmt.Sprintf("exit %d", exit), func(t *testing.T) {
			for _, call := range []struct {
				name string
				fn   collection.Method
			}{{"create", group.Create}, {"check create", group.CheckCreate}, {"modify", group.Modify}, {"remove", group.Remove}, {"check remove", group.CheckRemove}} {
				h := newHarness(t, groupState{exitOverride: exit})
				_, err := call.fn(context.Background(), h.rc, h.device, h.params("admins", map[string]any{"gid": 4000}))
				if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("getent group admins exited %d", exit)) {
					t.Errorf("%s: err = %v, want getent's exit %d to fail the task", call.name, err, exit)
				}
				if calls := h.invocations(t); len(calls) != 0 {
					t.Errorf("%s: sent %v after getent failed", call.name, calls)
				}
			}
		})
	}
}

// FuzzGroupParams is the identity.group methods' parameter fuzz target, in
// the shape of the file methods' FuzzCopyParams: the device is nil, so
// anything that parses stops at the connection, and the properties are
// that no bytes panic, no change is claimed beside an error, and a name
// pkg/operand refuses never gets as far as the connection.
func FuzzGroupParams(f *testing.F) {
	f.Add("admins", 4000)
	f.Add("", 0)
	f.Add("-i", 4000)
	f.Add("--badname", -1)
	f.Add("adm*", 4000)
	f.Add("g[1]", 0)
	f.Add("app.svc", 5000)

	f.Fuzz(func(t *testing.T, name string, gid int) {
		params := map[string]any{"name": name, "gid": gid}
		for fqcn, fn := range map[string]collection.Method{
			"identity.group.create": group.Create, "identity.group.modify": group.Modify, "identity.group.remove": group.Remove,
		} {
			res, err := fn(context.Background(), &ctxStub{secrets: map[string]string{}, stats: map[string]any{}}, nil, params)
			if err == nil {
				t.Fatalf("%s with no target device returned no error for %+v", fqcn, params)
			}
			if res.Changed {
				t.Fatalf("%s reported a change beside an error for %+v", fqcn, params)
			}
			if operand.Problem(name) != "" && strings.Contains(err.Error(), "no target device") {
				t.Fatalf("%s: name %q, which the operand rule refuses, reached the connection: %v", fqcn, name, err)
			}
		}
	})
}
