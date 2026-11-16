// Package gnmi: decoding a device's notifications and Set answers, each
// refused whole at the first part that breaks a rule.
package gnmi

import (
	"context"
	"errors"
	"strings"
	"testing"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
)

// badPath is a path no rule accepts.
var badPath = &gpb.Path{Elem: []*gpb.PathElem{{Name: "a b"}}}

// TestDecodeNotifications_RefuseTheWholeAnswer refuses each malformed part.
func TestDecodeNotifications_RefuseTheWholeAnswer(t *testing.T) {
	good := &gpb.Update{Path: &gpb.Path{Elem: []*gpb.PathElem{{Name: "a"}}}, Val: &gpb.TypedValue{Value: &gpb.TypedValue_UintVal{UintVal: 1}}}
	for name, n := range map[string]*gpb.Notification{
		"nil":          nil,
		"bad prefix":   {Prefix: badPath, Update: []*gpb.Update{good}},
		"bad path":     {Update: []*gpb.Update{{Path: badPath, Val: good.Val}}},
		"deprecated":   {Update: []*gpb.Update{{Path: good.Path, Value: &gpb.Value{Value: []byte("1")}}}},
		"bad value":    {Update: []*gpb.Update{{Path: good.Path, Val: &gpb.TypedValue{Value: &gpb.TypedValue_JsonVal{JsonVal: []byte("{")}}}}},
		"bad deletion": {Delete: []*gpb.Path{badPath}},
	} {
		if _, err := decodeNotifications([]*gpb.Notification{n}); err == nil {
			t.Errorf("%s: decoded", name)
		}
	}
	many := &gpb.Notification{Update: make([]*gpb.Update, MaxUpdates+1)}
	if _, err := decodeNotifications([]*gpb.Notification{many}); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Errorf("an answer past MaxUpdates = %v", err)
	}
	ns, err := decodeNotifications([]*gpb.Notification{{Prefix: &gpb.Path{Elem: []*gpb.PathElem{{Name: "p"}}}, Update: []*gpb.Update{good}, Delete: []*gpb.Path{good.Path}, Atomic: true}})
	if err != nil || len(ns) != 1 || !ns[0].Atomic || len(ns[0].Deletes) != 1 || Join(ns[0].Prefix, ns[0].Updates[0].Path).String() != "/p/a" {
		t.Errorf("a good notification = %+v, %v", ns, err)
	}
}

// TestReadSetResponse_NamesEachOperation reads a Set's answer.
func TestReadSetResponse_NamesEachOperation(t *testing.T) {
	ok := &gpb.Path{Elem: []*gpb.PathElem{{Name: "a"}}}
	res, err := readSetResponse(&gpb.SetResponse{Timestamp: 7, Response: []*gpb.UpdateResult{
		{Path: ok, Op: gpb.UpdateResult_DELETE}, {Path: ok, Op: gpb.UpdateResult_REPLACE},
		{Path: ok, Op: gpb.UpdateResult_UPDATE}, {Path: ok, Op: gpb.UpdateResult_UNION_REPLACE}, {Path: ok, Op: 99},
	}})
	if err != nil || res.Timestamp != 7 || len(res.Operations) != 5 {
		t.Fatalf("readSetResponse = %+v, %v", res, err)
	}
	for i, want := range []string{"delete", "replace", "update", "union_replace", "unknown"} {
		if res.Operations[i].Op != want {
			t.Errorf("operation %d = %s, want %s", i, res.Operations[i].Op, want)
		}
	}
	if _, err := readSetResponse(&gpb.SetResponse{Prefix: badPath}); err == nil {
		t.Error("a bad prefix was read")
	}
	if _, err := readSetResponse(&gpb.SetResponse{Response: []*gpb.UpdateResult{{Path: badPath}}}); err == nil {
		t.Error("a bad operation path was read")
	}
}

// TestLost_SortsASetsFailure reads which failures leave a Set's outcome
// unknown.
func TestLost_SortsASetsFailure(t *testing.T) {
	for code, want := range map[codes.Code]bool{
		codes.DeadlineExceeded: true, codes.Canceled: true, codes.Unavailable: true,
		codes.InvalidArgument: false, codes.Unauthenticated: false, codes.PermissionDenied: false,
	} {
		if got := lost(status.Error(code, "x")); got != want {
			t.Errorf("lost(%s) = %v", code, got)
		}
	}
}

// TestCapturingCredentials_ACloneSharesTheRecord holds a clone to recording
// into the same place, since gRPC may hand any connection a clone.
func TestCapturingCredentials_ACloneSharesTheRecord(t *testing.T) {
	seen := &handshakeSeen{}
	c := &capturingCredentials{TransportCredentials: credentials.NewTLS(nil), seen: seen}
	clone, ok := c.Clone().(*capturingCredentials)
	if !ok || clone.seen != seen {
		t.Fatal("the clone records somewhere else")
	}
	seen.set(errors.New("refused"))
	client := &Client{handshake: seen, target: "r:1", certWatch: &devicetls.Watch{}}
	if err := client.connectionFailure(context.Background()); err == nil || !strings.Contains(err.Error(), "nothing was sent") {
		t.Errorf("connectionFailure = %v", err)
	}
	if (&Client{}).connectionFailure(context.Background()) != nil {
		t.Error("a plaintext client reports a handshake failure")
	}
	if got := truncate(strings.Repeat("é", 100), 7); len(got) > 7 || !strings.HasPrefix(got, "é") {
		t.Errorf("truncate = %q", got)
	}
	if (&Error{RPC: "Get", Code: codes.NotFound, Message: `"x"`}).Error() == "" {
		t.Error("an Error says nothing")
	}
}
