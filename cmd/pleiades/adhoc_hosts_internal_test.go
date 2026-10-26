// Tests for adhoc's hosts: "-" (or none) for a method that needs no device,
// run end to end through the pipeline; a comma list as one task per device;
// and journal list's label for an ad-hoc run and its --since and --changed
// filters.
package main

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

// adhocBook is the runbook adhocRunbook writes, read back.
type adhocBook struct {
	Hosts string           `yaml:"hosts"`
	Tasks []map[string]any `yaml:"tasks"`
}

func TestAdhocRunbook_HostsForms(t *testing.T) {
	read := func(hosts string, params map[string]any) adhocBook {
		t.Helper()
		data, err := adhocRunbook(hosts, "net.ios.command", params, 0)
		if err != nil {
			t.Fatal(err)
		}
		var b adhocBook
		if err := yaml.Unmarshal(data, &b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	if b := read("-", map[string]any{"src": "x"}); b.Hosts != "" || len(b.Tasks) != 1 {
		t.Errorf("no device: %+v", b)
	}
	b := read("br1, br2", map[string]any{"command": "show clock"})
	if b.Hosts != "" || len(b.Tasks) != 2 {
		t.Fatalf("a comma list: %+v", b)
	}
	for i, want := range []string{"br1", "br2"} {
		p, _ := b.Tasks[i]["net.ios.command"].(map[string]any)
		if p["target"] != want || p["command"] != "show clock" || b.Tasks[i]["name"] != "net.ios.command on "+want {
			t.Errorf("task %d = %v", i, b.Tasks[i])
		}
	}
	for _, bad := range []struct {
		hosts  string
		params map[string]any
	}{{"br1,,br2", nil}, {"br1,br1", nil}, {"br1,br2", map[string]any{"target": "br3"}}} {
		if _, err := adhocRunbook(bad.hosts, "net.ios.command", bad.params, 0); err == nil {
			t.Errorf("hosts %q with %v was taken", bad.hosts, bad.params)
		}
	}
}

// emptyCapture is a pcap file of one Ethernet frame carrying ICMP.
func emptyCapture() []byte {
	h := make([]byte, 24)
	binary.LittleEndian.PutUint32(h, 0xa1b2c3d4)
	binary.LittleEndian.PutUint16(h[4:], 2)
	binary.LittleEndian.PutUint16(h[6:], 4)
	binary.LittleEndian.PutUint32(h[16:], 65535)
	binary.LittleEndian.PutUint32(h[20:], 1)
	ip := make([]byte, 28)
	ip[0], ip[9] = 0x45, 1
	binary.BigEndian.PutUint16(ip[2:], 28)
	copy(ip[12:], []byte{10, 0, 0, 1, 10, 0, 0, 2})
	frame := append(append(make([]byte, 12), 0x08, 0x00), ip...)
	r := make([]byte, 16)
	binary.LittleEndian.PutUint32(r[8:], uint32(len(frame)))
	binary.LittleEndian.PutUint32(r[12:], uint32(len(frame)))
	return append(append(h, r...), frame...)
}

func TestAdhoc_AMethodThatNeedsNoDeviceTakesNoHosts(t *testing.T) {
	dir := t.TempDir()
	if err := runInit([]string{"--dir", dir}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "captures"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "captures", "wan.pcap"), emptyCapture(), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"net.pcap.summary", "src=captures/wan.pcap"},
		{"-", "net.pcap.summary", "src=captures/wan.pcap"},
	} {
		var err error
		out := captureStdout(t, func() { err = runAdhoc(append(args, "--dir", dir, "--json")) })
		if err != nil {
			t.Fatalf("adhoc %v: %v\n%s", args, err, out)
		}
		var rep struct {
			Tasks []struct {
				Stats map[string]any `json:"stats"`
			} `json:"tasks"`
		}
		if err := json.Unmarshal([]byte(out), &rep); err != nil || len(rep.Tasks) != 1 || rep.Tasks[0].Stats["cleartext"] != float64(1) {
			t.Fatalf("adhoc %v: %v\n%s", args, err, out)
		}
	}

	// Both runs are journaled under adhoc, named by their method, and
	// neither changed anything.
	out := captureStdout(t, func() {
		if err := runJournal([]string{"list", "--dir", dir, "--json"}); err != nil {
			t.Fatal(err)
		}
	})
	var runs []runSummary
	if err := json.Unmarshal([]byte(out), &runs); err != nil || len(runs) != 2 || runs[0].Runbook != "adhoc net.pcap.summary" {
		t.Fatalf("journal list: %v\n%s", err, out)
	}
	out = captureStdout(t, func() {
		if err := runJournal([]string{"list", "--dir", dir, "--changed"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "no run recorded in this project matches") {
		t.Errorf("--changed kept a run that changed nothing:\n%s", out)
	}
	out = captureStdout(t, func() {
		if err := runJournal([]string{"list", "--dir", dir, "--since", "1h", "--json"}); err != nil {
			t.Fatal(err)
		}
	})
	if err := json.Unmarshal([]byte(out), &runs); err != nil || len(runs) != 2 {
		t.Errorf("--since 1h: %v\n%s", err, out)
	}
	if err := runJournal([]string{"show", "x", "--dir", dir, "--changed"}); err == nil {
		t.Error("--changed was taken by journal show")
	}
}

func TestSinceTime(t *testing.T) {
	now := time.Date(2026, 10, 25, 18, 0, 0, 0, time.UTC)
	for text, want := range map[string]time.Time{
		"":                     {},
		"2h":                   now.Add(-2 * time.Hour),
		"2026-10-25T16:00:00Z": time.Date(2026, 10, 25, 16, 0, 0, 0, time.UTC),
	} {
		if got, err := sinceTime(text, now); err != nil || !got.Equal(want) {
			t.Errorf("sinceTime(%q) = %v, %v, want %v", text, got, err, want)
		}
	}
	for _, bad := range []string{"-2h", "yesterday", "2026-10-25"} {
		if _, err := sinceTime(bad, now); err == nil {
			t.Errorf("sinceTime(%q) was taken", bad)
		}
	}
}
