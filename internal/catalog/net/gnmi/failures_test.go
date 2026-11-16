// Package gnmi_test: what the net.gnmi methods do when something fails: a
// read before the write, the device's refusal of a Set, a Set whose answer is
// lost before it was applied or answered OK in a form the client cannot read,
// a read or a stat that fails after an applied Set, a run with no named
// secrets, and a server whose Capabilities is malformed or too long to record
// whole.
package gnmi_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi/gnmitest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// refusing is login with a hook refusing calls whose method ends in suffix.
func refusing(suffix string, code codes.Code) gnmitest.Options {
	opts := login
	opts.Hook = func(_ context.Context, rpc string) error {
		if strings.HasSuffix(rpc, suffix) {
			return status.Errorf(code, "refused by the test")
		}
		return nil
	}
	return opts
}

// update is a one-entry update of path to val.
func update(path string, val any) map[string]any {
	return map[string]any{"update": []any{map[string]any{"path": path, "val": val}}}
}

// TestConfig_AReadBeforeThatFailsWritesNothing refuses the read before.
func TestConfig_AReadBeforeThatFailsWritesNothing(t *testing.T) {
	s := gnmitest.New(t, refusing("/Get", codes.PermissionDenied))
	_, err := run(t, "net.gnmi.config", s, newRecorder(gnmitest.Secrets(s)), update("/system/config/hostname", "r1"))
	if err == nil || !strings.Contains(err.Error(), "before writing") || !strings.Contains(err.Error(), "nothing was written") || s.Sets() != 0 {
		t.Fatalf("config with the read refused = %v after %d Sets", err, s.Sets())
	}
}

// TestConfig_ARefusedSetChangedNothing refuses the Set itself.
func TestConfig_ARefusedSetChangedNothing(t *testing.T) {
	s := gnmitest.New(t, refusing("/Set", codes.InvalidArgument))
	res, err := run(t, "net.gnmi.config", s, newRecorder(gnmitest.Secrets(s)), update("/system/config/hostname", "r1"))
	if err == nil || res.Changed || !strings.Contains(err.Error(), "nothing changed") || !strings.Contains(err.Error(), "InvalidArgument") {
		t.Fatalf("config with the Set refused = %+v, %v", res, err)
	}
}

