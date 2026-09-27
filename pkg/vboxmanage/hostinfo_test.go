// Tests for reading the host's processors and memory from list hostinfo,
// as the lab host wrote it (testdata/list-hostinfo.stdout).
package vboxmanage

import (
	"context"
	"strings"
	"testing"
)

func TestHostInfo_Captured(t *testing.T) {
	r := answering{Output{Stdout: fixture(t, "list-hostinfo.stdout")}}
	info, err := Host{Runner: r, Path: vbox}.HostInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info != (HostInfo{CPUs: 20, MemoryMB: 32388, AvailableMB: 15121}) {
		t.Errorf("read %+v", info)
	}
}

func TestParseHostInfo_Refusals(t *testing.T) {
	good := fixture(t, "list-hostinfo.stdout")
	for why, tt := range map[string]struct{ text, want string }{
		"no processor count":  {strings.Replace(good, "Processor online count: 20", "", 1), `no "Processor online count" line`},
		"no memory":           {strings.Replace(good, "Memory size: 32388 MByte", "", 1), `no "Memory size" line`},
		"memory in gigabytes": {strings.Replace(good, "Memory size: 32388 MByte", "Memory size: 32 GByte", 1), `"Memory size" is "32 GByte"`},
		"memory with no unit": {strings.Replace(good, "Memory available: 15121 MByte", "Memory available: 15121", 1), `"Memory available" is "15121"`},
		"negative memory":     {strings.Replace(good, "Memory available: 15121 MByte", "Memory available: -1 MByte", 1), `"Memory available" is "-1 MByte"`},
		"no processors":       {strings.Replace(good, "Processor online count: 20", "Processor online count: 0", 1), "0 processors"},
		"more free than all":  {strings.Replace(good, "Memory available: 15121 MByte", "Memory available: 40000 MByte", 1), "40000 MB of 32388 MB"},
		"nothing":             {"", `no "Processor online count" line`},
	} {
		if _, err := ParseHostInfo(tt.text); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: %v, want one mentioning %q", why, err, tt.want)
		}
	}
	if info, err := ParseHostInfo(strings.Replace(good, "Memory available: 15121 MByte", "Memory available: 0 MByte", 1)); err != nil || info.AvailableMB != 0 {
		t.Errorf("a host with no memory free: %+v, %v", info, err)
	}
}

func TestHostInfo_Fails(t *testing.T) {
	r := answering{Output{ExitCode: 1, Stderr: "VBoxManage.exe: error: E_ACCESSDENIED\r\n"}}
	if _, err := (Host{Runner: r, Path: vbox}).HostInfo(context.Background()); err == nil || !strings.Contains(err.Error(), "E_ACCESSDENIED") {
		t.Errorf("%v", err)
	}
}
