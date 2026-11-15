// Package restconf_test: net.restconf.config against a RESTCONF server over
// real TLS (restconftest) that stores what it is written, through the
// method's registered Invoke: each method's change read back, the undo
// recorded for a create alone, every refusal made before anything is sent,
// and a named secret resolved into the document and masked everywhere else.
package restconf_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf/restconftest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// groupPath is a NACM group whose name holds a slash and a comma, written as
// a person writes it, and groupStored the path the server must see.
const (
	groupPath   = "ietf-netconf-acm:nacm/groups/group=ops%2Fteam%2C1"
	groupStored = "/restconf/data/ietf-netconf-acm:nacm/groups/group=ops%2Fteam%2C1"
	groupDoc    = `{"ietf-netconf-acm:group": [{"name": "ops/team,1", "user-name": ["alice"]}]}`
)

// secretRecorder is a recorder whose run holds named secrets.
type secretRecorder struct {
	*recorder
	named      map[string]string
	registered []string
}

// NamedSecret implements sdk.NamedSecrets.
func (r *secretRecorder) NamedSecret(name, field string) (string, bool, error) {
	v, ok := r.named[name+"."+field]
	return v, ok, nil
}

// RegisterSecret implements sdk.SecretRegistrar.
func (r *secretRecorder) RegisterSecret(values ...string) {
	r.registered = append(r.registered, values...)
}

// configMethod is the registered net.restconf.config.
func configMethod(t *testing.T) collection.Descriptor {
	t.Helper()
	d, ok := collection.Lookup("net.restconf.config")
	if !ok {
		t.Fatal("net.restconf.config is not registered")
	}
	return d
}

// runConfig invokes the method against s with params.
func runConfig(t *testing.T, s *restconftest.Server, rc sdk.RunbookContext, params map[string]any) (collection.Result, error) {
	t.Helper()
	return configMethod(t).Invoke(context.Background(), rc, restconftest.NewDevice(t, s), params)
}

// inverse is the undo rc recorded, nil when none.
func inverse(rc *recorder) map[string]any {
	inv, _ := rc.stats[sdk.StatInverse].(map[string]any)
	return inv
}

// writes counts the requests s saw that were not GETs.
func writes(s *restconftest.Server) int {
	n := 0
	for _, m := range s.Methods() {
		if m != http.MethodGet {
			n++
		}
	}
	return n
}

func TestConfig_Registered(t *testing.T) {
	d := configMethod(t)
	m := d.Manifest
	if m.Status != collection.StatusImplemented || m.SupportsCheck || m.NoCheckReason == "" || !m.Reversibility.Reversible {
		t.Errorf("manifest = %+v, want an implemented, reversible method with no check and its reason", m)
	}
	if len(m.Reversibility.Inverses) != 1 || m.Reversibility.Inverses[0].FQCN != "net.restconf.config" || !slices.Contains(m.Reversibility.Inverses[0].Record, "path") || slices.Contains(m.Reversibility.Inverses[0].Record, "content") {
		t.Errorf("inverses = %+v, want its own delete, recording the path and never the content", m.Reversibility.Inverses)
	}
	for _, p := range m.Doc.Params {
		if p.Name == "content" && !p.SecretRefs {
			t.Error("content does not take secret references")
		}
	}
}

