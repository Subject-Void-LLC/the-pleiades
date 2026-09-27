// Tests for hashFrom, the part of Hash that turns random bytes into a
// salt. They live inside the package because only there can the random
// source be replaced, which is the one way to reach Hash's error path and
// to pin the byte-to-character mapping.
package shacrypt

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

// TestHashFrom_RandomReadFails proves a failure to read randomness, or a
// source that runs dry before 16 bytes, comes back as an error with the
// package prefix and never as a hash made from a partial salt.
func TestHashFrom_RandomReadFails(t *testing.T) {
	cause := errors.New("entropy unavailable")
	for _, tc := range []struct {
		name   string
		random io.Reader
		want   error
	}{
		{"read error", iotest.ErrReader(cause), cause},
		{"fifteen bytes then end of stream", bytes.NewReader(make([]byte, saltMax-1)), io.ErrUnexpectedEOF},
	} {
		got, err := hashFrom(tc.random, "password")
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: hashFrom error = %v, want it to wrap %v", tc.name, err, tc.want)
		}
		if got != "" || !strings.HasPrefix(err.Error(), "shacrypt: ") {
			t.Fatalf("%s: hashFrom = %q, %q; want no hash and a prefixed error", tc.name, got, err)
		}
	}
}

// TestHashFrom_SaltMapping proves each random byte picks the alphabet
// character its low six bits index, so bytes 0 to 15 and bytes 64 to 79
// both give the salt made of the alphabet's first 16 characters.
func TestHashFrom_SaltMapping(t *testing.T) {
	low := make([]byte, saltMax)
	high := make([]byte, saltMax)
	for i := range low {
		low[i] = byte(i)
		high[i] = byte(i + 64)
	}
	want, err := HashWithSalt("password", alphabet[:saltMax])
	if err != nil {
		t.Fatalf("HashWithSalt: %v", err)
	}
	for _, random := range [][]byte{low, high} {
		got, err := hashFrom(bytes.NewReader(random), "password")
		if err != nil {
			t.Fatalf("hashFrom: %v", err)
		}
		if got != want {
			t.Fatalf("hashFrom with bytes %v = %q, want %q", random, got, want)
		}
	}
}
