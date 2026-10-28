// Tests of which IOS command lines read only.
package ios

import "testing"

// TestIOSShow_OnlyAShowThatWritesNoFile: show and its abbreviations, and
// dir and more in full, with any filtering pipe, read only; one sent to a
// file, or any other command, does not.
func TestIOSShow_OnlyAShowThatWritesNoFile(t *testing.T) {
	for line, want := range map[string]bool{
		"show version":                         true,
		"sh ip int brief":                      true,
		"SHO run | include hostname":           true,
		"show running-config | section router": true,
		"show run | redirect flash:run.txt":    false,
		"show run | re flash:run.txt":          false,
		"show tech | append flash:tech.txt":    false,
		"show log | tee flash:log.txt":         false,
		"s ip route":                           false,
		"copy running-config startup-config":   false,
		"reload":                               false,
		"shutdown":                             false,
		"":                                     false,
		"show":                                 true,
		"dir":                                  true,
		"dir unix:":                            true,
		"more unix:pleiades-3f2a.cfg":          true,
		"dir | redirect flash:list.txt":        false,
		"di":                                   false,
		"mo unix:a.cfg":                        false,
		"delete /force unix:a.cfg":             false,
	} {
		if got := iosShow(line); got != want {
			t.Errorf("iosShow(%q) = %v, want %v", line, got, want)
		}
	}
}

// TestCommandReadsOnly_AShowThatAnswersNothing: net.ios.command's
// ReadOnlyCall declares a call read-only only for a command that reads and
// writes no file (iosShow), with no question answered, no template still
// to render, and not terminal, which changes its session.
func TestCommandReadsOnly_AShowThatAnswersNothing(t *testing.T) {
	answer := []any{map[string]any{"match": "x", "answer": "y"}}
	for _, tc := range []struct {
		params map[string]any
		want   bool
	}{
		{map[string]any{paramCommand: "show version | include Version"}, true},
		{map[string]any{paramCommand: "ping 192.0.2.1"}, true},
		{map[string]any{paramCommand: "traceroute 192.0.2.1"}, true},
		{map[string]any{paramCommand: "dir flash:"}, true},
		{map[string]any{paramCommand: "more flash:a.cfg"}, true},
		{map[string]any{paramCommand: "show version", "prompts": answer}, false},
		{map[string]any{paramCommand: "show version", "expect_disconnect": true}, false},
		{map[string]any{paramCommand: "show run | redirect flash:a.txt"}, false},
		{map[string]any{paramCommand: "terminal length 0"}, false},
		{map[string]any{paramCommand: "show {{ vars.what }}"}, false},
		{map[string]any{paramCommand: "reload"}, false},
		{map[string]any{}, false},
	} {
		if got := commandReadsOnly(tc.params); got != tc.want {
			t.Errorf("commandReadsOnly(%v) = %v, want %v", tc.params, got, tc.want)
		}
	}
}
