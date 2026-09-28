// Tests for reading VirtualBox's DHCP leases (leases.go) against the file
// captured from the lab host (testdata/dhcpd.leases), and for typing into
// a console (keyboard.go) against the model host.
package vboxmanage_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage/vboxmanagetest"
)

const hostOnly = "VirtualBox Host-Only Ethernet Adapter"

func TestParseLeasesReadsTheHostsFile(t *testing.T) {
	leases, err := vboxmanage.ParseLeases(captured(t, "dhcpd.leases"))
	if err != nil {
		t.Fatal(err)
	}
	want := vboxmanage.Lease{MAC: "0800273C99C7", Address: "192.168.56.102", State: "acked",
		Issued: time.Unix(1790536392, 0).UTC(), Expires: time.Unix(1790536392+600, 0).UTC()}
	if len(leases) != 2 || !reflect.DeepEqual(leases[0], want) || leases[1].State != "expired" {
		t.Errorf("read %+v", leases)
	}
	for name, text := range map[string]string{
		"not XML":     "<Leases>",
		"no address":  `<Leases><Lease mac="08:00:27:3c:99:c7" state="acked"/></Leases>`,
		"a short MAC": `<Leases><Lease mac="08:00:27" state="acked"><Address value="1.2.3.4"/></Lease></Leases>`,
	} {
		if _, err := vboxmanage.ParseLeases(text); err == nil {
			t.Errorf("%s: read", name)
		}
	}
}

func TestDHCPLeases(t *testing.T) {
	ctx := context.Background()
	h := vboxmanagetest.New()
	host := h.VBoxHost()
	if leases, err := host.DHCPLeases(ctx, hostOnly); err != nil || leases != nil {
		t.Errorf("a server that handed out nothing: %v, %v", leases, err)
	}
	h.Leases = map[string]string{hostOnly: captured(t, "dhcpd.leases")}
	if leases, err := host.DHCPLeases(ctx, hostOnly); err != nil || len(leases) != 2 {
		t.Errorf("read %v, %v", leases, err)
	}
	if _, err := host.DHCPLeases(ctx, `a'b`); err == nil {
		t.Error("read an adapter with a quote in its name")
	}
	h.Fail = map[string]vboxmanage.Output{"powershell leases": {ExitCode: 1, Stderr: "Access denied\r\n"}}
	if _, err := host.DHCPLeases(ctx, hostOnly); err == nil || !strings.Contains(err.Error(), "Access denied") {
		t.Errorf("a script that failed: %v", err)
	}
	h.Fail = map[string]vboxmanage.Output{"powershell leases": {Stdout: "not base64!\r\n"}}
	if _, err := host.DHCPLeases(ctx, hostOnly); err == nil {
		t.Error("read leases that came back garbled")
	}
}

func TestKeysReadsPackersNotation(t *testing.T) {
	strokes, err := vboxmanage.Keys("Administrator<tab>x y<enter><wait><wait3><up><f12>")
	if err != nil {
		t.Fatal(err)
	}
	want := []vboxmanage.Keystroke{
		{Text: "Administrator"}, {Scancodes: []string{"0f", "8f"}}, {Text: "x y"}, {Scancodes: []string{"1c", "9c"}},
		{Pause: time.Second}, {Pause: 3 * time.Second}, {Scancodes: []string{"e0", "48", "e0", "c8"}}, {Scancodes: []string{"58", "d8"}},
	}
	if !reflect.DeepEqual(strokes, want) {
		t.Errorf("read %+v", strokes)
	}
	if names := vboxmanage.KeyNames(); len(names) < 20 || names[0] != "bs" {
		t.Errorf("key names %v", names)
	}
	for name, written := range map[string]string{
		"nothing":          "",
		"an unknown key":   "<return>",
		"a long pause":     "<wait61>",
		"a zero pause":     "<wait0>",
		"a non-ASCII rune": "café",
		"a control char":   "a\tb",
	} {
		if _, err := vboxmanage.Keys(written); err == nil {
			t.Errorf("%s: read", name)
		}
	}
}

func TestTypeSendsEachStroke(t *testing.T) {
	ctx := context.Background()
	h := vboxmanagetest.New(&vboxmanagetest.VM{Name: "win", UUID: "00000000-0000-4000-8000-000000000020", State: vboxmanage.StateRunning})
	strokes, _ := vboxmanage.Keys("ab<tab>")
	strokes = append(strokes, vboxmanage.Keystroke{Pause: time.Millisecond})
	if err := h.VBoxHost().Type(ctx, "win", strokes); err != nil {
		t.Fatal(err)
	}
	if typed := h.VM("win").Typed; !reflect.DeepEqual(typed, []string{"ab", "0f 8f"}) {
		t.Errorf("typed %q", typed)
	}
	if err := h.VBoxHost().Type(ctx, "a b", strokes); err == nil {
		t.Error("typed into a bad name")
	}
	off := vboxmanagetest.New(&vboxmanagetest.VM{Name: "off", UUID: "00000000-0000-4000-8000-000000000021", State: vboxmanage.StatePoweroff})
	if err := off.VBoxHost().Type(ctx, "off", strokes); err == nil {
		t.Error("typed into a machine that is off")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := h.VBoxHost().Type(cancelled, "win", []vboxmanage.Keystroke{{Pause: time.Hour}}); !errors.Is(err, context.Canceled) {
		t.Errorf("a pause in a cancelled run: %v", err)
	}
}
