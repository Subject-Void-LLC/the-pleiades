// Fuzz test of the tracepath parse: a device's answer, never trusted.
package net

import "testing"

// FuzzParseTracepath: the parse never panics, every hop is a word, a path
// is read as a loop only from addresses that answered, and a return count
// is never negative.
func FuzzParseTracepath(f *testing.F) {
	f.Add(" 1?: [LOCALHOST] pmtu 1500\n 1:  10.0.0.1 0.1ms\n 2:  no reply\n 3:  10.0.0.9 0.2ms reached\n")
	f.Add(labAsymmetricTrace)
	f.Fuzz(func(t *testing.T, out string) {
		p := parseTracepath(out)
		answered := false
		for _, h := range p.hops {
			if h.addr == "" {
				t.Fatal("an empty hop")
			}
			if h.back < 0 {
				t.Fatalf("hop %d came back over %d hops", h.n, h.back)
			}
			answered = answered || h.addr != "*"
		}
		if p.state() == pathLoop && !answered {
			t.Fatal("a loop read from hops that did not answer")
		}
		if o, b, ok := p.returnCounts(); ok && (o < 0 || b < 0) {
			t.Fatalf("return counts %d %d", o, b)
		}
	})
}
