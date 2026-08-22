package tftpxfer

import "testing"

// FuzzValidateFilename proves validateFilename never panics against
// arbitrary filenames, and specifically that every ".." traversal
// attempt and every absolute path it is seeded with is refused, not
// silently accepted -- an unauthenticated network endpoint (TFTP has no
// authentication at all, this package's own doc comment) feeding a
// filename straight into a client request is exactly the shape
// FAILURE_PATTERNS.md #78 already found once for a runbook id reaching
// filepath.Join.
func FuzzValidateFilename(f *testing.F) {
	seeds := []string{
		"",
		"firmware.bin",
		"vendor/switch1/config.txt",
		"../etc/passwd",
		"..\\windows\\system32",
		"a/../../b",
		"/etc/passwd",
		`C:\Windows`,
		"....//....//etc/passwd",
		"a/b/../../../c",
		"\x00",
		"a\x00../../etc/passwd",
		"..",
		".",
		"~/../../etc/passwd",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, filename string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("validateFilename panicked on %q: %v", filename, r)
			}
		}()
		_ = validateFilename(filename)
	})
}