// TestConfig_PutCreatesThenChangesNothing: a put to an absent resource
// creates it, at the path the key's slash and comma stay inside, and
// records its exact undo; the same put again changes nothing and records no
// undo.
func TestConfig_PutCreatesThenChangesNothing(t *testing.T) {
	s := restconftest.New(t, login)
	params := map[string]any{"method": "put", "path": groupPath, "content": groupDoc}
	rc := newRecorder(restconftest.Secrets(s))
	res, err := runConfig(t, s, rc, params)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Stored(groupStored); !res.Changed || !ok {
		t.Fatalf("changed = %v, stored at %s = %v; the server saw %v", res.Changed, groupStored, ok, s.Paths())
	}
	inv := inverse(rc)
	params0, _ := inv["params"].(map[string]any)
	if inv["fqcn"] != "net.restconf.config" || params0["method"] != "delete" || params0["path"] != strings.TrimPrefix(groupStored, "/restconf/data/") || params0["content"] != nil {
		t.Errorf("inverse = %v, want a delete of the created path and nothing else", inv)
	}
	diff, _ := rc.stats[sdk.StatDiff].(map[string]any)
	before, _ := diff[sdk.DiffBefore].(map[string]any)
	after, _ := diff[sdk.DiffAfter].(map[string]any)
	if before["exists"] != false || after["exists"] != true || !strings.Contains(after["document"].(string), "alice") {
		t.Errorf("diff = %v, want absent before and the document after", diff)
	}

	again := newRecorder(restconftest.Secrets(s))
	res, err = runConfig(t, s, again, params)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed || inverse(again) != nil {
		t.Errorf("a re-run changed = %v with undo %v, want neither", res.Changed, inverse(again))
	}
}

