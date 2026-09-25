// Tests for the session child's request loop, over buffers.
package native

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// sessionRequests encodes reqs as one stream.
func sessionRequests(t testing.TB, reqs ...wire.ChildRequest) []byte {
	t.Helper()
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	for _, r := range reqs {
		if err := enc.Encode(&r); err != nil {
			t.Fatal(err)
		}
	}
	return b.Bytes()
}

// TestSessionChild_AnswersEachRequestInOrder proves one child answers a
// stream of requests, a method's failure included, one response each and
// in order, and exits 0 at the end of its input.
func TestSessionChild_AnswersEachRequestInOrder(t *testing.T) {
	in := sessionRequests(t,
		wire.ChildRequest{FQCN: nativeIPCEchoMethodName, Mode: string(collection.ModeExecute), Params: map[string]any{"message": "one"}},
		wire.ChildRequest{FQCN: "nativesessiontest.missing", Mode: string(collection.ModeExecute)},
		wire.ChildRequest{FQCN: nativeIPCEchoMethodName, Mode: string(collection.ModeExecute), Params: map[string]any{"message": "two"}},
	)
	var out, errOut bytes.Buffer
	if code := runCollectionSession(context.Background(), bytes.NewReader(in), &out, &errOut); code != 0 {
		t.Fatalf("exit %d, stderr %s", code, errOut.String())
	}
	dec := json.NewDecoder(&out)
	var got []wire.ChildResponse
	for {
		var r wire.ChildResponse
		if err := dec.Decode(&r); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	if len(got) != 3 {
		t.Fatalf("%d responses, want 3", len(got))
	}
	if got[0].Facts["echoed_param"] != "one" || got[2].Facts["echoed_param"] != "two" {
		t.Errorf("responses out of order: %+v", got)
	}
	if !strings.Contains(got[1].Error, "not registered") {
		t.Errorf("the unknown method answered %+v, want its refusal", got[1])
	}
}

// TestSessionChild_BrokenInputExitsNonZero proves a stream that is not
// requests ends the session as a broken exchange, after answering what
// came before it.
func TestSessionChild_BrokenInputExitsNonZero(t *testing.T) {
	in := append(sessionRequests(t, wire.ChildRequest{FQCN: nativeIPCEchoMethodName, Mode: string(collection.ModeExecute)}), []byte("{not json")...)
	var out, errOut bytes.Buffer
	if code := runCollectionSession(context.Background(), bytes.NewReader(in), &out, &errOut); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if strings.Count(out.String(), "\n") != 1 {
		t.Errorf("want exactly the one answer before the broken input, got %q", out.String())
	}
}

// FuzzSessionChild feeds the loop arbitrary input: it must end, with 0 or
// 1, without panicking, and every line it writes must be a response.
func FuzzSessionChild(f *testing.F) {
	f.Add(sessionRequests(f, wire.ChildRequest{FQCN: nativeIPCEchoMethodName, Mode: "execute"}))
	f.Add(sessionRequests(f, wire.ChildRequest{FQCN: nativeIPCEchoMethodName, Mode: "bogus"}, wire.ChildRequest{FQCN: ""}))
	f.Add([]byte(`{"fqcn":1}`))
	f.Add([]byte("[]{}\"\x00"))
	f.Fuzz(func(t *testing.T, in []byte) {
		var out bytes.Buffer
		code := runCollectionSession(context.Background(), bytes.NewReader(in), &out, io.Discard)
		if code != 0 && code != 1 {
			t.Fatalf("exit %d", code)
		}
		dec := json.NewDecoder(&out)
		for {
			var r wire.ChildResponse
			if err := dec.Decode(&r); err == io.EOF {
				break
			} else if err != nil {
				t.Fatalf("the session wrote something that is not a response: %v", err)
			}
		}
	})
}
