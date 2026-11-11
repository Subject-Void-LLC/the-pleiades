// Package wire_test: tests of the dispatch payload's version.
package wire_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

func TestDispatchPayloadVersionIsReadAndRefusedByName(t *testing.T) {
	for _, tc := range []struct {
		body    string
		wantErr string
	}{
		{body: `{"job_id":"j"}`},
		{body: `{"payload_version":1,"job_id":"j"}`},
		{body: `{"payload_version":2,"job_id":"j","graph_scheme":"graph/1","graph_digest":"sha256:00"}`},
		{body: `{"payload_version":3,"job_id":"j","a_field_this_build_does_not_know":true}`, wantErr: "it is version 3 and this runner reads up to 2"},
		{body: `{"payload_version":-1}`, wantErr: "not a payload version"},
	} {
		var p wire.DispatchPayload
		if err := json.Unmarshal([]byte(tc.body), &p); err != nil {
			t.Fatalf("%s: %v", tc.body, err)
		}
		err := p.CheckVersion()
		switch {
		case tc.wantErr == "" && err != nil:
			t.Errorf("%s: refused: %v", tc.body, err)
		case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
			t.Errorf("%s: error %v, want %q", tc.body, err, tc.wantErr)
		case strings.Contains(tc.wantErr, "version 3") && !errors.Is(err, wire.ErrNewerPayload):
			t.Errorf("%s: error %v is not ErrNewerPayload", tc.body, err)
		}
	}
}

func TestADispatchPayloadCarriesItsVersionOnTheWire(t *testing.T) {
	b, err := json.Marshal(wire.DispatchPayload{PayloadVersion: wire.DispatchPayloadVersion, JobID: "j"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), `{"payload_version":2,`) {
		t.Errorf("wire form %s does not lead with payload_version 2", b)
	}
}

// TestPayloadVersionFor: a dispatch holding its Runner to a pinned graph is
// written at the version whose Runners make that check, so a Runner of an
// earlier release refuses it by version (CheckVersion, at that release)
// rather than running it unheld; every other dispatch, a rollback's and a
// playbook's among them, stays at version 1, which every Runner reads.
func TestPayloadVersionFor(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    wire.DispatchPayload
		want int
	}{
		{"a pinned native dispatch", wire.DispatchPayload{GraphScheme: "graph/1", GraphDigest: "sha256:00"}, wire.PinnedGraphPayloadVersion},
		{"a digest alone", wire.DispatchPayload{GraphDigest: "sha256:00"}, wire.PinnedGraphPayloadVersion},
		{"an unpinned native dispatch", wire.DispatchPayload{RunbookID: "web"}, 1},
		{"a rollback", wire.DispatchPayload{Rollback: &wire.Rollback{Of: "j", DAGVersion: "v"}}, 1},
		{"a playbook", wire.DispatchPayload{Kind: "playbook"}, 1},
	} {
		if got := wire.PayloadVersionFor(tc.p); got != tc.want {
			t.Errorf("%s: version %d, want %d", tc.name, got, tc.want)
		}
	}
	// An earlier Runner reads up to version 1, so it refuses the pinned one.
	pinned := wire.DispatchPayload{PayloadVersion: wire.PinnedGraphPayloadVersion}
	if wire.PinnedGraphPayloadVersion <= 1 || wire.PinnedGraphPayloadVersion > wire.DispatchPayloadVersion || pinned.CheckVersion() != nil {
		t.Errorf("version %d is not past 1 and readable here", wire.PinnedGraphPayloadVersion)
	}
}