// TestConfig_AReplaceOrMergeOfAnExistingResourceRecordsNoUndo: the earlier
// document is never journaled, so changing an existing resource records no
// undo, and a patch or put of something else reports the change.
func TestConfig_AReplaceOrMergeOfAnExistingResourceRecordsNoUndo(t *testing.T) {
	s := restconftest.New(t, login)
	s.Store(groupStored, "application/yang-data+json", []byte(groupDoc))
	for _, method := range []string{"patch", "put"} {
		rc := newRecorder(restconftest.Secrets(s))
		doc := strings.ReplaceAll(groupDoc, "alice", "bob-"+method)
		res, err := runConfig(t, s, rc, map[string]any{"method": method, "path": groupPath, "content": doc})
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		if !res.Changed || inverse(rc) != nil {
			t.Errorf("%s: changed = %v, undo %v; want changed and no undo", method, res.Changed, inverse(rc))
		}
	}
	// The same document in another key order and spacing is no change.
	rc := newRecorder(restconftest.Secrets(s))
	reordered := `{"ietf-netconf-acm:group":[{"user-name":["bob-put"],"name":"ops/team,1"}]}`
	s.Store(groupStored, "application/yang-data+json", []byte(reordered))
	res, err := runConfig(t, s, rc, map[string]any{"method": "patch", "path": groupPath, "content": strings.ReplaceAll(groupDoc, "alice", "bob-put")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed {
		t.Error("a document differing only in key order and white space read as a change")
	}
}

func TestConfig_DeleteRemovesAndAnAbsentResourceSendsNothing(t *testing.T) {
	s := restconftest.New(t, login)
	s.Store(groupStored, "application/yang-data+json", []byte(groupDoc))
	rc := newRecorder(restconftest.Secrets(s))
	res, err := runConfig(t, s, rc, map[string]any{"method": "delete", "path": groupPath})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Stored(groupStored); !res.Changed || ok || inverse(rc) != nil {
		t.Errorf("changed = %v, still stored = %v, undo %v; want removed with no undo", res.Changed, ok, inverse(rc))
	}
	sent := writes(s)
	res, err = runConfig(t, s, newRecorder(restconftest.Secrets(s)), map[string]any{"method": "delete", "path": groupPath})
	if err != nil || res.Changed || writes(s) != sent {
		t.Errorf("a second delete = %v, %v, %d writes; want unchanged with nothing sent", res.Changed, err, writes(s)-sent)
	}
}

func TestConfig_APatchOfAnAbsentResourceIsRefusedUnsent(t *testing.T) {
	s := restconftest.New(t, login)
	_, err := runConfig(t, s, newRecorder(restconftest.Secrets(s)), map[string]any{"method": "patch", "path": groupPath, "content": groupDoc})
	if err == nil || !strings.Contains(err.Error(), "nothing was sent") || writes(s) != 0 {
		t.Errorf("err = %v with %d writes, want the patch refused before sending", err, writes(s))
	}
}

// TestConfig_PostCreatesTheResourceItsLocationNames: the undo deletes the
// resource the answer's Location names; a post answered data-exists changes
// nothing and says so.
func TestConfig_PostCreatesTheResourceItsLocationNames(t *testing.T) {
	opts := login
	opts.PostChild = "group=ops"
	s := restconftest.New(t, opts)
	params := map[string]any{"path": "ietf-netconf-acm:nacm/groups", "content": `{"ietf-netconf-acm:group": [{"name": "ops"}]}`}
	rc := newRecorder(restconftest.Secrets(s))
	res, err := runConfig(t, s, rc, params)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := inverse(rc)["params"].(map[string]any)
	if !res.Changed || p["path"] != "ietf-netconf-acm:nacm/groups/group=ops" || p["method"] != "delete" {
		t.Errorf("changed = %v, undo %v; want the created resource's delete", res.Changed, inverse(rc))
	}
	again := newRecorder(restconftest.Secrets(s))
	res, err = runConfig(t, s, again, params)
	if err != nil || res.Changed || !strings.Contains(strings.Join(again.warnings(), "\n"), "post never replaces") {
		t.Errorf("a second post = %v, %v, warnings %v; want unchanged and said", res.Changed, err, again.warnings())
	}
}

// TestConfig_RefusesBeforeSendingAnything holds every refusal to sending no
// write at all.
func TestConfig_RefusesBeforeSendingAnything(t *testing.T) {
	s := restconftest.New(t, login)
	cases := map[string]map[string]any{
		"a query in the path":          {"method": "put", "path": groupPath + "?x=1", "content": groupDoc},
		"a dot segment":                {"method": "put", "path": "ietf-netconf-acm:nacm/../x", "content": groupDoc},
		"an unqualified first node":    {"method": "put", "path": "nacm/groups", "content": groupDoc},
		"an empty node":                {"method": "put", "path": "ietf-netconf-acm:nacm//groups", "content": groupDoc},
		"a raw space in a key":         {"method": "put", "path": "ietf-netconf-acm:nacm/groups/group=a b", "content": groupDoc},
		"a bad escape in a key":        {"method": "put", "path": "ietf-netconf-acm:nacm/groups/group=a%zz", "content": groupDoc},
		"an unknown method":            {"method": "merge", "path": groupPath, "content": groupDoc},
		"an unknown format":            {"method": "put", "path": groupPath, "content": groupDoc, "format": "yaml"},
		"content with delete":          {"method": "delete", "path": groupPath, "content": groupDoc},
		"no content for put":           {"method": "put", "path": groupPath},
		"content that is not JSON":     {"method": "put", "path": groupPath, "content": "{"},
		"a reference outside a string": {"method": "put", "path": groupPath, "content": `{"a": {{ secret.k.v }}}`},
		"a DOCTYPE":                    {"method": "put", "path": groupPath, "format": "xml", "content": `<!DOCTYPE x [<!ENTITY e "x">]><group xmlns="urn:ietf:params:xml:ns:yang:ietf-netconf-acm"/>`},
		"XML not well-formed":          {"method": "put", "path": groupPath, "format": "xml", "content": `<group>`},
		"a masked placeholder":         {"method": "put", "path": groupPath, "content": `{"k": "pleiades-masked-0123456789abcdef"}`},
		"the candidate datastore":      {"method": "put", "path": groupPath, "content": groupDoc, "datastore": "candidate"},
		"a datastore not listed":       {"method": "put", "path": groupPath, "content": groupDoc, "datastore": "startup"},
		"a module not implemented":     {"method": "put", "path": "no-such-module:x", "content": groupDoc},
		"no path":                      {"method": "put", "content": groupDoc},
		"a backup that is not a bool":  {"method": "put", "path": groupPath, "content": groupDoc, "backup": "yes"},
	}
	for name, params := range cases {
		if _, err := runConfig(t, s, newRecorder(restconftest.Secrets(s)), params); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if writes(s) != 0 {
		t.Errorf("%d writes reached the device, want none", writes(s))
	}
}

// TestConfig_ANamedSecretIsWrittenAndMaskedEverywhereElse: the reference is
// resolved into the document the device stores, escaped as JSON string
// content, and appears in no stat, diff or warning.
func TestConfig_ANamedSecretIsWrittenAndMaskedEverywhereElse(t *testing.T) {
	s := restconftest.New(t, login)
	const key = `Ike"Key\2026`
	rc := &secretRecorder{recorder: newRecorder(restconftest.Secrets(s)), named: map[string]string{"ike.key": key}}
	content := `{"ietf-netconf-acm:group": [{"name": "ops/team,1", "user-name": ["{{ secret.ike.key }}"]}]}`
	res, err := configMethod(t).Invoke(context.Background(), rc, restconftest.NewDevice(t, s), map[string]any{"method": "put", "path": groupPath, "content": content, "backup": true})
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := s.Stored(groupStored)
	var decoded struct {
		Group []struct {
			UserName []string `json:"user-name"`
		} `json:"ietf-netconf-acm:group"`
	}
	if err := json.Unmarshal(stored, &decoded); err != nil || len(decoded.Group) != 1 || decoded.Group[0].UserName[0] != key || !res.Changed {
		t.Fatalf("the device stored %s (%v), want the secret resolved as JSON string content", stored, err)
	}
	recorded, _ := json.Marshal(rc.stats)
	escaped, _ := json.Marshal(key)
	if strings.Contains(string(recorded), key) || strings.Contains(string(recorded), string(escaped[1:len(escaped)-1])) {
		t.Errorf("a stat carries the secret: %s", recorded)
	}
	if !slices.Contains(rc.registered, string(escaped[1:len(escaped)-1])) {
		t.Errorf("registered %v, want the escaped form in the run's masking set", rc.registered)
	}
}

func TestConfig_AReferenceThatDoesNotResolveSendsNothing(t *testing.T) {
	s := restconftest.New(t, login)
	rc := &secretRecorder{recorder: newRecorder(restconftest.Secrets(s)), named: map[string]string{}}
	content := `{"ietf-netconf-acm:group": [{"name": "{{ secret.missing.key }}"}]}`
	if _, err := configMethod(t).Invoke(context.Background(), rc, restconftest.NewDevice(t, s), map[string]any{"method": "put", "path": groupPath, "content": content}); err == nil || writes(s) != 0 {
		t.Errorf("err = %v with %d writes, want refused unsent", err, writes(s))
	}
	plain := newRecorder(restconftest.Secrets(s))
	if _, err := runConfig(t, s, plain, map[string]any{"method": "put", "path": groupPath, "content": content}); err == nil || writes(s) != 0 {
		t.Errorf("a run with no named secrets = %v with %d writes, want refused unsent", err, writes(s))
	}
}

// TestConfig_XMLAndARefusedWriteComeBackStructuredAndMasked writes XML, then
// a write the device refuses: its error is the device's own, with the echoed
// credential masked.
func TestConfig_XMLAndARefusedWriteComeBackStructuredAndMasked(t *testing.T) {
	opts := login
	opts.Override = map[string]http.HandlerFunc{"/restconf/data/ietf-netconf-acm:nacm/groups/group=denied": func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/yang-data+xml")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`<errors xmlns="urn:ietf:params:xml:ns:yang:ietf-restconf"><error><error-type>application</error-type><error-tag>invalid-value</error-tag><error-message>rejected for ` + r.Header.Get("Authorization") + `</error-message></error></errors>`))
	}}
	s := restconftest.New(t, opts)
	xmlDoc := `<group xmlns="urn:ietf:params:xml:ns:yang:ietf-netconf-acm"><name>ops/team,1</name></group>`
	res, err := runConfig(t, s, newRecorder(restconftest.Secrets(s)), map[string]any{"method": "put", "format": "xml", "path": groupPath, "content": xmlDoc})
	if err != nil || !res.Changed {
		t.Fatalf("an XML put = %v, %v", res.Changed, err)
	}
	_, err = runConfig(t, s, newRecorder(restconftest.Secrets(s)), map[string]any{"method": "put", "format": "xml", "path": "ietf-netconf-acm:nacm/groups/group=denied", "content": xmlDoc})
	if err == nil || !strings.Contains(err.Error(), "invalid-value") || strings.Contains(err.Error(), "Basic ") {
		t.Errorf("err = %v, want the device's invalid-value with the echoed header masked", err)
	}
}

// TestConfig_AnXMLReRunAndTheRunningDatastore: an XML put written twice
// changes once, and a write to the running datastore RFC 8527 addresses goes
// under /ds/ and records its undo there.
func TestConfig_AnXMLReRunAndTheRunningDatastore(t *testing.T) {
	s := restconftest.New(t, login)
	xmlDoc := `<group xmlns="urn:ietf:params:xml:ns:yang:ietf-netconf-acm"><name>ops/team,1</name></group>`
	params := map[string]any{"method": "put", "format": "xml", "path": groupPath, "content": xmlDoc, "datastore": "running"}
	rc := newRecorder(restconftest.Secrets(s))
	if res, err := runConfig(t, s, rc, params); err != nil || !res.Changed {
		t.Fatalf("first put = %v, %v", res.Changed, err)
	}
	if _, ok := s.Stored("/restconf/ds/ietf-datastores:running/ietf-netconf-acm:nacm/groups/group=ops%2Fteam%2C1"); !ok {
		t.Errorf("nothing was written under /ds/; the server saw %v", s.Paths())
	}
	p, _ := inverse(rc)["params"].(map[string]any)
	if p["datastore"] != "running" || p["format"] != "xml" {
		t.Errorf("undo %v, want it on the same datastore and format", inverse(rc))
	}
	if res, err := runConfig(t, s, newRecorder(restconftest.Secrets(s)), params); err != nil || res.Changed {
		t.Errorf("the same XML put again = %v, %v; want no change", res.Changed, err)
	}
}

// TestConfig_AWriteGoesOnWhenTheDiscoveryLearnedNothing: the checks the
// discovery feeds are skipped, the write is made, and nothing is recorded
// as a discovery fact.
func TestConfig_AWriteGoesOnWhenTheDiscoveryLearnedNothing(t *testing.T) {
	notFound := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }
	opts := login
	opts.Override = map[string]http.HandlerFunc{
		"/restconf/data/ietf-yang-library:yang-library":                       notFound,
		"/restconf/data/ietf-yang-library:modules-state":                      notFound,
		"/restconf/data/ietf-restconf-monitoring:restconf-state/capabilities": notFound,
	}
	s := restconftest.New(t, opts)
	rc := newRecorder(restconftest.Secrets(s))
	res, err := runConfig(t, s, rc, map[string]any{"method": "put", "path": groupPath, "content": groupDoc})
	if err != nil || !res.Changed {
		t.Fatalf("put = %v, %v", res.Changed, err)
	}
	if len(rc.facts) != 0 || !strings.Contains(strings.Join(rc.warnings(), "\n"), "no discovery fact is recorded") {
		t.Errorf("facts %v, warnings %v; want none and the reason", rc.facts, rc.warnings())
	}
}

// TestConfig_ARefusedCredentialIsSentOnce: a wrong password ends the task
// at the first refusal, before any write.
func TestConfig_ARefusedCredentialIsSentOnce(t *testing.T) {
	s := restconftest.New(t, login)
	rc := newRecorder(map[string]string{"username": "admin", "password": "not-the-password"})
	if _, err := runConfig(t, s, rc, map[string]any{"method": "put", "path": groupPath, "content": groupDoc}); err == nil || !strings.Contains(err.Error(), "refused the credential") {
		t.Fatalf("err = %v, want the credential refused", err)
	}
	refused := 0
	for _, p := range s.Paths() {
		if p != "/.well-known/host-meta" {
			refused++
		}
	}
	if refused != 1 || writes(s) != 0 {
		t.Errorf("%d reads past the root and %d writes, want one refused read and no write", refused, writes(s))
	}
}
