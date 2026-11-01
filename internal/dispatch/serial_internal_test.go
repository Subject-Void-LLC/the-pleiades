// Tests of the serial gate's rule on its own.
package dispatch

import "testing"

// TestGateOf: the lowest batch with an unfinished device is current; an
// earlier failed batch stops the rollout; a failure in the current batch does
// not stop it until that batch finishes; nothing unfinished is done.
func TestGateOf(t *testing.T) {
	for name, tc := range map[string]struct {
		rows []SerialRow
		want SerialGate
	}{
		"no batches": {nil, SerialGate{StoppedBy: -1}},
		"first running": {[]SerialRow{{Batch: 0, Unfinished: true}, {Batch: 1, Unfinished: true}},
			SerialGate{Batched: true, Current: 0, Size: 1, StoppedBy: -1}},
		"second after the first": {[]SerialRow{{Batch: 0}, {Batch: 0}, {Batch: 1, Unfinished: true}, {Batch: 1, Unfinished: true}},
			SerialGate{Batched: true, Current: 1, Size: 2, StoppedBy: -1}},
		"failure in the current batch": {[]SerialRow{{Batch: 0, Failed: true}, {Batch: 0, Unfinished: true}, {Batch: 1, Unfinished: true}},
			SerialGate{Batched: true, Current: 0, Size: 2, StoppedBy: -1}},
		"failure in a finished batch": {[]SerialRow{{Batch: 0, Failed: true}, {Batch: 0}, {Batch: 1, Unfinished: true}},
			SerialGate{Batched: true, Current: 1, Size: 1, StoppedBy: 0}},
		"all finished": {[]SerialRow{{Batch: 0}, {Batch: 1, Failed: true}},
			SerialGate{Batched: true, Done: true, StoppedBy: -1}},
	} {
		if got := gateOf(tc.rows); got != tc.want {
			t.Errorf("%s: %+v, want %+v", name, got, tc.want)
		}
	}
}
