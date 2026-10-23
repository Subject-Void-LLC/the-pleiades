// Fuzz test of the console a GNS3 node is imported at: the server's words,
// never trusted.
package main

import (
	"testing"
	"unicode"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/gns3"
)

// FuzzGNS3Console: whatever a server says a console is, a node is either
// skipped with a reason or imported at a port in range and a host with no
// control character in it.
func FuzzGNS3Console(f *testing.F) {
	f.Add("telnet", 5000, "0.0.0.0", "gns3.lab")
	f.Add("telnet", 70000, "127.0.0.1", "10.0.0.5")
	f.Add("vnc", 5900, "::", "[::1]")
	f.Add("telnet", 23, "a\x1b[2Jb", "gns3.lab")
	f.Fuzz(func(t *testing.T, consoleType string, console int, consoleHost, serverHost string) {
		host, port, skip := gns3Console(gns3.Node{ConsoleType: consoleType, Console: &console, ConsoleHost: consoleHost}, serverHost)
		if skip != "" {
			if host != "" || port != 0 {
				t.Fatalf("skipped (%q) yet gave %q:%d", skip, host, port)
			}
			return
		}
		if port < 1 || port > 65535 {
			t.Fatalf("imported at port %d", port)
		}
		if host != serverHost {
			for _, r := range host {
				if unicode.IsControl(r) || unicode.IsSpace(r) {
					t.Fatalf("imported at host %q", host)
				}
			}
		}
	})
}
