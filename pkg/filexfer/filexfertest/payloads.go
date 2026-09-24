// Package filexfertest holds the fixtures every filexfer.Store
// implementation's tests share, so each protocol is attacked with the
// same payloads and held to the same outcomes rather than to its own
// author's idea of what an escape looks like.
//
// It is test support: nothing in a shipped binary imports it.
package filexfertest

import "strings"

// EscapePayload is one attempt to name a file outside a transfer root,
// or to smuggle something past a remote path guard.
type EscapePayload struct {
	// Name labels the payload in a test's output.
	Name string
	// Leaf is the runbook-supplied path under the transfer root.
	Leaf string
}

// EscapePayloads is every leaf filexfer.Resolve must refuse. Each one
// is a known way a remote path guard has been bypassed somewhere: dot
// segments, absolute and doubled slashes, NUL truncation, overlong
// UTF-8, Windows separators on a POSIX controller and the reverse, a
// newline that would forge a line of SCP's wire header, and invisible
// Unicode that would make a log lie about which file was written.
//
// Invisible characters are written as byte escapes, never literally, so
// this file stays free of the control characters internal/archtest
// refuses in Go source.
var EscapePayloads = []EscapePayload{
	{Name: "dot dot", Leaf: "../etc/passwd"},
	{Name: "dot dot after a real segment", Leaf: "a/../../etc/passwd"},
	{Name: "dot dot alone", Leaf: ".."},
	{Name: "dot alone", Leaf: "."},
	{Name: "dot segment", Leaf: "a/./b"},
	{Name: "absolute", Leaf: "/etc/passwd"},
	{Name: "double slash absolute", Leaf: "//etc/passwd"},
	{Name: "double slash inside", Leaf: "a//b"},
	{Name: "trailing slash", Leaf: "a/"},
	{Name: "embedded NUL", Leaf: "a\x00/../../etc/passwd"},
	{Name: "NUL truncation", Leaf: "safe.txt\x00../../etc/passwd"},
	{Name: "overlong UTF-8 dot dot", Leaf: "\xc0\xae\xc0\xae/etc/passwd"},
	{Name: "overlong UTF-8 slash", Leaf: "..\xc0\xafetc"},
	{Name: "Windows dot dot on a POSIX controller", Leaf: `..\..\etc\passwd`},
	{Name: "Windows separator inside a segment", Leaf: `a\..\..\b`},
	{Name: "Windows drive", Leaf: `C:\Windows\System32`},
	{Name: "UNC path", Leaf: `\\server\share`},
	{Name: "POSIX dot dot the way a Windows controller would join it", Leaf: `a/..\../etc`},
	{Name: "newline forging an SCP header", Leaf: "a\nC0644 1 x"},
	{Name: "carriage return", Leaf: "a\rb"},
	{Name: "tab", Leaf: "a\tb"},
	{Name: "DEL", Leaf: "a\x7fb"},
	{Name: "C1 control NEL", Leaf: "a\xc2\x85b"},
	{Name: "right-to-left override", Leaf: "invoice\xe2\x80\xaetxt.exe"},
	{Name: "zero-width joiner", Leaf: "a\xe2\x80\x8db"},
	{Name: "empty", Leaf: ""},
	{Name: "overlong segment", Leaf: strings.Repeat("a", 256)},
	{Name: "overlong path", Leaf: strings.Repeat("a/", 2048) + "a"},
}

// AllowedLeaves is every leaf filexfer.Resolve must accept, each chosen
// because a guard that is too eager would refuse it: names that merely
// contain dots, that look encoded, that hold a colon or a space, or
// that are not ASCII.
var AllowedLeaves = []string{
	"a",
	"a/b/c.txt",
	".env",
	"a..b",
	"...",
	"..hidden",
	"%2e%2e",
	"timestamp-2026-09-23T10:00:00",
	"file with spaces",
	"\xe6\x97\xa5\xe6\x9c\xac.txt",
	strings.Repeat("a", 255),
}