// TestConfig_ALostAnswerBeforeTheChangeIsSaidSo holds a Set the server never
// reaches to failing the task, the read back saying it was most likely not
// applied, and no undo.
func TestConfig_ALostAnswerBeforeTheChangeIsSaidSo(t *testing.T) {
	opts := login
	opts.Hook = func(ctx context.Context, rpc string) error {
		if strings.HasSuffix(rpc, "/Set") {
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}
	s := gnmitest.New(t, opts)
	d, _ := collection.Lookup("net.gnmi.config")
	rc := newRecorder(gnmitest.Secrets(s))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := d.Invoke(ctx, rc, gnmitest.NewDevice(t, s), update("/interface[name=lo3]", map[string]any{"name": "lo3"}))
	if err == nil || !strings.Contains(err.Error(), "most likely not applied") || res.Changed || inverse(rc) != nil {
		t.Fatalf("Invoke = %+v, %v, inverse %v", res, err, inverse(rc))
	}
}

// TestConfig_AnOKAnswerItCannotReadIsReadBack has the server apply a Set and
// answer OK with a prefix in the field gNMI 0.4.0 deprecated, and holds the
// method to what an applied Set is: read back, changed, its undo kept, and
// never "refused, nothing changed".
func TestConfig_AnOKAnswerItCannotReadIsReadBack(t *testing.T) {
	opts := login
	opts.SetAnswer = func(r *gpb.SetResponse) { r.Prefix = &gpb.Path{Element: []string{"interface"}} }
	s := gnmitest.New(t, opts)
	rc := newRecorder(gnmitest.Secrets(s))
	res, err := run(t, "net.gnmi.config", s, rc, update("/interface[name=lo4]", map[string]any{"name": "lo4"}))
	if err == nil || strings.Contains(err.Error(), "nothing changed") || !strings.Contains(err.Error(), "applied") {
		t.Fatalf("Invoke = %v, want the unreadable answer read back as applied", err)
	}
	if !res.Changed || fmt.Sprint(inverse(rc)["params"]) != "map[delete:[/interface[name=lo4]] encoding:json_ietf]" {
		t.Errorf("result %+v, inverse %v, want the change and the delete of what it created", res, inverse(rc))
	}
	if _, ok := s.Value(t, "/interface[name=lo4]"); !ok {
		t.Error("the server holds no change, so the test proves nothing")
	}
}

// TestConfig_AReadAfterTheSetThatFailsKeepsTheUndo refuses every read once
// the Set is applied, and holds the method to keeping what it changed: the
// task changed, and the undo recorded from the read before.
func TestConfig_AReadAfterTheSetThatFailsKeepsTheUndo(t *testing.T) {
	var applied atomic.Bool
	opts := login
	opts.AfterSet = func(context.Context) error { applied.Store(true); return nil }
	opts.Hook = func(_ context.Context, rpc string) error {
		if strings.HasSuffix(rpc, "/Get") && applied.Load() {
			return status.Error(codes.PermissionDenied, "refused by the test")
		}
		return nil
	}
	s := gnmitest.New(t, opts)
	rc := newRecorder(gnmitest.Secrets(s))
	res, err := run(t, "net.gnmi.config", s, rc, update("/interface[name=lo6]/description", "six"))
	if err == nil || !strings.Contains(err.Error(), "after the Set") {
		t.Fatalf("Invoke = %v, want the read after the Set refused", err)
	}
	if !res.Changed || fmt.Sprint(inverse(rc)["params"]) != "map[delete:[/interface[name=lo6]] encoding:json_ietf]" {
		t.Errorf("result %+v, inverse %v, want the change and its undo kept", res, inverse(rc))
	}
}

// TestConfig_AnAncestorReadThatFailsWritesNothing refuses the read of what
// is above the path a write creates, the read that says what its undo
// deletes, and holds the method to sending no Set rather than a change it
// could not undo.
func TestConfig_AnAncestorReadThatFailsWritesNothing(t *testing.T) {
	var gets atomic.Int32
	opts := login
	opts.Hook = func(_ context.Context, rpc string) error {
		// The first Get reads the path itself; the second, what is above it.
		if strings.HasSuffix(rpc, "/Get") && gets.Add(1) == 2 {
			return status.Error(codes.Unavailable, "refused by the test")
		}
		return nil
	}
	s := gnmitest.New(t, opts)
	_, err := run(t, "net.gnmi.config", s, newRecorder(gnmitest.Secrets(s)), update("/interface[name=lo7]/description", "seven"))
	if err == nil || !strings.Contains(err.Error(), "nothing was written") || s.Sets() != 0 {
		t.Fatalf("Invoke = %v after %d Sets, want the write refused before its Set", err, s.Sets())
	}
}

// failingStat is a run context whose SetStat refuses one key, as a store
// that cannot take a stat would.
type failingStat struct {
	*recorder
	key string
}

// SetStat implements sdk.RunbookContext.
func (f failingStat) SetStat(key string, value any) error {
	if key == f.key {
		return errors.New("the stat store refused " + key)
	}
	return f.recorder.SetStat(key, value)
}

// TestConfig_AStatThatCannotBeRecordedLeavesTheChange refuses the undo, then
// the diff, after an applied Set, and holds the task to reporting the change
// it made rather than none.
func TestConfig_AStatThatCannotBeRecordedLeavesTheChange(t *testing.T) {
	for i, key := range []string{sdk.StatInverse, sdk.StatDiff} {
		s := gnmitest.New(t, login)
		rc := failingStat{recorder: newRecorder(gnmitest.Secrets(s)), key: key}
		res, err := run(t, "net.gnmi.config", s, rc, update(fmt.Sprintf("/interface[name=lo%d]", 10+i), map[string]any{"name": "x"}))
		if err == nil || !strings.Contains(err.Error(), key) || !res.Changed || s.Sets() != 1 {
			t.Errorf("%s refused: Invoke = %+v, %v after %d Sets, want the change reported", key, res, err, s.Sets())
		}
		if key == sdk.StatDiff && inverse(rc.recorder) == nil {
			t.Error("the diff refused: the undo, recorded first, is gone")
		}
	}
}

// TestConfig_ADeleteOfWhatIsThereChangesAndRecordsNoUndo deletes a present
// node: a change, and no undo, since putting it back would journal its value.
func TestConfig_ADeleteOfWhatIsThereChangesAndRecordsNoUndo(t *testing.T) {
	s := gnmitest.New(t, login)
	s.Put(t, "/network-instance[name=lab]", `{"name":"lab","type":"ip-vrf"}`)
	rc := newRecorder(gnmitest.Secrets(s))
	res, err := run(t, "net.gnmi.config", s, rc, map[string]any{"delete": []any{"/network-instance[name=lab]"}})
	if err != nil || !res.Changed || inverse(rc) != nil {
		t.Fatalf("delete = %+v, %v, inverse %v", res, err, inverse(rc))
	}
	if _, ok := s.Value(t, "/network-instance[name=lab]"); ok {
		t.Error("the node is still there")
	}
}

// TestConfig_SecretsInNestedValuesAndARunWithoutThem resolves references at
// every depth of a value, and holds a run with no named secrets to refusing
// before anything is sent.
func TestConfig_SecretsInNestedValuesAndARunWithoutThem(t *testing.T) {
	s := gnmitest.New(t, login)
	val := map[string]any{"keys": []any{map[string]any{"id": 1, "secret": "{{ secret.ntp.key }}"}}, "enabled": true}
	rc := newRecorder(gnmitest.Secrets(s))
	rc.named = map[string]string{"ntp.key": "Ntp-Key-Nested-11"}
	if res, err := run(t, "net.gnmi.config", s, rc, update("/system/ntp", val)); err != nil || !res.Changed {
		t.Fatalf("nested secret = %+v, %v", res, err)
	}
	if v, _ := s.Value(t, "/system/ntp"); !strings.Contains(string(v.GetJsonIetfVal()), "Ntp-Key-Nested-11") {
		t.Errorf("stored %s", v.GetJsonIetfVal())
	}
	without := &plainContext{recorder: newRecorder(gnmitest.Secrets(s))}
	_, err := run(t, "net.gnmi.config", s, without, update("/system/ntp2", val))
	if !errors.Is(err, sdk.ErrNoNamedSecrets) || !strings.Contains(err.Error(), "nothing was sent") {
		t.Fatalf("a run without named secrets = %v", err)
	}
	if _, err := run(t, "net.gnmi.config", s, rc, update("/system/ntp3", []any{"{{ secret.ntp.key | upper }}"})); err == nil {
		t.Error("a reference with a filter inside a list was accepted")
	}
}

// plainContext is a run context with no named secrets: only its recorder's
// four methods.
type plainContext struct{ recorder *recorder }

// InjectSecrets implements sdk.RunbookContext.
func (p *plainContext) InjectSecrets() map[string]string { return p.recorder.InjectSecrets() }

// SetStat implements sdk.RunbookContext.
func (p *plainContext) SetStat(key string, value any) error { return p.recorder.SetStat(key, value) }

// EmitFact implements sdk.RunbookContext.
func (p *plainContext) EmitFact(key string, value any) error {
	return p.recorder.EmitFact(key, value)
}

// TestFacts_SaysWhatItLeftOut reads a server listing malformed entries and
// more models than a fact holds, and holds the read to warning about each.
func TestFacts_SaysWhatItLeftOut(t *testing.T) {
	models := []*gpb.ModelData{{Name: "bad\x1bname"}, {Name: "urn:x:"}}
	for i := 0; i < 2500; i++ {
		models = append(models, &gpb.ModelData{Name: fmt.Sprintf("vendor-model-with-a-long-name-%05d", i), Organization: "Vendor", Version: "2026-01-01"})
	}
	opts := login
	opts.Models = models
	s := gnmitest.New(t, opts)
	rc := newRecorder(gnmitest.Secrets(s))
	if _, err := run(t, "net.gnmi.facts", s, rc, nil); err != nil {
		t.Fatal(err)
	}
	warnings, _ := rc.stats[sdk.StatWarnings].([]string)
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{"1 malformed", "recorded by count and digest only", "name no YANG module"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings %q, want %q", joined, want)
		}
	}
	if _, ok := rc.facts["gnmi.models"]; ok || rc.facts["gnmi.model_count"] != 2501 {
		t.Errorf("facts: models present %v, count %v", ok, rc.facts["gnmi.model_count"])
	}
}

