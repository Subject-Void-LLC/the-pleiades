// Package gnmi_test: net.gnmi.facts and net.gnmi.config against a gNMI
// server over a real loopback gRPC connection and real TLS (gnmitest),
// through each method's registered Invoke: what a read records, each write's
// change read back, the undo recorded for a create alone, every refusal made
// before anything is sent, and a named secret resolved into the value and
// masked everywhere else.
package gnmi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	gpb "github.com/openconfig/gnmi/proto/gnmi"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/gnmi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/gnmi/gnmitest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// login is the throwaway credential the test servers ask for.
var login = gnmitest.Options{Username: "admin", Password: "gnmi-Method-Pass-4"}

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

// everything is every stat and fact r holds, as JSON, for a search.
func (r *recorder) everything(t *testing.T) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"stats": r.stats, "facts": r.facts})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// run invokes the registered fqcn against s's device.
func run(t *testing.T, fqcn string, s *gnmitest.Server, rc sdk.RunbookContext, params map[string]any) (collection.Result, error) {
	t.Helper()
	d, ok := collection.Lookup(fqcn)
	if !ok {
		t.Fatalf("%s is not registered", fqcn)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return d.Invoke(ctx, rc, gnmitest.NewDevice(t, s), params)
}

// TestFacts_RecordsCapabilitiesAndThePartialYangFamily reads a server's
// Capabilities and holds the facts to it: the gnmi.* family, and yang.*
// never complete, with no digest a YANG library's could be compared with.
func TestFacts_RecordsCapabilitiesAndThePartialYangFamily(t *testing.T) {
	s := gnmitest.New(t, login)
	rc := newRecorder(gnmitest.Secrets(s))
	res, err := run(t, "net.gnmi.facts", s, rc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed {
		t.Error("a read reported a change")
	}
	want := map[string]any{
		"gnmi.version":      "0.7.0",
		"gnmi.model_count":  3,
		"yang.completeness": "partial",
		"yang.module_count": 3,
	}
	for k, v := range want {
		if rc.facts[k] != v {
			t.Errorf("fact %s = %v, want %v", k, rc.facts[k], v)
		}
	}
	if got := rc.facts["yang.modules"]; !slices.Equal(got.([]string), []string{"openconfig-interfaces", "openconfig-system", "srl_nokia-interfaces@2024-10-31"}) {
		t.Errorf("yang.modules = %v: a semantic version is no revision, a date is", got)
	}
	if got := rc.facts["gnmi.encodings"]; !slices.Equal(got.([]string), []string{"json", "proto", "json_ietf"}) {
		t.Errorf("gnmi.encodings = %v", got)
	}
	for _, absent := range []string{"yang.modules_digest", "yang.digest_rule", "yang.content_id"} {
		if _, ok := rc.facts[absent]; ok {
			t.Errorf("%s recorded from a gNMI read, which no YANG library gave", absent)
		}
	}
	if d, _ := collection.Lookup("net.gnmi.facts"); d.Check == nil || !d.Manifest.SupportsCheck {
		t.Error("net.gnmi.facts has no check, and a read changes nothing")
	}
}

// TestFacts_OverPlaintextRecordsStatsOnly reads a server that takes no
// credential over plaintext, and holds the read to stats alone, warned.
func TestFacts_OverPlaintextRecordsStatsOnly(t *testing.T) {
	s := gnmitest.New(t, gnmitest.Options{Stratum: true, Plaintext: true})
	rc := newRecorder(nil)
	if _, err := run(t, "net.gnmi.facts", s, rc, nil); err != nil {
		t.Fatal(err)
	}
	if len(rc.facts) != 0 {
		t.Errorf("facts %v recorded over plaintext", rc.facts)
	}
	if rc.stats["gnmi.version"] != "0.7.0" {
		t.Errorf("stats %v, want the read kept as stats", rc.stats)
	}
	warnings, _ := rc.stats[sdk.StatWarnings].([]string)
	if joined := strings.Join(warnings, "\n"); !strings.Contains(joined, "without TLS") || !strings.Contains(joined, "nothing this read found is recorded as a fact") {
		t.Errorf("warnings %q", joined)
	}
}

// TestFacts_ARefusedLoginFailsTheRead sends a wrong password once.
func TestFacts_ARefusedLoginFailsTheRead(t *testing.T) {
	s := gnmitest.New(t, login)
	secrets := gnmitest.Secrets(s)
	secrets["password"] = "wrong"
	if _, err := run(t, "net.gnmi.facts", s, newRecorder(secrets), nil); err == nil || !strings.Contains(err.Error(), "refused the login") {
		t.Fatalf("facts with a wrong password = %v", err)
	}
	if s.Refusals() != 1 {
		t.Errorf("%d refusals, want one", s.Refusals())
	}
}

// inverse is the undo rc recorded, nil when none.
func inverse(rc *recorder) map[string]any {
	inv, _ := rc.stats[sdk.StatInverse].(map[string]any)
	return inv
}

// TestConfig_UpdateCreatesThenChangesNothing creates a list entry under a
// prefix, records the exact undo of the node it created (the entry, which
// was absent, not only the leaf), and the same update again changes nothing.
func TestConfig_UpdateCreatesThenChangesNothing(t *testing.T) {
	s := gnmitest.New(t, login)
	s.Put(t, "/interface[name=ethernet-1/1]", `{"name":"ethernet-1/1","admin-state":"enable"}`)
	params := map[string]any{
		"prefix": "/interface[name=lo7]",
		"update": []any{map[string]any{"path": "/description", "val": "managed loopback"}},
	}
	rc := newRecorder(gnmitest.Secrets(s))
	res, err := run(t, "net.gnmi.config", s, rc, params)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := s.Value(t, "/interface[name=lo7]/description"); !res.Changed || !ok || string(v.GetJsonIetfVal()) != `"managed loopback"` {
		t.Fatalf("changed %v, stored %v", res.Changed, v)
	}
	inv := inverse(rc)
	p, _ := inv["params"].(map[string]any)
	if inv["fqcn"] != "net.gnmi.config" || fmt.Sprint(p["delete"]) != "[/interface[name=lo7]]" || p["encoding"] != "json_ietf" || p["update"] != nil {
		t.Errorf("inverse = %v, want the delete of the entry the run created and nothing else", inv)
	}
	diff, _ := rc.stats[sdk.StatDiff].(map[string]any)
	before := diff[sdk.DiffBefore].(map[string]any)["/interface[name=lo7]/description"].(map[string]any)
	after := diff[sdk.DiffAfter].(map[string]any)["/interface[name=lo7]/description"].(map[string]any)
	if before["exists"] != false || after["exists"] != true {
		t.Errorf("diff = %v", diff)
	}

	again := newRecorder(gnmitest.Secrets(s))
	res, err = run(t, "net.gnmi.config", s, again, params)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed || inverse(again) != nil {
		t.Errorf("the same update again: changed %v, inverse %v", res.Changed, inverse(again))
	}

	// The undo, run as the rollback would, removes the entry.
	undo := newRecorder(gnmitest.Secrets(s))
	if res, err := run(t, "net.gnmi.config", s, undo, p); err != nil || !res.Changed {
		t.Fatalf("the undo = %+v, %v", res, err)
	}
	if _, ok := s.Value(t, "/interface[name=lo7]/description"); ok {
		t.Error("the undo left the created leaf")
	}
	if _, ok := s.Value(t, "/interface[name=ethernet-1/1]"); !ok {
		t.Error("the undo removed what the run did not make")
	}
}

// TestConfig_AReplaceOfWhatIsThereRecordsNoUndo replaces an existing node:
// changed, backed up masked, and no undo, since the earlier value would sit
// in the journal.
func TestConfig_AReplaceOfWhatIsThereRecordsNoUndo(t *testing.T) {
	s := gnmitest.New(t, login)
	s.Put(t, "/system/config", `{"hostname":"r1","login-banner":"hi"}`)
	rc := newRecorder(gnmitest.Secrets(s))
	res, err := run(t, "net.gnmi.config", s, rc, map[string]any{
		"replace": []any{map[string]any{"path": "/system/config", "val": map[string]any{"hostname": "r2"}}},
		"backup":  true,
	})
	if err != nil || !res.Changed || inverse(rc) != nil {
		t.Fatalf("replace = %+v, %v, inverse %v", res, err, inverse(rc))
	}
	backup, _ := rc.stats["backup"].(map[string]any)
	if got := fmt.Sprint(backup["/system/config"]); !strings.Contains(got, "r1") {
		t.Errorf("backup = %v, want the earlier value", backup)
	}
	if v, _ := s.Value(t, "/system/config"); string(v.GetJsonIetfVal()) != `{"hostname":"r2"}` {
		t.Errorf("stored = %s", v.GetJsonIetfVal())
	}
}

// TestConfig_ADeleteOfWhatIsNotThereSendsNothing holds a task of deletes of
// absent paths to sending no Set.
func TestConfig_ADeleteOfWhatIsNotThereSendsNothing(t *testing.T) {
	s := gnmitest.New(t, login)
	res, err := run(t, "net.gnmi.config", s, newRecorder(gnmitest.Secrets(s)), map[string]any{"delete": []any{"/network-instance[name=lab]"}})
	if err != nil || res.Changed || s.Sets() != 0 {
		t.Fatalf("delete of an absent path = %+v, %v after %d Sets", res, err, s.Sets())
	}
}

// TestConfig_RefusesBeforeSending makes each refusal the method makes before
// anything is sent.
func TestConfig_RefusesBeforeSending(t *testing.T) {
	s := gnmitest.New(t, gnmitest.Options{Username: login.Username, Password: login.Password, Encodings: []gpb.Encoding{gpb.Encoding_JSON_IETF}})
	for name, params := range map[string]map[string]any{
		"nothing":           {},
		"wildcard":          {"delete": []any{"/interface[name=*]"}},
		"bad path":          {"delete": []any{"/a//b"}},
		"bad prefix":        {"prefix": "x", "delete": []any{"/a"}},
		"no val":            {"update": []any{map[string]any{"path": "/a"}}},
		"no path":           {"update": []any{map[string]any{"val": 1}}},
		"extra key":         {"update": []any{map[string]any{"path": "/a", "val": 1, "op": "x"}}},
		"not a mapping":     {"update": []any{"/a"}},
		"not a list":        {"replace": "/a"},
		"masked value":      {"update": []any{map[string]any{"path": "/a", "val": "pleiades-masked-0123456789abcdef"}}},
		"malformed ref":     {"update": []any{map[string]any{"path": "/a", "val": "{{ secret.psk }}"}}},
		"ref in a key":      {"update": []any{map[string]any{"path": "/a", "val": map[string]any{"{{ secret.psk.value }}": 1}}}},
		"bad encoding":      {"encoding": "xml", "delete": []any{"/a"}},
		"unoffered":         {"encoding": "json", "delete": []any{"/a"}},
		"backup not a bool": {"backup": "yes", "delete": []any{"/a"}},
	} {
		if _, err := run(t, "net.gnmi.config", s, newRecorder(gnmitest.Secrets(s)), params); err == nil {
			t.Errorf("%s: nil, want a refusal", name)
		}
	}
	if s.Sets() != 0 {
		t.Errorf("%d Sets reached the server", s.Sets())
	}
}

// TestConfig_ANamedSecretIsResolvedAndMasked writes a pre-shared key from a
// named secret: the device gets the value, and nothing recorded holds it,
// even when the device echoes it back in its read.
func TestConfig_ANamedSecretIsResolvedAndMasked(t *testing.T) {
	const psk = "Ike-Preshared-91c4"
	s := gnmitest.New(t, login)
	rc := newRecorder(gnmitest.Secrets(s))
	rc.named = map[string]string{"ike.psk": psk}
	res, err := run(t, "net.gnmi.config", s, rc, map[string]any{
		"update": []any{map[string]any{"path": "/ipsec/peer[name=hq]", "val": map[string]any{"pre-shared-key": "{{ secret.ike.psk }}", "address": "192.0.2.1"}}},
		"backup": true,
	})
	if err != nil || !res.Changed {
		t.Fatalf("update = %+v, %v", res, err)
	}
	if v, _ := s.Value(t, "/ipsec/peer[name=hq]"); !strings.Contains(string(v.GetJsonIetfVal()), psk) {
		t.Fatalf("stored %s, want the resolved key", v.GetJsonIetfVal())
	}
	if all := rc.everything(t); strings.Contains(all, psk) {
		t.Errorf("the run recorded the key: %s", all)
	}
	if !slices.Contains(rc.registered, psk) {
		t.Error("the key did not join the run's masking set")
	}
}

// TestConfig_ARerunMasksTheSecretItReadsBack writes a named secret into a
// description, a leaf no shape masking reads as a secret, then runs the same
// task again with a backup: the read before now finds the secret on the
// device, and neither the backup nor the diff of the second run holds it.
func TestConfig_ARerunMasksTheSecretItReadsBack(t *testing.T) {
	const secret = "Desc-Secret-Rerun-58"
	s := gnmitest.New(t, login)
	params := map[string]any{
		"update": []any{map[string]any{"path": "/interface[name=lo8]/description", "val": "{{ secret.lab.description }}"}},
		"backup": true,
	}
	for i := range 2 {
		rc := newRecorder(gnmitest.Secrets(s))
		rc.named = map[string]string{"lab.description": secret}
		res, err := run(t, "net.gnmi.config", s, rc, params)
		if err != nil || res.Changed != (i == 0) {
			t.Fatalf("run %d = %+v, %v", i+1, res, err)
		}
		if all := rc.everything(t); strings.Contains(all, secret) {
			t.Errorf("run %d recorded the secret it read back: %s", i+1, all)
		}
		if i == 1 && !strings.Contains(fmt.Sprint(rc.stats["backup"]), "pleiades-masked-") {
			t.Errorf("run 2's backup = %v, want the description read back masked", rc.stats["backup"])
		}
	}
}

// TestConfig_ALostAnswerIsReadBack has the server apply a Set and never
// answer it, and holds the method to reading back, recording the undo of
// what it sees created, and failing the task saying so.
func TestConfig_ALostAnswerIsReadBack(t *testing.T) {
	opts := login
	opts.AfterSet = func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	s := gnmitest.New(t, opts)
	d, _ := collection.Lookup("net.gnmi.config")
	rc := newRecorder(gnmitest.Secrets(s))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := d.Invoke(ctx, rc, gnmitest.NewDevice(t, s), map[string]any{
		"update": []any{map[string]any{"path": "/interface[name=lo9]", "val": map[string]any{"name": "lo9"}}},
	})
	var lost interface{ Unwrap() error }
	if err == nil || !errors.As(err, &lost) || !strings.Contains(err.Error(), "so it was applied") {
		t.Fatalf("Invoke = %v, want the lost answer read back as applied", err)
	}
	if !res.Changed || inverse(rc) == nil {
		t.Errorf("result %+v, inverse %v, want the change and its undo kept", res, inverse(rc))
	}
}
