// Package native: a rollback dispatch's node id and the Collection group
// its steps recorded reach the job's log escaped (rollback.Printable, and
// the skew note's quoting), never raw, whatever the dispatch carried.
package native

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

func TestAdapter_Execute_PrintsADispatchedNameEscapedInTheJobLog(t *testing.T) {
	ran, id, _ := skewFixture(t)
	override := string(rune(0x202e))
	odd := id
	odd.Group, odd.Version, odd.Manifests = "fi"+override+"le", "0.3.9", wire.ManifestDigestPrefix+strings.Repeat("a", 64)
	runbook := "id: ran\ntasks:\n  - name: make it\n    file.rbmake:\n      path: /tmp/made\n"
	for name, step := range map[string]wire.RollbackStep{
		"a node id the runbook lacks": {Node: "tasks[0]" + override, Emitter: "file.rbmake", Method: "file.rbremove", Params: map[string]any{"path": "/tmp/made"},
			Name: "undo", Source: "recorded"},
		"a group in an accepted skew": {Node: "tasks[0]", Emitter: "file.rbmake", Method: "file.rbremove", Params: map[string]any{"path": "/tmp/made"},
			Name: "undo", Source: "recorded", Collection: odd, UndoDigest: wire.ManifestDigestPrefix + strings.Repeat("b", 64), SkewAccepted: true},
	} {
		t.Run(name, func(t *testing.T) {
			bus := &mockBus{}
			adapter, err := NewAdapter(bus, writeRunbook(t, "ran", runbook), slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			_, _ = adapter.Execute(context.Background(), wire.DispatchPayload{JobID: "job-1", RunbookID: "ran", DeviceName: "web1", DeviceHost: "10.0.0.1",
				Rollback: &wire.Rollback{Of: "job-0", DAGVersion: ran.Version, Steps: []wire.RollbackStep{step}}})
			events := bus.logEvents("job-1")
			if len(events) == 0 {
				t.Fatal("the job's log carried nothing")
			}
			saidIt := false
			for _, evt := range events {
				var je wire.JobEvent
				if err := json.Unmarshal(evt.Data, &je); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(je.EventData.Message, override) {
					t.Errorf("a %s event holds the name raw: %q", je.Task, je.EventData.Message)
				}
				saidIt = saidIt || strings.Contains(je.EventData.Message, `\u202e`)
			}
			if !saidIt {
				t.Error("no event of the job's log names the escaped character, so what was there is not shown")
			}
		})
	}
}
