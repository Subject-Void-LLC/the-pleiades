// Package facts: the identity facts' parsers (Phase 41, base). Each rule is held
// to the case it exists for: a file that is not a machine id is not stored, a
// placeholder serial is left out, and a host with no virtualization tool gets no
// fact rather than a claim it is physical.
package facts

import "testing"

func TestGatherMachineID_OnlyAMachineIDIsStored(t *testing.T) {
	good := "0123456789abcdef0123456789abcdef\n"
	if got := gatherMachineID(good); len(got) != 1 || got[0].key != gatherFactMachineID || got[0].value != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("gatherMachineID(valid) = %+v", got)
	}
	for _, bad := range []string{"", "not a machine id", "0123456789ABCDEF0123456789ABCDEF", "0123456789abcdef0123456789abcde", "0123456789abcdef0123456789abcdef0"} {
		if got := gatherMachineID(bad); got != nil {
			t.Errorf("gatherMachineID(%q) stored %+v, want nothing", bad, got)
		}
	}
}

func TestGatherIdentityValue_APlaceholderIsNeverStored(t *testing.T) {
	read := gatherIdentityValue(gatherFactSerial)
	if got := read("ABC123XYZ\n"); len(got) != 1 || got[0].value != "ABC123XYZ" {
		t.Fatalf("a real serial = %+v", got)
	}
	for _, placeholder := range []string{"", "  \n", "To be filled by O.E.M.", "0", "Not Specified", "None", "Default string", "00000000-0000-0000-0000-000000000000", "FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF"} {
		if got := read(placeholder); got != nil {
			t.Errorf("serial %q stored %+v, want nothing", placeholder, got)
		}
	}
}

func TestGatherVirtualization_AnAbsentToolIsNoFactNotPhysical(t *testing.T) {
	cases := map[string]string{
		"kvm\n|\nnone\n":       "vm:kvm",
		"none\n|\nnone\n":      "physical",
		"none\n|\ndocker\n":    "container:docker",
		"amazon\n|\nnone\n":    "cloud:amazon",
		"microsoft\n|\nnone\n": "vm:microsoft",
		"kvm\n|\npodman\n":     "container:podman",
		"\n|\n":                "",
	}
	for in, want := range cases {
		got := gatherVirtualization(in)
		if want == "" {
			if got != nil {
				t.Errorf("gatherVirtualization(%q) = %+v, want no fact", in, got)
			}
			continue
		}
		if len(got) != 1 || got[0].value != want {
			t.Errorf("gatherVirtualization(%q) = %+v, want %s", in, got, want)
		}
	}
	if got := gatherVirtualization("none\n"); got != nil {
		t.Errorf("a read without the separator stored %+v, want nothing", got)
	}
}
