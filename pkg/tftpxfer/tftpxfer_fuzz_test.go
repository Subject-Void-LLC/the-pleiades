package tftpxfer

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// FuzzValidateFilename checks what every verdict promises, not only that
// validateFilename does not panic. A remote filename reaches an
// unauthenticated server inside a request with NUL-separated fields
// packed into a fixed buffer, so a wrong acceptance is an injected mode or
// option, a panic inside pin/tftp, or a different file requested.
//
// An accepted name must fit one request beside the largest block size
// option, be non-empty and valid UTF-8, hold no control or format
// character, be relative, hold no ":", and have no ".." segment under
// either separator. Each is checked here in a different form from
// validateFilename's own, so the two cannot share one mistake.
//
// A refused name must be refused with ErrInvalidFilename, in a message
// that is valid UTF-8, holds no control or format character, and stays
// bounded however long the name was, so any refusal is safe to log.
func FuzzValidateFilename(f *testing.F) {
	seeds := []string{
		"",
		"firmware.bin",
		"vendor/switch1/config.txt",
		"vendor\\switch1\\config.txt",
		"../etc/passwd",
		"..\\windows\\system32",
		"a/../../b",
		"/etc/passwd",
		`C:\Windows`,
		"a:b",
		"....//....//etc/passwd",
		"a/b/../../../c",
		"..",
		".",
		"~/../../etc/passwd",
		// A NUL ends the filename field, so what follows it would be read
		// as the transfer mode, then as option names and values.
		"\x00",
		"a\x00../../etc/passwd",
		"firmware.bin\x00netascii",
		"fw.bin\x00blksize\x0065464",
		"config.txt\n",
		"a\x7fb",
		"a\xc2\x85b",             // NEL, a C1 control
		"\xe2\x80\xaegnp.exe",    // right-to-left override
		"a\xe2\x80\x8bb",         // zero-width space
		"\xef\xbb\xbfconfig.txt", // byte order mark
		"\xff",
		"\xc0\xae\xc0\xae/x", // overlong encodings of "."
		strings.Repeat("a", MaxFilenameBytes),
		strings.Repeat("a", MaxFilenameBytes+1),
		strings.Repeat("a", 600),
		strings.Repeat("\xc3\xa9", 246) + "a", // 493 bytes in 247 runes
		strings.Repeat("\xc3\xa9", 247),       // 494 bytes in 247 runes
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, filename string) {
		err := validateFilename(filename)
		if err != nil {
			if !errors.Is(err, ErrInvalidFilename) {
				t.Fatalf("validateFilename(%q) refused with %v, which does not wrap ErrInvalidFilename", filename, err)
			}
			msg := err.Error()
			if !utf8.ValidString(msg) || strings.IndexFunc(msg, isControlOrFormat) >= 0 {
				t.Fatalf("refusing %q printed a message unsafe to log: %q", filename, msg)
			}
			// %q spends at most four bytes on one byte of the name.
			if limit := 4*MaxFilenameBytes + 128; len(msg) > limit {
				t.Fatalf("refusing a %d-byte name printed %d bytes, over %d", len(filename), len(msg), limit)
			}
			return
		}

		if got := requestLength(filename); got > requestBytes {
			t.Fatalf("accepted %q packs into a %d-byte request, over pin/tftp's %d", filename, got, requestBytes)
		}
		if filename == "" {
			t.Fatal("accepted an empty name")
		}
		if !utf8.ValidString(filename) {
			t.Fatalf("accepted %q, which is not valid UTF-8", filename)
		}
		if i := strings.IndexFunc(filename, isControlOrFormat); i >= 0 {
			t.Fatalf("accepted %q, which holds a control or format character at byte %d", filename, i)
		}
		if filename[0] == '/' || filename[0] == '\\' {
			t.Fatalf("accepted %q, an absolute path", filename)
		}
		if strings.ContainsRune(filename, ':') {
			t.Fatalf("accepted %q, which holds \":\"", filename)
		}
		for _, seg := range strings.Split(strings.ReplaceAll(filename, `\`, "/"), "/") {
			if seg == ".." {
				t.Fatalf("accepted %q, which holds a \"..\" segment", filename)
			}
		}
	})
}

// isControlOrFormat names the refused characters by their Unicode
// categories, where validateFilename uses unicode.IsControl.
func isControlOrFormat(r rune) bool {
	return unicode.In(r, unicode.Cc, unicode.Cf)
}

// requestLength counts, field by field, the read or write request pin/tftp
// packs for filename with the largest block size option: a two-byte
// opcode, then each field followed by its NUL.
func requestLength(filename string) int {
	n := 2
	for _, field := range []string{filename, mode, "blksize", strconv.Itoa(MaxBlockSize)} {
		n += len(field) + 1
	}
	return n
}
