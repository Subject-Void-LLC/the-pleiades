// Tests for reading IOS configurations, IOS's own difference reports, and
// the undo lines written from two configurations. The configurations are
// shaped as show running-config prints them on IOS XE 17.15 (the CML
// sandbox router, 2026-10-23); the difference reports are that router's own.
package iosconfig_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/iosconfig"
)

// base is a small running configuration with the header, comments, a
// banner and NTP's clock period IOS prints around and inside one.
const base = `Building configuration...

Current configuration : 1520 bytes
!
! Last configuration change at 14:02:11 UTC Thu Oct 23 2026
!
version 17.15
hostname R1
!
ntp clock-period 17179869
interface Loopback0
 ip address 10.255.0.1 255.255.255.255
!
interface Ethernet0/1
 description spare
 no ip address
 shutdown
!
router ospf 1
 router-id 10.255.0.1
 network 10.0.0.0 0.0.0.255 area 0
!
ip access-list extended EDGE
 10 permit tcp any any eq 22
 20 deny ip any any
!
banner motd ^C
Authorised use only.
^C
!
end
`

// change applies edits, each an old line and its replacement ("" adds or
// removes), to base.
func change(t *testing.T, edits ...[2]string) string {
	t.Helper()
	out := base
	for _, e := range edits {
		if e[0] == "" {
			out = strings.Replace(out, "!\nend\n", e[1]+"\n!\nend\n", 1)
			continue
		}
		if !strings.Contains(out, e[0]) {
			t.Fatalf("base holds no %q", e[0])
		}
		out = strings.Replace(out, e[0], e[1], 1)
	}
	return out
}

func TestParse_ReadsTheTreeAndLeavesOutWhatIsNotConfiguration(t *testing.T) {
	roots := iosconfig.Parse(base)
	var lines []string
	for _, r := range roots {
		lines = append(lines, strings.SplitN(r.Line, "\n", 2)[0])
	}
	want := "version 17.15|hostname R1|interface Loopback0|interface Ethernet0/1|router ospf 1|ip access-list extended EDGE|banner motd ^C"
	if got := strings.Join(lines, "|"); got != want {
		t.Fatalf("top-level lines\n got %s\nwant %s", got, want)
	}
	if eth := roots[3]; len(eth.Children) != 3 || eth.Children[0].Line != "description spare" {
		t.Errorf("Ethernet0/1's children = %+v", eth.Children)
	}
	if banner := roots[6]; !strings.Contains(banner.Line, "Authorised use only.") || len(banner.Children) != 0 {
		t.Errorf("the banner's text is not kept in its line: %q", banner.Line)
	}
}

func TestInverse_UndoesEachKindOfChange(t *testing.T) {
	for name, tc := range map[string]struct {
		after string
		want  []string
	}{
		"nothing changed": {after: base, want: nil},
		"a block added": {
			after: change(t, [2]string{"", "interface Loopback198\n description pleiades\n no ip address"}),
			want:  []string{"no interface Loopback198"},
		},
		"a setting replaced": {
			after: change(t, [2]string{" description spare", " description uplink to core"}),
			want:  []string{"interface Ethernet0/1", " no description uplink to core", " description spare"},
		},
		"an interface brought up": {
			after: change(t, [2]string{" shutdown\n", ""}),
			want:  []string{"interface Ethernet0/1", " shutdown"},
		},
		"an address set where there was none": {
			after: change(t, [2]string{" no ip address\n", " ip address 10.0.0.1 255.255.255.0\n"}),
			want:  []string{"interface Ethernet0/1", " no ip address 10.0.0.1 255.255.255.0", " no ip address"},
		},
		"an address removed": {
			after: change(t, [2]string{" ip address 10.255.0.1 255.255.255.255", " no ip address"}),
			want:  []string{"interface Loopback0", " ip address 10.255.0.1 255.255.255.255"},
		},
		"a block removed is put back whole": {
			after: change(t, [2]string{"router ospf 1\n router-id 10.255.0.1\n network 10.0.0.0 0.0.0.255 area 0\n", ""}),
			want:  []string{"router ospf 1", " router-id 10.255.0.1", " network 10.0.0.0 0.0.0.255 area 0"},
		},
		"a global line replaced": {
			after: change(t, [2]string{"hostname R1", "hostname core-1"}),
			want:  []string{"no hostname core-1", "hostname R1"},
		},
		"an access list removed whole is put back in order": {
			after: change(t, [2]string{"ip access-list extended EDGE\n 10 permit tcp any any eq 22\n 20 deny ip any any\n", ""}),
			want:  []string{"ip access-list extended EDGE", " 10 permit tcp any any eq 22", " 20 deny ip any any"},
		},
		"only NTP's clock period moved": {
			after: change(t, [2]string{"ntp clock-period 17179869", "ntp clock-period 17179870"}),
			want:  nil,
		},
	} {
		got, err := iosconfig.Inverse(iosconfig.Parse(base), iosconfig.Parse(tc.after))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
			t.Errorf("%s:\n got %q\nwant %q", name, got, tc.want)
		}
	}
}

