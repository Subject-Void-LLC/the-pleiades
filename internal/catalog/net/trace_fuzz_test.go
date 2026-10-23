// Fuzz test of the tracepath parse: a device's answer, never trusted.
package net

import "testing"

// FuzzParseTracepath: the parse never panics, and every hop is a word.
func FuzzParseTracepath(f *testing.F) {
	f.Add(" 1?: [LOCALHOST] pmtu 1500\n 1:  10.0.0.1 0.1ms\n 2:  no reply\n 3:  10.0.0.9 0.2ms reached\n")
	f.Fuzz(func(t *testing.T, out string) {
		hops, _ := parseTracepath(out)
		for _, h := range hops {
			if h == "" {
				t.Fatal("an empty hop")
			}
		}
	})
}
