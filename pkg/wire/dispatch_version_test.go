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
		{body: `{"payload_version":2,"job_id":"j","a_field_this_build_does_not_know":true}`, wantErr: "it is version 2 and this runner reads up to 1"},
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
		case strings.Contains(tc.wantErr, "version 2") && !errors.Is(err, wire.ErrNewerPayload):
			t.Errorf("%s: error %v is not ErrNewerPayload", tc.body, err)
		}
	}
}

func TestADispatchPayloadCarriesItsVersionOnTheWire(t *testing.T) {
	b, err := json.Marshal(wire.DispatchPayload{PayloadVersion: wire.DispatchPayloadVersion, JobID: "j"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), `{"payload_version":1,`) {
		t.Errorf("wire form %s does not lead with payload_version 1", b)
	}
}