func TestInverse_RefusesWhatCannotBeUndoneLineByLine(t *testing.T) {
	for name, after := range map[string]string{
		"an access list entry added": change(t, [2]string{" 20 deny ip any any", " 15 permit icmp any any\n 20 deny ip any any"}),
		"a banner changed":           change(t, [2]string{"Authorised use only.", "Keep out."}),
		"a certificate chain added":  change(t, [2]string{"", "crypto pki certificate chain TP-1\n certificate self-signed 01"}),
	} {
		_, err := iosconfig.Inverse(iosconfig.Parse(base), iosconfig.Parse(after))
		if !errors.Is(err, iosconfig.ErrUnsupported) {
			t.Errorf("%s: err = %v, want ErrUnsupported", name, err)
		}
	}
}

// TestInverse_NestedBlocksNameEveryBlockAbove: a change inside an
// address family is preceded by both blocks it sits in.
func TestInverse_NestedBlocksNameEveryBlockAbove(t *testing.T) {
	before := "router bgp 65001\n address-family ipv4\n  neighbor 10.0.0.2 activate\n exit-address-family\n"
	after := "router bgp 65001\n address-family ipv4\n  neighbor 10.0.0.2 activate\n  network 10.9.0.0 mask 255.255.0.0\n exit-address-family\n"
	got, err := iosconfig.Inverse(iosconfig.Parse(before), iosconfig.Parse(after))
	want := []string{"router bgp 65001", " address-family ipv4", "  no network 10.9.0.0 mask 255.255.0.0"}
	if err != nil || strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got %q, %v; want %q", got, err, want)
	}
}

// The three answers the sandbox router gave show archive config
// differences, verbatim but for the echoed command.
const (
	differencesAdded = "!Contextual Config Diffs:\r\n+interface Loopback198\r\n +description pleiades-replace-probe\r\n +no ip address\r\n"
	differencesNone  = "!Contextual Config Diffs:\r\n!No changes were found\r\n"
	differencesError = "                                 ^\r\n% Invalid input detected at '^' marker.\r\n"
)

func TestDifferences_ReadsTheDevicesOwnReport(t *testing.T) {
	got, err := iosconfig.Differences(differencesAdded)
	want := []string{"+interface Loopback198", " +description pleiades-replace-probe", " +no ip address"}
	if err != nil || strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("added: got %q, %v; want %q", got, err, want)
	}
	if got, err := iosconfig.Differences(differencesNone); err != nil || got != nil {
		t.Errorf("none: got %q, %v; want nil", got, err)
	}
	if _, err := iosconfig.Differences(differencesError); !errors.Is(err, iosconfig.ErrNotDifferences) {
		t.Errorf("an error answer: err = %v, want ErrNotDifferences, never no differences", err)
	}
	if got, err := iosconfig.Differences("!Contextual Config Diffs:\n+ntp clock-period 1\n-ntp clock-period 2\n"); err != nil || len(got) != 0 {
		t.Errorf("only the clock period: got %q, %v; want none", got, err)
	}
}

// FuzzInverse: device output is untrusted, so reading and undoing any two
// texts never panics.
func FuzzInverse(f *testing.F) {
	f.Add(base, change(&testing.T{}, [2]string{" description spare", " description x"}))
	f.Add("banner motd ^C\n", "interface a\n  b\n c\nd")
	f.Fuzz(func(t *testing.T, before, after string) {
		_, _ = iosconfig.Inverse(iosconfig.Parse(before), iosconfig.Parse(after))
		_, _ = iosconfig.Differences(after)
	})
}
