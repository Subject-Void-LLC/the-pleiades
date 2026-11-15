// Package restconflive: net.restconf.config's whole lifecycle against
// rousette, through the method's registered Invoke: create, a re-run that
// changes nothing, a merge, a replace, a named secret, a delete and a delete
// of what is gone, a post and its undo run as recorded, each read back over
// RESTCONF and, for the create, over the same server's NETCONF.
package restconflive

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/Subject-Void-LLC/the-pleiades/internal/catalog/net/restconf" // registers net.restconf.config
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/datastore"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf/restconftest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// liveRecorder is a task's context: rousette's login, a named secret, and
// what the method records.
type liveRecorder struct {
	mu    sync.Mutex
	stats map[string]any
	named map[string]string
}

// InjectSecrets implements sdk.RunbookContext.
func (r *liveRecorder) InjectSecrets() map[string]string {
	return map[string]string{wire.SecretUsername: notconfUser, wire.SecretPassword: notconfPassword}
}

// SetStat implements sdk.RunbookContext.
func (r *liveRecorder) SetStat(key string, value any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stats[key] = value
	return nil
}

// EmitFact implements sdk.RunbookContext.
func (r *liveRecorder) EmitFact(string, any) error { return nil }

// NamedSecret implements sdk.NamedSecrets.
func (r *liveRecorder) NamedSecret(name, field string) (string, bool, error) {
	v, ok := r.named[name+"."+field]
	return v, ok, nil
}

// liveDevice is rousette as a RESTCONF device at its plain-HTTP origin.
func liveDevice(s server) *restconftest.Device {
	return &restconftest.Device{
		Stub: &inventorytest.Stub{StubName: "notconf", Caps: []capability.Name{capability.NameRestconf}},
		Base: s.origin, Auth: "basic", AllowPlaintext: true,
	}
}

