// ParseMedia and LinkedClones, against what the lab host wrote.
package vboxmanage_test

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

// The machines in testdata/list-long-hdds.stdout, captured on the lab host
// on 2026-11-13: a base imported from a VHDX with one snapshot, and a
// linked clone of that snapshot.
const (
	capturedBase  = "1e5793e0-6eac-4684-bfa8-472b1dc5312d"
	capturedClone = "33600706-08fd-4c4e-9a4a-ccbae1095d7c"
)

func TestParseMedia_ReadsTheCapturedHost(t *testing.T) {
	raw, err := os.ReadFile("testdata/list-long-hdds.stdout")
	if err != nil {
		t.Fatal(err)
	}
	media := vboxmanage.ParseMedia(string(raw))
	want := []vboxmanage.Medium{
		{UUID: "67793435-652b-492e-8649-7f664ce14aa9", State: "locked read", Location: `G:\PleiadesLab\p112-base\p112-base.vdi`,
			// Its snapshot, in brackets, is not a machine.
			Users: []vboxmanage.MediumUser{{Name: "p112-base", UUID: capturedBase}}},
		{UUID: "2de3d928-bdfa-4f21-a0f6-95b70b3450c8", Parent: "67793435-652b-492e-8649-7f664ce14aa9", State: "created",
			Location: `G:\PleiadesLab\p112-base\Snapshots/{2de3d928-bdfa-4f21-a0f6-95b70b3450c8}.vdi`,
			Users:    []vboxmanage.MediumUser{{Name: "p112-base", UUID: capturedBase}}},
		{UUID: "485b4e77-4756-4138-8faa-0596743075b6", Parent: "67793435-652b-492e-8649-7f664ce14aa9", State: "locked write",
			Location: `G:\PleiadesLab\p112-a\Snapshots\{485b4e77-4756-4138-8faa-0596743075b6}.vdi`,
			Users:    []vboxmanage.MediumUser{{Name: "p112-a", UUID: capturedClone}}},
	}
	if !reflect.DeepEqual(media, want) {
		t.Fatalf("parsed\n%+v\nwant\n%+v", media, want)
	}
	if got := vboxmanage.LinkedClones(media, capturedBase); !reflect.DeepEqual(got, []vboxmanage.MediumUser{{Name: "p112-a", UUID: capturedClone}}) {
		t.Errorf("the base's linked clones: %+v", got)
	}
	if got := vboxmanage.LinkedClones(media, capturedClone); len(got) != 0 {
		t.Errorf("the clone has linked clones of its own: %+v", got)
	}
}

func TestParseMedia_Edges(t *testing.T) {
	for name, tt := range map[string]struct {
		in   string
		want []vboxmanage.Medium
	}{
		"empty":         {"", nil},
		"no UUID":       {"Location:       C:\\a.vdi\r\n\r\n", nil},
		"a line no key": {"UUID:           x\r\nnot a key line\r\n", []vboxmanage.Medium{{UUID: "x"}}},
		"two users, one continued": {"UUID: d\nIn use by VMs:  a (UUID: 11111111-1111-4111-8111-111111111111)\n                b (UUID: 22222222-2222-4222-8222-222222222222) [s (UUID: 33333333-3333-4333-8333-333333333333)]\n",
			[]vboxmanage.Medium{{UUID: "d", Users: []vboxmanage.MediumUser{{Name: "a", UUID: "11111111-1111-4111-8111-111111111111"}, {Name: "b", UUID: "22222222-2222-4222-8222-222222222222"}}}}},
	} {
		if got := vboxmanage.ParseMedia(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: %+v, want %+v", name, got, tt.want)
		}
	}
}

// BenchmarkLinkedClones reads a host of 1000 bases, each with one linked
// clone, as list --long hdds writes it, and asks for one base's clones:
// Forget's whole cost before it unregisters anything.
func BenchmarkLinkedClones(b *testing.B) {
	var text strings.Builder
	for i := range 1000 {
		fmt.Fprintf(&text, "UUID:           00000000-0000-4000-a000-%012d\r\nParent UUID:    base\r\nLocation:       G:\\b%d\\b.vdi\r\nIn use by VMs:  b%d (UUID: 00000000-0000-4000-b000-%012d)\r\n\r\n", i, i, i, i)
		fmt.Fprintf(&text, "UUID:           00000000-0000-4000-c000-%012d\r\nParent UUID:    00000000-0000-4000-a000-%012d\r\nLocation:       G:\\c%d\\c.vdi\r\nIn use by VMs:  c%d (UUID: 00000000-0000-4000-d000-%012d)\r\n\r\n", i, i, i, i, i)
	}
	raw := text.String()
	b.ReportAllocs()
	for b.Loop() {
		if got := vboxmanage.LinkedClones(vboxmanage.ParseMedia(raw), "00000000-0000-4000-b000-000000000500"); len(got) != 1 {
			b.Fatalf("%+v", got)
		}
	}
}

// TestLinkedClones_AParentLoopEnds: a loop of parents, which VirtualBox
// never writes, ends the walk rather than hanging it.
func TestLinkedClones_AParentLoopEnds(t *testing.T) {
	media := []vboxmanage.Medium{
		{UUID: "a", Parent: "b", Users: []vboxmanage.MediumUser{{Name: "x", UUID: "11111111-1111-4111-8111-111111111111"}}},
		{UUID: "b", Parent: "a", Users: []vboxmanage.MediumUser{{Name: "y", UUID: "22222222-2222-4222-8222-222222222222"}}},
	}
	if got := vboxmanage.LinkedClones(media, "33333333-3333-4333-8333-333333333333"); len(got) != 0 {
		t.Errorf("%+v", got)
	}
}
