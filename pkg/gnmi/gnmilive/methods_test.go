// Package gnmilive: onboarding and the net.gnmi methods against SR Linux and
// Stratum, through the same prober and registered Invoke a run calls, each
// change read back over SR Linux's own command line.
package gnmilive

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/gnmi"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/devices/generic"
	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory/onboard"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// recorder is a task's context: the secrets it hands the method, the named
// secrets its run holds, and what the method recorded.
type recorder struct {
	mu         sync.Mutex
	secrets    map[string]string
	named      map[string]string
	stats      map[string]any
	facts      map[string]any
	registered []string
}

// newRecorder returns a context holding secrets.
func newRecorder(secrets map[string]string) *recorder {
	return &recorder{secrets: secrets, stats: map[string]any{}, facts: map[string]any{}}
}

// InjectSecrets implements sdk.RunbookContext.
func (r *recorder) InjectSecrets() map[string]string { return r.secrets }

// SetStat implements sdk.RunbookContext.
func (r *recorder) SetStat(key string, value any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stats[key] = value
	return nil
}

// EmitFact implements sdk.RunbookContext.
func (r *recorder) EmitFact(key string, value any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.facts[key] = value
	return nil
}

// NamedSecret implements sdk.NamedSecrets.
func (r *recorder) NamedSecret(name, field string) (string, bool, error) {
	v, ok := r.named[name+"."+field]
	return v, ok, nil
}

// RegisterSecret implements sdk.SecretRegistrar.
func (r *recorder) RegisterSecret(values ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.registered = append(r.registered, values...)
}

// invoke runs the registered fqcn against device.
func invoke(t *testing.T, fqcn string, device inventory.InventoryItem, rc sdk.RunbookContext, params map[string]any) (collection.Result, error) {
	t.Helper()
	d, ok := collection.Lookup(fqcn)
	if !ok {
		t.Fatalf("%s is not registered", fqcn)
	}
	return d.Invoke(ctxOf(t), rc, device, params)
}

// TestSRLinux_OnboardsAndRecordsItsFacts runs the gNMI prober onboarding
// runs, then net.gnmi.facts, and holds both to what SR Linux said.
func TestSRLinux_OnboardsAndRecordsItsFacts(t *testing.T) {
	s := startSRLinux(t)
	device, err := srlPinned(s.target)
	if err != nil {
		t.Fatal(err)
	}
	prober, ok := onboard.Lookup(generic.TypeGNMI)
	if !ok {
		t.Fatal("no gNMI prober")
	}
	probed, err := prober.Probe(ctxOf(t), device, srlSecrets(srlPassword))
	if err != nil {
		t.Fatal(err)
	}
	if len(probed.Capabilities) != 1 || probed.Capabilities[0] != capability.NameGNMI || probed.Facts["gnmi_version"] == "" {
		t.Errorf("probe = %+v", probed)
	}
	// The pin is the record's own setting, said on every onboarding; nothing
	// else is weakened.
	if len(probed.Warnings) != 1 || !strings.Contains(probed.Warnings[0], "pinned") {
		t.Errorf("warnings = %v", probed.Warnings)
	}
	rc := newRecorder(srlSecrets(srlPassword))
	if _, err := invoke(t, "net.gnmi.facts", device, rc, nil); err != nil {
		t.Fatal(err)
	}
	if rc.facts["yang.completeness"] != "partial" || rc.facts["gnmi.models_digest"] != probed.Facts["gnmi_models_digest"] {
		t.Errorf("facts = completeness %v, digest %v (onboarding's %v)", rc.facts["yang.completeness"], rc.facts["gnmi.models_digest"], probed.Facts["gnmi_models_digest"])
	}
	modules, _ := rc.facts["yang.modules"].([]string)
	if !strings.Contains(strings.Join(modules, ","), "srl_nokia-interfaces@") {
		t.Errorf("yang.modules = %v, want SR Linux's interfaces module at its revision", modules)
	}
}

// TestSRLinux_ConfigCreatesUndoesAndMasks creates a loopback with a
// description from a named secret, holds the change to sr_cli's reading and
// the secret to nothing recorded, runs the recorded undo, and holds sr_cli
// to showing the loopback gone.
func TestSRLinux_ConfigCreatesUndoesAndMasks(t *testing.T) {
	const secret = "Gnmi-Live-Secret-73"
	s := startSRLinux(t)
	device, err := srlPinned(s.target)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, err := srlClient(s.target, srlPassword)
		if err == nil {
			_, _ = c.Set(context.Background(), gnmi.SetRequest{Delete: []gnmi.Path{mustPath(t, "/interface[name=lo18]")}})
			_ = c.Close()
		}
	})
	rc := newRecorder(srlSecrets(srlPassword))
	rc.named = map[string]string{"lab.description": secret}
	params := map[string]any{
		"prefix": "/interface[name=lo18]",
		"update": []any{map[string]any{"path": "/description", "val": "{{ secret.lab.description }}"}},
		"backup": true,
	}
	res, err := invoke(t, "net.gnmi.config", device, rc, params)
	if err != nil || !res.Changed {
		t.Fatalf("create = %+v, %v", res, err)
	}
	if out := srCLI(t, s, "info from running /interface lo18"); !strings.Contains(out, secret) {
		t.Fatalf("sr_cli does not show the description: %s", out)
	}
	recorded, _ := json.Marshal(map[string]any{"stats": rc.stats, "facts": rc.facts})
	if strings.Contains(string(recorded), secret) {
		t.Errorf("the run recorded the secret: %s", recorded)
	}
	inv, _ := rc.stats[sdk.StatInverse].(map[string]any)
	undo, _ := inv["params"].(map[string]any)
	if fmt.Sprint(undo["delete"]) != "[/interface[name=lo18]]" {
		t.Fatalf("inverse = %v, want the loopback the run created deleted", inv)
	}

	again := newRecorder(srlSecrets(srlPassword))
	again.named = rc.named
	if res, err := invoke(t, "net.gnmi.config", device, again, params); err != nil || res.Changed || again.stats[sdk.StatInverse] != nil {
		t.Errorf("the same update again = %+v, %v, inverse %v", res, err, again.stats[sdk.StatInverse])
	}

	if res, err := invoke(t, "net.gnmi.config", device, newRecorder(srlSecrets(srlPassword)), undo); err != nil || !res.Changed {
		t.Fatalf("the undo = %+v, %v", res, err)
	}
	if out := srCLI(t, s, "info from running /interface lo18"); strings.Contains(out, "description") {
		t.Errorf("sr_cli still shows the loopback after the undo: %s", out)
	}
}

// TestSRLinux_ConfigRefusesPlainJSON holds net.gnmi.config to refusing an
// encoding SR Linux does not list, before anything is sent.
func TestSRLinux_ConfigRefusesPlainJSON(t *testing.T) {
	s := startSRLinux(t)
	device, err := srlPinned(s.target)
	if err != nil {
		t.Fatal(err)
	}
	_, err = invoke(t, "net.gnmi.config", device, newRecorder(srlSecrets(srlPassword)), map[string]any{
		"encoding": "json", "update": []any{map[string]any{"path": "/interface[name=lo19]/description", "val": "x"}},
	})
	if err == nil || !strings.Contains(err.Error(), "nothing was sent") {
		t.Fatalf("config in plain JSON = %v", err)
	}
}
