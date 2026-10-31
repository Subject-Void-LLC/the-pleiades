// A fuzz test of handlers: random notifying tasks against random handlers,
// each run through the real Builder and Executor and held to the rule.
package engine_test

import (
	"fmt"
	"strings"
	"testing"
)

// FuzzNotify builds a runbook from the fuzzer's bytes: up to six tasks, each
// changing or not, skipped or not, failing or not, and notifying a subset of
// up to four handlers. On each device every handler runs at most once, only
// when some notifying task before it ran without failing and changed
// something, after every task, in definition order; and no handler runs at
// all when a failure ended the run.
func FuzzNotify(f *testing.F) {
	f.Add([]byte{0x31, 0x05, 0x12, 0x60})
	f.Add([]byte{0xff, 0x00, 0xa5})
	f.Add([]byte{0x01, 0x82, 0x07})
	f.Fuzz(func(t *testing.T, data []byte) {
		// A handler count and at least one task.
		if len(data) < 2 {
			return
		}
		handlers := int(data[0]%4) + 1
		data = data[1:]
		if len(data) > 6 {
			data = data[:6]
		}
		var b strings.Builder
		b.WriteString("id: fz\nhosts: all\ntasks:\n")
		notified := make([]bool, handlers)
		failed := false
		for i, d := range data {
			change, skip, fail := d&1 != 0, d&2 != 0, d&0x80 != 0 && !failed
			fmt.Fprintf(&b, "  - name: t%d\n    enginetest.touch:\n      id: t%d\n      change: %v\n      fail: %v\n", i, i, change, fail)
			if skip {
				b.WriteString("    when_cel: 1 == 2\n")
			}
			var names []string
			for h := 0; h < handlers; h++ {
				if d>>(2+h)&1 != 0 {
					names = append(names, fmt.Sprintf("h%d", h))
				}
			}
			if len(names) > 0 {
				fmt.Fprintf(&b, "    notify: [%s]\n", strings.Join(names, ", "))
			}
			if skip || failed {
				continue
			}
			if fail {
				failed = true
				continue
			}
			if change {
				for _, n := range names {
					var h int
					_, _ = fmt.Sscanf(n, "h%d", &h)
					notified[h] = true
				}
			}
		}
		b.WriteString("handlers:\n")
		for h := 0; h < handlers; h++ {
			fmt.Fprintf(&b, "  - name: h%d\n    enginetest.touch:\n      id: H%d\n", h, h)
		}

		log := registerTouch(t)
		if _, err := runHandlers(t, b.String()); err != nil {
			t.Fatalf("Run: %v\n%s", err, b.String())
		}
		var want []string
		if !failed {
			for h, on := range notified {
				if on {
					want = append(want, fmt.Sprintf("H%d", h))
				}
			}
		}
		for _, device := range []string{"d1", "d2"} {
			var got []string
			for _, id := range log.of(device) {
				if strings.HasPrefix(id, "H") {
					got = append(got, id)
				}
			}
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("%s ran handlers %v, want %v\n%s", device, got, want, b.String())
			}
		}
	})
}
