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
