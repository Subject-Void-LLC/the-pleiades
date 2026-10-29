// Tests for the disk sampler: its account of the readings it is fed.
package headroom

import (
	"testing"
	"time"
)

// TestDiskWatch_ChargesTheLargestFall: each reading's largest fall below
// the start on any one path is charged to every package running then (two
// paths on one file system fall together and are not summed), the least
// free is kept per path, and a path with no start reading is ignored.
func TestDiskWatch_ChargesTheLargestFall(t *testing.T) {
	start := map[string]float64{"/src": 100, "/tmp": 100}
	w := StartDiskWatch([]string{"/src", "/tmp", "/other"}, start, func(string) (float64, error) { return 100, nil }, time.Hour)
	w.Began("a")
	w.Sample(map[string]float64{"/src": 99.5, "/tmp": 99.5, "/other": 10})
	w.Began("b")
	w.Sample(map[string]float64{"/src": 98, "/tmp": 98})
	w.Ended("a")
	w.Sample(map[string]float64{"/src": 99, "/tmp": 97})
	r := w.Finish()
	if r.PeakMB["a"] != 2048 || r.PeakMB["b"] != 3072 {
		t.Errorf("peaks = %v, want a charged 2 GB and b 3 GB", r.PeakMB)
	}
	if r.MinFreeGB["/src"] != 98 || r.MinFreeGB["/tmp"] != 97 || r.StartFreeGB["/tmp"] != 100 || r.Samples != 3 {
		t.Errorf("report = %+v", r)
	}
	if _, ok := r.MinFreeGB["/other"]; ok {
		t.Error("a path with no start reading was reported")
	}
	if again := w.Finish(); again.Samples != 3 {
		t.Errorf("a second Finish = %+v", again)
	}
}

// TestDiskWatch_LoopReads: the loop reads each path on its interval, through
// the reader it was given, and a failed reading is left out.
func TestDiskWatch_LoopReads(t *testing.T) {
	reads := make(chan string, 64)
	read := func(p string) (float64, error) {
		reads <- p
		if p == "/bad" {
			return 0, errorf("no such file system")
		}
		return 50, nil
	}
	w := StartDiskWatch([]string{"/src", "/bad"}, map[string]float64{"/src": 60, "/bad": 60}, read, 5*time.Millisecond)
	w.Began("x")
	deadline := time.After(5 * time.Second)
	seen := 0
	for seen < 4 {
		select {
		case <-reads:
			seen++
		case <-deadline:
			t.Fatal("the loop never read")
		}
	}
	r := w.Finish()
	if r.Samples < 1 || r.PeakMB["x"] != 10*1024 || r.MinFreeGB["/src"] != 50 || r.MinFreeGB["/bad"] != 60 {
		t.Errorf("report = %+v", r)
	}
}
