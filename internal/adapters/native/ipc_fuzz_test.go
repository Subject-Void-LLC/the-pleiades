package native

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// FuzzReadChildRequest fuzzes the child's own inbound frame decoder. This
// is a real trust boundary in the direction that matters least (the parent
// writes it), but it is fuzzed anyway for a specific reason: the same
// decoder is what a future Phase 42 third-party Collection binary would
// use, and at that point the writer is no longer necessarily this
// codebase. The property is only that a hostile or truncated frame is
// refused as an error and never panics the process.
func FuzzReadChildRequest(f *testing.F) {
	valid, err := json.Marshal(&wire.ChildRequest{
		FQCN:    "some.method",
		JobID:   "job-1",
		Params:  map[string]any{"message": "hello"},
		Secrets: map[string]string{"username": "u"},
	})
	if err != nil {
		f.Fatalf("marshaling seed: %v", err)
	}
	f.Add(string(valid))
	f.Add("")
	f.Add("{}")
	f.Add("null")
	f.Add("[]")
	f.Add("{\"fqcn\":")
	f.Add("{\"params\":{\"a\":{\"b\":{\"c\":1}}}}")
	f.Add("{\"secrets\":{\"k\":123}}")
	f.Add(strings.Repeat("{", 128))

	f.Fuzz(func(t *testing.T, payload string) {
		// The only contract: return a request or an error, never panic and
		// never hang. A malformed frame must not yield a usable request.
		req, err := readChildRequest(strings.NewReader(payload))
		if err != nil {
			return
		}
		// If it decoded, the value must be self-consistent enough to use
		// without a nil-map panic downstream in invokeChild.
		_ = req.FQCN
		_ = len(req.Params)
		_ = len(req.Secrets)
		_ = len(req.Capabilities)
	})
}

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
