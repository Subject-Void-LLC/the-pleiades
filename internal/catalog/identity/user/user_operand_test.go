// Tests that every identity.user method holds the account name, and create
// and modify the primary group, to the operand rule before it connects
// (FAILURE_PATTERNS 1174), that each declares them
// collection.ParamFormatOperand for validation, the engine and the rollback
// planner, and that only getent's exit 2 reads as an absent account.
package user_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/catalog/identity/user"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/operand"
)

// userOperands are the operand parameters each method declares.
var userOperands = map[string][]string{
	"identity.user.create": {"name", "group"},
	"identity.user.modify": {"name", "group"},
	"identity.user.remove": {"name"},
}

// TestNames_DeclaredOperands: the declaration is what validation, the
// engine and the rollback planner read.
func TestNames_DeclaredOperands(t *testing.T) {
	for fqcn, keys := range userOperands {
		d, ok := collection.Lookup(fqcn)
		if !ok {
			t.Fatalf("%s is not registered", fqcn)
		}
		formats := map[string]collection.ParamFormat{}
		for _, p := range d.Manifest.Doc.Params {
			formats[p.Name] = p.Format
		}
		for _, key := range keys {
			if formats[key] != collection.ParamFormatOperand {
				t.Errorf("%s's %s declares format %q, want %q", fqcn, key, formats[key], collection.ParamFormatOperand)
			}
		}
	}
}

// TestNames_RefusedBeforeTheConnection calls each method, and its check,
// with no device, so a call that got as far as connecting fails saying
// so. "-i" is the name that mattered: glibc getent passwd -i lists every
// account and exits 0, so its first record, root's, read as the account.
func TestNames_RefusedBeforeTheConnection(t *testing.T) {
	for fqcn, keys := range userOperands {
		d, _ := collection.Lookup(fqcn)
		for _, key := range keys {
			for _, value := range []string{"-i", "--badname", "adm*", "us?r", "web[12]"} {
				params := map[string]any{"name": "deploy", key: value}
				for mode, call := range map[string]collection.Method{"run": d.Invoke, "check": d.Check} {
					rc := &ctxStub{secrets: map[string]string{}, stats: map[string]any{}}
					res, err := call(context.Background(), rc, nil, params)
					if err == nil || !strings.Contains(err.Error(), key+" is refused") {
						t.Errorf("%s %s with %s %q = %v, want %s refused before connecting", fqcn, mode, key, value, err, key)
						continue
					}
					if res.Changed || strings.Contains(err.Error(), value) || len(rc.stats) != 0 {
						t.Errorf("%s %s: changed %v, stats %v, error %q: want nothing recorded and no value in the error", fqcn, mode, res.Changed, rc.stats, err)
					}
				}
			}
		}
		// The control: a real name and group get as far as the connection.
		params := map[string]any{"name": "deploy", "group": "admins"}
		if fqcn == "identity.user.remove" {
			delete(params, "group")
		}
		rc := &ctxStub{secrets: map[string]string{}, stats: map[string]any{}}
		if _, err := d.Invoke(context.Background(), rc, nil, params); err == nil || !strings.Contains(err.Error(), "no target device") {
			t.Errorf("%s with a real name = %v, want it to reach the connection", fqcn, err)
		}
	}
}

// TestGetent_OnlyExitTwoReadsAsAbsent is the class queryDpkg's exit 2 was
// in (FAILURE_PATTERNS 1174): an exit getent gives for anything but "no
// such entry" must fail the task, never read as an absent account (which
// would send useradd) or an absent group. Nothing changes the device.
func TestGetent_OnlyExitTwoReadsAsAbsent(t *testing.T) {
	for _, exit := range []int{1, 3, 64} {
		t.Run(fmt.Sprintf("passwd exit %d", exit), func(t *testing.T) {
			for _, call := range []struct {
				name string
				fn   collection.Method
			}{{"create", user.Create}, {"check create", user.CheckCreate}, {"modify", user.Modify}, {"remove", user.Remove}, {"check remove", user.CheckRemove}} {
				h := newHarness(t, userState{passwdExitOverride: exit})
				_, err := call.fn(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"shell": "/bin/sh"}))
				if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("getent passwd deploy exited %d", exit)) {
					t.Errorf("%s: err = %v, want getent's exit %d to fail the task", call.name, err, exit)
				}
				if calls := h.invocations(t); len(calls) != 0 {
					t.Errorf("%s: sent %v after getent failed", call.name, calls)
				}
			}
		})
		t.Run(fmt.Sprintf("group exit %d", exit), func(t *testing.T) {
			for _, call := range []struct {
				name string
				fn   collection.Method
			}{{"create", user.Create}, {"check create", user.CheckCreate}, {"modify", user.Modify}} {
				state := presentUser
				state.groupExitOverride = exit
				h := newHarness(t, state)
				_, err := call.fn(context.Background(), h.rc, h.device, h.params("deploy", map[string]any{"group": "admins"}))
				if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("getent group admins exited %d", exit)) {
					t.Errorf("%s: err = %v, want getent's exit %d to fail the task, not read as no such group", call.name, err, exit)
				}
				if calls := h.invocations(t); len(calls) != 0 {
					t.Errorf("%s: sent %v after getent failed", call.name, calls)
				}
			}
		})
	}
}

// FuzzUserParams is the identity.user methods' parameter fuzz target, in
// the shape of the file methods' FuzzCopyParams: the device is nil, so
// anything that parses stops at the connection, and the properties are
// that no bytes panic, no change is claimed beside an error, and a name or
// group pkg/operand refuses never gets as far as the connection.
func FuzzUserParams(f *testing.F) {
	f.Add("deploy", "admins", "/bin/bash", "/home/deploy", "Deploy")
	f.Add("", "", "", "", "")
	f.Add("-i", "", "", "", "")
	f.Add("--badname", "-g", "-s", "-d", "-c")
	f.Add("deploy", "adm*", "/bin/sh", "/home/deploy", "x")
	f.Add("us?r", "1000", "", "", "")
	f.Add("deploy", "--help", "", "", "")
	f.Add("app.svc", "www-data", "-l", "--home", "--")

	f.Fuzz(func(t *testing.T, name, group, shell, home, comment string) {
		params := map[string]any{"name": name, "group": group, "shell": shell, "home": home, "comment": comment}
		for fqcn, fn := range map[string]collection.Method{
			"identity.user.create": user.Create, "identity.user.modify": user.Modify, "identity.user.remove": user.Remove,
		} {
			res, err := fn(context.Background(), &ctxStub{secrets: map[string]string{}, stats: map[string]any{}}, nil, params)
			if err == nil {
				t.Fatalf("%s with no target device returned no error for %+v", fqcn, params)
			}
			if res.Changed {
				t.Fatalf("%s reported a change beside an error for %+v", fqcn, params)
			}
			refused := operand.Problem(name) != "" || (fqcn != "identity.user.remove" && operand.Problem(group) != "")
			if refused && strings.Contains(err.Error(), "no target device") {
				t.Fatalf("%s: name %q or group %q, which the operand rule refuses, reached the connection: %v", fqcn, name, group, err)
			}
		}
	})
}
