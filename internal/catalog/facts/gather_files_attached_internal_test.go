// Package facts: the files probe's answers about what a path carries beside
// its content, and about its access control lists, read as the parser reads
// them, including the answers no machine here prints (a tool's bad hex, a
// digest that is not one, more names than the bound).
package facts

import (
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// attachedOf parses one file section and returns its file_attached. value.
func attachedOf(t *testing.T, section string) (map[string]any, map[string]any, bool) {
	t.Helper()
	facts := filesFacts([]string{"/f"}, parseFiles(fileSectionMark+"\n"+section))
	file, _ := facts[0].value.(map[string]any)
	if len(facts) == 1 {
		return file, nil, false
	}
	attached, _ := facts[1].value.(map[string]any)
	return file, attached, true
}

func TestParseFiles_ReadsWhatAPathCarriesBesideItsContent(t *testing.T) {
	ok := "ok:regular file:3:0:0:644:root:root\n"
	digest := strings.Repeat("ab", 32)
	names := hex.EncodeToString([]byte("user.a\nuser.b\tuser.c"))

	file, a, found := attachedOf(t, ok+"xn:"+names+"\nxd:"+strings.ToUpper(digest)+"\n")
	if !found || a["state"] != "listed" || a["kind"] != "xattrs" || a[collection.RawDigestField] != digest || len(a["names"].([]any)) != 3 {
		t.Fatalf("a listing = %v", a)
	}
	for k := range file {
		if strings.HasPrefix(k, "_attached") {
			t.Errorf("the file fact kept %s", k)
		}
	}
	// Names that are not hex are no listing at all; a digest that is not
	// 64 hex characters is no digest, so the listing reads as unreadable.
	if _, _, found := attachedOf(t, ok+"xn:zz\nxd:"+digest+"\n"); found {
		t.Error("names that are not hex made a listing")
	}
	for _, d := range []string{"abc", strings.Repeat("zz", 32)} {
		if _, a, _ := attachedOf(t, ok+"xn:"+names+"\nxd:"+d+"\n"); a["state"] != "unreadable" || a[collection.RawDigestField] != nil {
			t.Errorf("digest %q: %v, want unreadable and no digest", d, a)
		}
	}
	if _, a, _ := attachedOf(t, ok+"xs:unlisted\n"); a["state"] != "unlisted" || a["names"] != nil {
		t.Errorf("unlisted = %v", a)
	}
	// An empty listing is listed, with no names.
	if _, a, _ := attachedOf(t, ok+"xn:\nxd:"+digest+"\n"); a["state"] != "listed" || len(a["names"].([]any)) != 0 {
		t.Errorf("no attributes = %v", a)
	}
	// Past the bound, the names stop and say so; the digest is still whole.
	many := make([]string, maxAttachedNames+5)
	for i := range many {
		many[i] = fmt.Sprintf("user.n%d", i)
	}
	if _, a, _ := attachedOf(t, ok+"xn:"+hex.EncodeToString([]byte(strings.Join(many, "\n")))+"\nxd:"+digest+"\n"); a["names_truncated"] != true || len(a["names"].([]any)) != maxAttachedNames {
		t.Errorf("past the bound: %d names, truncated %v", len(a["names"].([]any)), a["names_truncated"])
	}
}

func TestTakeAttached_KeepsTheKindTheAnswerNames(t *testing.T) {
	value := map[string]any{attachedKindField: attachedStreams, attachedStateField: attachedListed, attachedNamesField: []any{"x"},
		attachedDigestField: strings.Repeat("cd", 32), attachedUnreadField: 2, attachedTruncatedField: true}
	a, ok := takeAttached(value, attachedXattrs)
	if !ok || a["kind"] != attachedStreams || a["unread"] != 2 || a["names_truncated"] != true || len(value) != 0 {
		t.Fatalf("takeAttached = %v (left %v)", a, value)
	}
	if _, ok := takeAttached(map[string]any{}, attachedXattrs); ok {
		t.Error("an answer with no state made a fact")
	}
}

func TestParseFiles_ACLIsTheWorstAnswer(t *testing.T) {
	ok := "ok:regular file:3:0:0:644:root:root\n"
	for section, want := range map[string]string{
		"acl:listed\nacl:listed\n":     "listed",
		"acl:listed\nacl:unlisted\n":   "unlisted",
		"acl:unlisted\nacl:listed\n":   "unlisted",
		"acl:listed\nacl:unreadable\n": "unreadable",
		"acl:bogus\n":                  "unreadable",
		"acl:unlisted\nacl:bogus\n":    "unlisted",
	} {
		if file, _, _ := attachedOf(t, ok+section); file["acl"] != want {
			t.Errorf("%q: acl = %v, want %s", section, file["acl"], want)
		}
	}
	out := `[{"state":"present","type":"file","world_readable":true,"readable":true,"acl":"listed"},{"state":"present","type":"file","acl":"nonsense"}]`
	got := parseWindowsFiles(out)
	if a, _ := got[0].value.(map[string]any); a["acl"] != "listed" {
		t.Errorf("Windows listed = %v", a)
	}
	if b, _ := got[1].value.(map[string]any); b["acl"] != "unreadable" {
		t.Errorf("a Windows word this does not know = %v, want unreadable", b)
	}
}

func TestSocketDescriptor_RefusesAnEmptyNumber(t *testing.T) {
	for _, line := range []string{"socket  /proc/1/fd/3", "socket 5 /proc//fd/3", "socket 5 /proc/1/fd/"} {
		if _, _, ok := socketDescriptor(line); ok {
			t.Errorf("%q read as a descriptor", line)
		}
	}
}