// TestFacts_RefusesWhatItCannotReach fails a read with no device, a record it
// cannot use, and a server refusing Capabilities.
func TestFacts_RefusesWhatItCannotReach(t *testing.T) {
	d, _ := collection.Lookup("net.gnmi.facts")
	if _, err := d.Invoke(context.Background(), newRecorder(nil), nil, nil); err == nil {
		t.Error("no device: nil")
	}
	s := gnmitest.New(t, refusing("/Capabilities", codes.Unavailable))
	device := gnmitest.NewDevice(t, s)
	if _, err := d.Invoke(context.Background(), newRecorder(gnmitest.Secrets(s)), device, nil); gnmiCode(err) != "Unavailable" {
		t.Errorf("a refused Capabilities = %v", err)
	}
	device.Auth = "basic"
	if _, err := d.Invoke(context.Background(), newRecorder(gnmitest.Secrets(s)), device, nil); err == nil {
		t.Error("an unusable record: nil")
	}
	if _, err := run(t, "net.gnmi.config", s, newRecorder(gnmitest.Secrets(s)), update("/a", 1)); err == nil || !strings.Contains(err.Error(), "nothing was written") {
		t.Errorf("config with Capabilities refused = %v", err)
	}
	c, _ := collection.Lookup("net.gnmi.config")
	if _, err := c.Invoke(context.Background(), newRecorder(nil), nil, update("/a", 1)); err == nil {
		t.Error("config with no device: nil")
	}
	if _, err := c.Invoke(context.Background(), newRecorder(nil), device, update("/a", 1)); err == nil {
		t.Error("config with an unusable record: nil")
	}
}

// gnmiCode names the status an error says it carries.
func gnmiCode(err error) string {
	if err != nil && strings.Contains(err.Error(), "Unavailable") {
		return "Unavailable"
	}
	return fmt.Sprint(err)
}