// configure runs net.restconf.config once and returns the result and the
// recorder.
func configure(t *testing.T, s server, params map[string]any) (collection.Result, *liveRecorder, error) {
	t.Helper()
	d, ok := collection.Lookup("net.restconf.config")
	if !ok {
		t.Fatal("net.restconf.config is not registered")
	}
	rc := &liveRecorder{stats: map[string]any{}, named: map[string]string{"lab.user": `ops"admin`}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	res, err := d.Invoke(ctx, rc, liveDevice(s), params)
	return res, rc, err
}

// groupUsers reads the group's user names over RESTCONF, nil when the group
// is not there.
func groupUsers(t *testing.T, s server, name string) []string {
	t.Helper()
	c := client(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := c.DiscoverRoot(ctx); err != nil {
		t.Fatal(err)
	}
	body, found, err := c.Read(ctx, "", restconf.Path{restconf.Node("ietf-netconf-acm", "nacm"), restconf.Node("", "groups"), restconf.Node("", "group").Entry(name)}, restconf.MediaTypeJSON)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		return nil
	}
	var doc struct {
		NACM struct {
			Groups struct {
				Group []struct {
					UserName []string `json:"user-name"`
				} `json:"group"`
			} `json:"groups"`
		} `json:"ietf-netconf-acm:nacm"`
	}
	if err := json.Unmarshal(body, &doc); err != nil || len(doc.NACM.Groups.Group) != 1 {
		t.Fatalf("the group's answer %s (%v) holds no one group", body, err)
	}
	return doc.NACM.Groups.Group[0].UserName
}

// group is a NACM group document holding users.
func group(name string, users ...string) string {
	doc, _ := json.Marshal(map[string]any{"ietf-netconf-acm:group": []any{map[string]any{"name": name, "user-name": users}}})
	return string(doc)
}

func TestLive_ConfigLifecycle(t *testing.T) {
	s := start(t)
	const name = "life/cycle,1"
	const path = "ietf-netconf-acm:nacm/groups/group=life%2Fcycle%2C1"
	t.Cleanup(func() { _, _, _ = configure(t, s, map[string]any{"method": "delete", "path": path}) })

	res, rc, err := configure(t, s, map[string]any{"method": "put", "path": path, "content": group(name, "alice")})
	if err != nil || !res.Changed {
		t.Fatalf("create = %v, %v", res.Changed, err)
	}
	inv, _ := rc.stats[sdk.StatInverse].(map[string]any)
	if p, _ := inv["params"].(map[string]any); p["path"] != path || p["method"] != "delete" {
		t.Errorf("the create's undo is %v, want a delete of %s", inv, path)
	}
	if got := groupUsers(t, s, name); strings.Join(got, ",") != "alice" {
		t.Errorf("after the create the group holds %v", got)
	}
	// Read over NETCONF too: one datastore, whichever protocol wrote it.
	ns := session(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	reply, err := ns.GetConfig(ctx, datastore.Path{})
	if err != nil || !strings.Contains(string(reply.Bytes), "<name>"+name+"</name>") {
		t.Errorf("NETCONF reads %s (%v), want the group RESTCONF created", reply.Bytes, err)
	}

	if res, rc, err = configure(t, s, map[string]any{"method": "put", "path": path, "content": group(name, "alice")}); err != nil || res.Changed || rc.stats[sdk.StatInverse] != nil {
		t.Errorf("the same put again = %v, %v, undo %v; want no change and no undo", res.Changed, err, rc.stats[sdk.StatInverse])
	}
	if res, rc, err = configure(t, s, map[string]any{"method": "patch", "path": path, "content": group(name, "bob")}); err != nil || !res.Changed || rc.stats[sdk.StatInverse] != nil {
		t.Errorf("a merge = %v, %v, undo %v; want a change and no undo", res.Changed, err, rc.stats[sdk.StatInverse])
	}
	if got := groupUsers(t, s, name); strings.Join(got, ",") != "alice,bob" {
		t.Errorf("after the merge the group holds %v, want alice and bob", got)
	}
	if res, _, err = configure(t, s, map[string]any{"method": "put", "path": path, "content": group(name, "{{ secret.lab.user }}"), "backup": true}); err != nil || !res.Changed {
		t.Fatalf("a replace with a named secret = %v, %v", res.Changed, err)
	}
	if got := groupUsers(t, s, name); strings.Join(got, ",") != `ops"admin` {
		t.Errorf("after the replace the group holds %v, want the secret's value alone", got)
	}
	if res, _, err = configure(t, s, map[string]any{"method": "delete", "path": path}); err != nil || !res.Changed {
		t.Errorf("a delete = %v, %v", res.Changed, err)
	}
	if got := groupUsers(t, s, name); got != nil {
		t.Errorf("after the delete the group holds %v", got)
	}
	if res, _, err = configure(t, s, map[string]any{"method": "delete", "path": path}); err != nil || res.Changed {
		t.Errorf("a delete of what is gone = %v, %v; want no change", res.Changed, err)
	}
}

// TestLive_ASecretNeverReachesAStat replaces a group with a named secret's
// value as its user name and reads every stat for it.
func TestLive_ASecretNeverReachesAStat(t *testing.T) {
	s := start(t)
	const path = "ietf-netconf-acm:nacm/groups/group=secret-holder"
	t.Cleanup(func() { _, _, _ = configure(t, s, map[string]any{"method": "delete", "path": path}) })
	_, rc, err := configure(t, s, map[string]any{"method": "put", "path": path, "content": group("secret-holder", "{{ secret.lab.user }}"), "backup": true})
	if err != nil {
		t.Fatal(err)
	}
	stats, _ := json.Marshal(rc.stats)
	if strings.Contains(string(stats), `ops\"admin`) || strings.Contains(string(stats), `ops"admin`) {
		t.Errorf("a stat carries the secret: %s", stats)
	}
}

// TestLive_PostAndItsConflict: rousette creates the resource a post sends
// (naming no Location, so no undo is recorded and the run says so), and
// answers the same post again with 409 resource-denied, RFC 8040 section
// 4.4.1's tag, which changes nothing.
func TestLive_PostAndItsConflict(t *testing.T) {
	s := start(t)
	t.Cleanup(func() {
		_, _, _ = configure(t, s, map[string]any{"method": "delete", "path": "ietf-netconf-acm:nacm/groups/group=posted"})
	})
	params := map[string]any{"path": "ietf-netconf-acm:nacm/groups", "content": group("posted", "carol")}
	res, rc, err := configure(t, s, params)
	if err != nil || !res.Changed {
		t.Fatalf("post = %v, %v", res.Changed, err)
	}
	warnings, _ := rc.stats[sdk.StatWarnings].([]string)
	if rc.stats[sdk.StatInverse] != nil || !strings.Contains(strings.Join(warnings, "\n"), "no Location") {
		t.Errorf("post recorded undo %v with warnings %v; want none, and the missing Location said", rc.stats[sdk.StatInverse], warnings)
	}
	if got := groupUsers(t, s, "posted"); strings.Join(got, ",") != "carol" {
		t.Errorf("after the post the group holds %v", got)
	}
	res, rc, err = configure(t, s, params)
	warnings, _ = rc.stats[sdk.StatWarnings].([]string)
	if err != nil || res.Changed || !strings.Contains(strings.Join(warnings, "\n"), "post never replaces") {
		t.Errorf("the same post again = %v, %v, warnings %v; want no change, said", res.Changed, err, warnings)
	}
}

// TestLive_TheCreatesUndoUndoesIt runs the undo a create recorded, as a
// rollback would, with the params as recorded.
func TestLive_TheCreatesUndoUndoesIt(t *testing.T) {
	s := start(t)
	const path = "ietf-netconf-acm:nacm/groups/group=undo%2Fme"
	_, rc, err := configure(t, s, map[string]any{"method": "put", "path": path, "content": group("undo/me", "dave")})
	if err != nil {
		t.Fatal(err)
	}
	inv, _ := rc.stats[sdk.StatInverse].(map[string]any)
	params, _ := inv["params"].(map[string]any)
	if params == nil || inv["fqcn"] != "net.restconf.config" {
		t.Fatalf("no undo was recorded: %v", rc.stats)
	}
	if res, _, err := configure(t, s, params); err != nil || !res.Changed {
		t.Fatalf("the undo = %v, %v", res.Changed, err)
	}
	if got := groupUsers(t, s, "undo/me"); got != nil {
		t.Errorf("after the undo the group holds %v, want it gone", got)
	}
}
