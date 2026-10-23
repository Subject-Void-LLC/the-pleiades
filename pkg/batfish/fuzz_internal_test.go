// Fuzz test of the answer decode: Batfish's words, never trusted.
package batfish

import "testing"

// FuzzDecodeAnswer: an answer never panics the decode, and a decoded table
// always has rows to range over.
func FuzzDecodeAnswer(f *testing.F) {
	f.Add([]byte(`{"status":"SUCCESS","answerElements":[{"metadata":{"columnMetadata":[{"name":"Node"}]},"rows":[{"Node":"r1"}]}]}`))
	f.Add([]byte(`{"status":"FAILURE","summary":"x"}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		table, err := decodeAnswer("q", data)
		if err == nil && table.Rows == nil {
			t.Fatal("a decoded table with nil rows")
		}
	})
}
