// Tests of serial: batch sizes and the batches they split devices into,
// against Ansible's own rounding (pct_to_int, _get_serialized_batches).
package engine_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory/inventorytest"
)

// sizesOf returns each batch's length.
func sizesOf[T any](batches [][]T) string {
	var out []string
	for _, b := range batches {
		out = append(out, fmt.Sprint(len(b)))
	}
	return strings.Join(out, ",")
}

// TestBatches_RoundAsAnsibleDoes: a percentage of the whole count rounds
// down and is never under one device, the last size repeats, a size larger
// than what is left takes the rest, no sizes is one batch, and order is kept.
func TestBatches_RoundAsAnsibleDoes(t *testing.T) {
	ten := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	for _, tc := range []struct {
		serial string
		items  []int
		want   string
	}{
		{"3", ten, "3,3,3,1"},
		{`"30%"`, ten, "3,3,3,1"},
		{`"1%"`, ten, "1,1,1,1,1,1,1,1,1,1"},
		{`[1, "50%"]`, ten, "1,5,4"},
		{`[1, 2, 3]`, ten, "1,2,3,3,1"},
		{"5", []int{0, 1}, "2"},
		{`"100%"`, ten, "10"},
	} {
		var sizes engine.SerialList
		if err := yaml.Unmarshal([]byte(tc.serial), &sizes); err != nil {
			t.Fatalf("%s: %v", tc.serial, err)
		}
		batches := engine.Batches(tc.items, sizes)
		if got := sizesOf(batches); got != tc.want {
			t.Errorf("serial %s over %d: batches %s, want %s", tc.serial, len(tc.items), got, tc.want)
		}
		var flat []int
		for _, b := range batches {
			flat = append(flat, b...)
		}
		if fmt.Sprint(flat) != fmt.Sprint(tc.items) {
			t.Errorf("serial %s reordered or lost devices: %v", tc.serial, flat)
		}
	}
	if got := engine.Batches(ten, nil); sizesOf(got) != "10" {
		t.Errorf("no serial: %s", sizesOf(got))
	}
	if got := engine.Batches([]int{}, engine.SerialList{{N: 2}}); got != nil {
		t.Errorf("no devices: %v", got)
	}
}

// TestSerialList_RefusesWhatIsNotABatchSize, in YAML and JSON alike, and
// writes back what was read.
func TestSerialList_RefusesWhatIsNotABatchSize(t *testing.T) {
	for _, bad := range []string{"0", "-1", "abc", `"150%"`, `"0%"`, `"{{ vars.n }}"`, "[]", "{a: 1}", "[[1]]"} {
		var sizes engine.SerialList
		if err := yaml.Unmarshal([]byte(bad), &sizes); err == nil {
			t.Errorf("YAML serial %s was accepted: %v", bad, sizes)
		}
	}
	for _, bad := range []string{"0", `"abc"`, "[]", `{"a": 1}`, "[true]"} {
		var sizes engine.SerialList
		if err := json.Unmarshal([]byte(bad), &sizes); err == nil {
			t.Errorf("JSON serial %s was accepted: %v", bad, sizes)
		}
	}
	var sizes engine.SerialList
	if err := json.Unmarshal([]byte(`[1, "25%"]`), &sizes); err != nil {
		t.Fatal(err)
	}
	if out, _ := json.Marshal(sizes); string(out) != `["1","25%"]` {
		t.Errorf("written back as %s", out)
	}
}

// FuzzSerialList holds any serial: text to one of two outcomes: refused, or
// sizes every one of which is at least one device (and a percentage at most
// 100), whose batches over any device count place every device exactly once,
// in order, with no empty batch. Seeded with the shapes a parser most often
// gets wrong: signs, exponents, hex, a full-width digit, a doubled or lone %,
// overflow, and spaces around the %.
func FuzzSerialList(f *testing.F) {
	for _, seed := range []string{"3", `"30%"`, `" 5 %"`, "1e2", "0x10", "+3", `"３"`, `"50%%"`, `"%"`, `"2147483648%"`, "9223372036854775807", `[1, "50%"]`, "-0", `"01%"`} {
		f.Add(seed, uint8(7))
	}
	f.Fuzz(func(t *testing.T, text string, count uint8) {
		var sizes engine.SerialList
		if err := yaml.Unmarshal([]byte(text), &sizes); err != nil {
			return
		}
		for _, s := range sizes {
			if s.N < 1 || (s.Percent && s.N > 100) {
				t.Fatalf("serial %q gave size %v", text, s)
			}
		}
		items := make([]int, int(count))
		for i := range items {
			items[i] = i
		}
		next := 0
		for _, batch := range engine.Batches(items, sizes) {
			if len(batch) == 0 {
				t.Fatalf("serial %q over %d gave an empty batch", text, count)
			}
			for _, item := range batch {
				if item != next {
					t.Fatalf("serial %q over %d placed %d where %d belongs", text, count, item, next)
				}
				next++
			}
		}
		if next != len(items) {
			t.Fatalf("serial %q over %d placed %d devices", text, count, next)
		}
	})
}

// TestBatchDevices_OrdersByName: batches follow device names, whatever order
// the resolver returned them in.
func TestBatchDevices_OrdersByName(t *testing.T) {
	var devices []inventory.InventoryItem
	for _, pair := range [][2]string{{"id-c", "s3"}, {"id-a", "s4"}, {"id-d", "s1"}, {"id-b", "s2"}} {
		devices = append(devices, &inventorytest.Stub{StubID: inventory.DeviceID(pair[0]), StubName: pair[1]})
	}
	var got []string
	for _, batch := range engine.BatchDevices(devices, engine.SerialList{{N: 2}}) {
		var names []string
		for _, d := range batch {
			names = append(names, d.Name())
		}
		got = append(got, strings.Join(names, "+"))
	}
	if strings.Join(got, ",") != "s1+s2,s3+s4" {
		t.Errorf("batches %v, want s1+s2,s3+s4", got)
	}
}
