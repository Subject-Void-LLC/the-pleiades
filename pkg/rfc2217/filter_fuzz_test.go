package rfc2217

import (
	"bytes"
	"testing"
)

// FuzzIACFilter proves iacFilter.feed never panics against arbitrary
// bytes -- RFC 2217 option framing is remote-controlled binary data (a
// compromised or buggy access server, or a machine-in-the-middle), so
// this is what proves a truncated subnegotiation, an unterminated IAC
// SB, or a run of payload bytes long past maxSBPayload with no
// terminator at all fails closed (an error or a partial parse) rather
// than crashing the client reading it, or growing without bound. Framing
// here is IAC-SE-terminated, not length-prefixed -- there is no length
// field to lie about -- so the unbounded-accumulation risk this fuzz
// target actually exercises is an attacker withholding the terminator
// rather than one supplying a false length.
func FuzzIACFilter(f *testing.F) {
	seeds := [][]byte{
		{},
		{iacByte},
		{iacByte, doByte},
		{iacByte, doByte, comPortOption},
		{iacByte, sbByte},
		{iacByte, sbByte, comPortOption},
		{iacByte, sbByte, comPortOption, cmdSetBaudRate},
		{iacByte, sbByte, comPortOption, cmdSetBaudRate, 0x00, 0x00, 0x25, 0x80, iacByte, seByte},
		{iacByte, sbByte, comPortOption, iacByte},                                           // unterminated, mid-escape
		{iacByte, sbByte, comPortOption, iacByte, iacByte},                                  // escaped 0xFF, then nothing
		{iacByte, sbByte, comPortOption, iacByte, 0x05},                                     // malformed: IAC then neither SE nor IAC
		{iacByte, iacByte, iacByte, iacByte, iacByte},                                       // odd/even runs of literal escapes
		bytes.Repeat([]byte{iacByte, sbByte, comPortOption}, 200),                           // repeated SB opens, never closed
		{iacByte, sbByte, comPortOption, cmdSetBaudRate, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF},      // length-looking bytes with no real terminator
		append([]byte{iacByte, sbByte, comPortOption}, bytes.Repeat([]byte{0x41}, 1000)...), // payload past maxSBPayload, never terminated
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("iacFilter.feed panicked on %v: %v", raw, r)
			}
		}()
		f := &iacFilter{}
		var reply bytes.Buffer
		_, _, _, _ = f.feed(&reply, raw)
	})
}
