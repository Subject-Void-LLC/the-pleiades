// Tests that every file method setting ownership holds its owner and group
// to the operand rule (FAILURE_PATTERNS 1174): each declares them
// collection.ParamFormatOperand, which validation, the engine and the
// rollback planner read, and each refuses such a name itself before it
// connects.
package file_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// ownershipMethods are the file methods that take an owner and a group,
// each with the smallest params it accepts otherwise.
var ownershipMethods = map[string]map[string]any{
	"file.copy":        {"dest": "/srv/app.conf", "content": "x"},
	"file.template":    {"dest": "/srv/app.conf", "content": "x"},
	"file.directory":   {"path": "/srv/app"},
	"file.permissions": {"path": "/srv/app.conf", "mode": "0644"},
	"file.touch":       {"path": "/srv/app.stamp"},
}

// TestOwnership_DeclaredOperands: the declaration is what validation, the
// engine and the rollback planner read, so a method that dropped it would
// be held by its own read alone.
func TestOwnership_DeclaredOperands(t *testing.T) {
	for fqcn := range ownershipMethods {
		d, ok := collection.Lookup(fqcn)
		if !ok {
			t.Fatalf("%s is not registered", fqcn)
		}
		seen := map[string]bool{}
		for _, p := range d.Manifest.Doc.Params {
			if p.Name == "owner" || p.Name == "group" {
				seen[p.Name] = true
				if p.Format != collection.ParamFormatOperand {
					t.Errorf("%s's %s declares format %q, want %q", fqcn, p.Name, p.Format, collection.ParamFormatOperand)
				}
			}
		}
		if !seen["owner"] || !seen["group"] {
			t.Errorf("%s declares owner %v and group %v, want both", fqcn, seen["owner"], seen["group"])
		}
	}
}

// TestOwnership_RefusedBeforeTheConnection calls each method with no
// device, so a call that got as far as connecting fails saying so: an
// owner or group refused by the operand rule must fail before that, by
// name, for the real run and for the check alike.
func TestOwnership_RefusedBeforeTheConnection(t *testing.T) {
	for fqcn, base := range ownershipMethods {
		d, _ := collection.Lookup(fqcn)
		for _, key := range []string{"owner", "group"} {
			for _, value := range []string{"--reference=/etc/shadow", "adm*"} {
				params := map[string]any{key: value, "insecure_skip_host_key_verify": true}
				for k, v := range base {
					params[k] = v
				}
				for mode, call := range map[string]collection.Method{"run": d.Invoke, "check": d.Check} {
					res, err := call(context.Background(), fuzzContext{}, nil, params)
					if err == nil || !strings.Contains(err.Error(), key+" is refused") {
						t.Errorf("%s %s with %s %q = %v, want %s refused before connecting", fqcn, mode, key, value, err, key)
						continue
					}
					if res.Changed || strings.Contains(err.Error(), value) {
						t.Errorf("%s %s: changed %v, error %q: want no change and no value in the error", fqcn, mode, res.Changed, err)
					}
				}
			}
		}
		// The control: the same call with a real name gets as far as the
		// connection, so the refusals above are the rule's and not some
		// earlier one's.
		params := map[string]any{"owner": "www-data", "group": "www-data", "insecure_skip_host_key_verify": true}
		for k, v := range base {
			params[k] = v
		}
		if _, err := d.Invoke(context.Background(), fuzzContext{}, nil, params); err == nil || !strings.Contains(err.Error(), "no target device") {
			t.Errorf("%s with a real owner and group = %v, want it to reach the connection", fqcn, err)
		}
	}
}
