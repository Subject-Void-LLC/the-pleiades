// Fuzz target for ParseMedia and LinkedClones: what the host writes is not
// trusted to be well formed, and Forget acts on what they decide.
package vboxmanage_test

import (
	"os"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

// FuzzParseMedia proves ParseMedia never panics and keeps no block without
// a UUID; that every user it reads carries a UUID of 36 characters; that
// LinkedClones ends on any parent graph, loops included, never names the
// machine asked about, names each clone once, and names only machines the
// input lists as using a disk; and that parsing the same text twice gives
// the same answer.
func FuzzParseMedia(f *testing.F) {
	if raw, err := os.ReadFile("testdata/list-long-hdds.stdout"); err == nil {
		f.Add(string(raw), capturedBase)
		f.Add(string(raw), capturedClone)
	}
	for _, s := range []string{
		"",
		"UUID: a\nParent UUID: b\n\nUUID: b\nParent UUID: a\n",
		"UUID: a\nParent UUID: a\nIn use by VMs:  x (UUID: 11111111-1111-4111-8111-111111111111)\n",
		"In use by VMs:  x (UUID: 11111111-1111-4111-8111-111111111111)\n",
		"UUID: d\nIn use by VMs:  [s (UUID: 22222222-2222-4222-8222-222222222222)]\n",
		"UUID: d\n   continued without a key\n",
		"UUID:\r\n\r\n",
		strings.Repeat("UUID: x\nParent UUID: x\n\n", 50),
	} {
		f.Add(s, "11111111-1111-4111-8111-111111111111")
	}
	f.Fuzz(func(t *testing.T, text, uuid string) {
		media := vboxmanage.ParseMedia(text)
		users := map[string]bool{}
		for _, m := range media {
			if m.UUID == "" {
				t.Fatalf("a medium with no UUID was kept: %+v", m)
			}
			for _, u := range m.Users {
				if len(u.UUID) != 36 {
					t.Fatalf("user %+v has no UUID of 36 characters", u)
				}
				users[strings.ToLower(u.UUID)] = true
			}
		}
		clones := vboxmanage.LinkedClones(media, uuid)
		seen := map[string]bool{}
		for _, c := range clones {
			id := strings.ToLower(c.UUID)
			if strings.EqualFold(c.UUID, uuid) {
				t.Fatalf("%s is named a linked clone of itself", uuid)
			}
			if seen[id] {
				t.Fatalf("%s is named twice", c.UUID)
			}
			if !users[id] {
				t.Fatalf("%s is named a clone but uses no disk in the input", c.UUID)
			}
			seen[id] = true
		}
		if again := vboxmanage.LinkedClones(vboxmanage.ParseMedia(text), uuid); len(again) != len(clones) {
			t.Fatalf("the same text gave %d clones, then %d", len(clones), len(again))
		}
	})
}
