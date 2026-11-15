// Package restconf_test: whether a write created its resource is the
// device's own answer (RFC 8040 section 4.5: a put answers 201 Created for a
// create and 204 No Content for a replace; section 4.4.1: a post answers 201
// with the new resource's Location), never a read before it that found
// nothing, and the undo of a create is recorded as its 201 arrives. A write
// to a conventional root nothing confirmed is refused.
package restconf_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/restconf/restconftest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// hidden answers every read of the resource 404 (access control hiding it,
// or a device that reads nothing back) and every put with status.
func hidden(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(status)
	}
}

// TestConfig_APutAnswered201IsACreateWhateverTheReads: a put answered 201
// Created is a create, changed and undone exactly, even when neither read
// found the resource; it used to report no change, so a rollback, which plans
// only changed tasks, never ran its undo.
func TestConfig_APutAnswered201IsACreateWhateverTheReads(t *testing.T) {
	opts := login
	opts.Override = map[string]http.HandlerFunc{groupStored: hidden(http.StatusCreated)}
	s := restconftest.New(t, opts)
	rc := newRecorder(restconftest.Secrets(s))
	res, err := runConfig(t, s, rc, map[string]any{"method": "put", "path": groupPath, "content": groupDoc})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed || inverse(rc) == nil {
		t.Errorf("changed = %v, undo %v; want a change with its delete recorded", res.Changed, inverse(rc))
	}
}

// TestConfig_APutAnswered204IsAReplaceWhateverTheReads: a put answered 204
// No Content replaced a resource that was there, even when the read before
// found nothing (access control hiding it), so no delete is recorded: it
// used to record one, which would have removed what the run did not make.
func TestConfig_APutAnswered204IsAReplaceWhateverTheReads(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusOK} {
		opts := login
		opts.Override = map[string]http.HandlerFunc{groupStored: hidden(status)}
		s := restconftest.New(t, opts)
		rc := newRecorder(restconftest.Secrets(s))
		if _, err := runConfig(t, s, rc, map[string]any{"method": "put", "path": groupPath, "content": groupDoc}); err != nil {
			t.Fatal(err)
		}
		if inverse(rc) != nil || !strings.Contains(strings.Join(rc.warnings(), "\n"), "not 201 Created") {
			t.Errorf("%d: undo %v, warnings %v; want no undo and the reason", status, inverse(rc), rc.warnings())
		}
	}
}

// TestConfig_APostUndoesOnlyTheChildItsLocationNames: a Location naming the
// post's own path, a sibling or a grandchild is no created resource, so no
// delete of it is recorded; it used to delete whatever the Location named.
func TestConfig_APostUndoesOnlyTheChildItsLocationNames(t *testing.T) {
	for name, location := range map[string]string{
		"the parent":     "/restconf/data/ietf-netconf-acm:nacm/groups",
		"a sibling":      "/restconf/data/ietf-netconf-acm:nacm/rule-list=ops",
		"a grandchild":   "/restconf/data/ietf-netconf-acm:nacm/groups/group=ops/user-name=alice",
		"another module": "/restconf/data/other:nacm/groups/group=ops",
	} {
		opts := login
		opts.Override = map[string]http.HandlerFunc{"/restconf/data/ietf-netconf-acm:nacm/groups": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", location)
			w.WriteHeader(http.StatusCreated)
		}}
		s := restconftest.New(t, opts)
		rc := newRecorder(restconftest.Secrets(s))
		res, err := runConfig(t, s, rc, map[string]any{"path": "ietf-netconf-acm:nacm/groups", "content": `{"ietf-netconf-acm:group": [{"name": "ops"}]}`})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !res.Changed || inverse(rc) != nil || !strings.Contains(strings.Join(rc.warnings(), "\n"), "directly under path") {
			t.Errorf("%s: changed = %v, undo %v, warnings %v; want a change, no undo and the reason", name, res.Changed, inverse(rc), rc.warnings())
		}
	}
}

// TestConfig_TheUndoOfACreateStandsWhenItsAnswerIsCutShort: the 201 arrives
// and its body never does; the delete is recorded all the same, and the
// failure says what is not known.
func TestConfig_TheUndoOfACreateStandsWhenItsAnswerIsCutShort(t *testing.T) {
	opts := login
	opts.Override = map[string]http.HandlerFunc{groupStored: func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(64))
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("{"))
		// The handler returns with 63 bytes owed: the client's read of the
		// body fails.
	}}
	s := restconftest.New(t, opts)
	rc := newRecorder(restconftest.Secrets(s))
	_, err := runConfig(t, s, rc, map[string]any{"method": "put", "path": groupPath, "content": groupDoc})
	if err == nil || !strings.Contains(err.Error(), "not known") {
		t.Fatalf("err = %v, want the answer lost and the effect unknown", err)
	}
	if p, _ := inverse(rc)["params"].(map[string]any); p["method"] != "delete" || inverse(rc)[sdk.InverseKeepOnFailureKey] != true {
		t.Errorf("undo %v, want the delete recorded and kept on failure", inverse(rc))
	}
}

// TestConfig_AWriteToAnUnconfirmedRootIsRefused: host-meta names no root,
// and neither the capabilities nor the YANG library answer under the
// conventional /restconf, so nothing is written there.
func TestConfig_AWriteToAnUnconfirmedRootIsRefused(t *testing.T) {
	broken := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }
	opts := login
	opts.Override = map[string]http.HandlerFunc{
		"/.well-known/host-meta":                                              func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) },
		"/restconf/data/ietf-yang-library:yang-library":                       broken,
		"/restconf/data/ietf-yang-library:modules-state":                      broken,
		"/restconf/data/ietf-restconf-monitoring:restconf-state/capabilities": broken,
	}
	s := restconftest.New(t, opts)
	_, err := runConfig(t, s, newRecorder(restconftest.Secrets(s)), map[string]any{"method": "put", "path": groupPath, "content": groupDoc})
	if err == nil || !strings.Contains(err.Error(), "conventional /restconf") || writes(s) != 0 {
		t.Errorf("err = %v with %d writes, want the write refused unsent", err, writes(s))
	}
}
