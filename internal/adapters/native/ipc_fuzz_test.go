package native

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// FuzzDecodeChildResponse fuzzes the direction that is genuinely hostile:
// the parent Runner process decoding a frame written by a spawned child.
// Phase 42 intends that child to eventually be third-party code, so a
// child that emits garbage, floods the pipe, or truncates mid-frame must
// produce an ordinary error in the parent, never a panic that would take
// down a Runner serving every other in-flight job in the same process.
func FuzzDecodeChildResponse(f *testing.F) {
	valid, err := json.Marshal(&wire.ChildResponse{
		Changed: true,
		Facts:   map[string]any{"reply": "pong"},
	})
	if err != nil {
		f.Fatalf("marshaling seed: %v", err)
	}
	f.Add(string(valid))
	f.Add("")
	f.Add("{}")
	f.Add("null")
	f.Add("{\"changed\":\"not-a-bool\"}")
	f.Add("{\"facts\":[1,2,3]}")
	f.Add("{\"error\":\"boom\"}")
	f.Add(strings.Repeat("[", 128))

	f.Fuzz(func(t *testing.T, payload string) {
		var resp wire.ChildResponse
		if err := json.NewDecoder(strings.NewReader(payload)).Decode(&resp); err != nil {
			return
		}
		// A decoded response is consumed by invoke exactly this way, so
		// these are the accesses that must be panic-free for any input
		// that decoded at all.
		_ = resp.Changed
		_ = resp.Error
		_ = len(resp.Facts)
	})
}
