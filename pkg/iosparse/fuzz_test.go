// Package iosparse_test: device output is untrusted, so parsing any text
// never panics, stays bounded in time, and fails only with a typed error.
package iosparse_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/iosparse"
)

// fuzzCommands are the commands the fuzz target parses output as, one
// per parser.
var fuzzCommands = []string{
	"show version", "show inventory", "show ip interface brief", "show interfaces", "show ip route",
	"show ip ospf neighbor", "show ip bgp summary", "show bgp vpnv4 unicast summary", "show vlan",
	"show cdp neighbors", "show lldp neighbors", "show archive config differences",
}

// FuzzParse feeds arbitrary output, which is untrusted device output in
// use, to every parser: Parse must never panic, must finish quickly, and
// must return either a typed error or a result encoding/json can write.
// The corpus is seeded with every genieparser fixture output.
func FuzzParse(f *testing.F) {
	outputs, _ := filepath.Glob("testdata/*/*/*/*_output.txt")
	for i, path := range outputs {
		data, err := os.ReadFile(path)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(uint8(i), string(data))
	}
	f.Fuzz(func(t *testing.T, which uint8, output string) {
		command := fuzzCommands[int(which)%len(fuzzCommands)]
		start := time.Now()
		_, parsed, err := iosparse.Parse(command, output)
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("Parse(%q) took %v on %d bytes", command, elapsed, len(output))
		}
		if err != nil {
			var pe *iosparse.ParserError
			var se *iosparse.SchemaError
			if !errors.Is(err, iosparse.ErrEmpty) && !errors.As(err, &pe) && !errors.As(err, &se) {
				t.Fatalf("Parse(%q) returned an untyped error: %v", command, err)
			}
			return
		}
		if _, err := json.Marshal(parsed); err != nil {
			t.Fatalf("Parse(%q) result does not encode: %v", command, err)
		}
	})
}
