// Package vmsize_test holds the tests for the T-shirt size table: every
// name finds its size, the sizes grow in order, and anything else is
// refused.
package vmsize_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/vmsize"
)

// TestLookupNamesEverySize proves each name finds its own size and that
// Of finds the same size back from its numbers.
func TestLookupNamesEverySize(t *testing.T) {
	names := vmsize.Names()
	if strings.Join(names, ",") != "xsmall,small,medium,large,xlarge" {
		t.Fatalf("Names() = %v", names)
	}
	for _, name := range names {
		s, err := vmsize.Lookup(name)
		if err != nil {
			t.Fatalf("Lookup(%q): %v", name, err)
		}
		if s.Name != name {
			t.Fatalf("Lookup(%q) = %+v", name, s)
		}
		back, ok := vmsize.Of(s.CPUs, s.MemoryMB)
		if !ok || back != s {
			t.Fatalf("Of(%d, %d) = %+v, %v; want %+v", s.CPUs, s.MemoryMB, back, ok, s)
		}
	}
}

// TestSizesGrow proves every size has more memory than the one before it,
// and never fewer CPUs, so the order the names are listed in is the order
// of the machines.
func TestSizesGrow(t *testing.T) {
	all := vmsize.All()
	for i := 1; i < len(all); i++ {
		if all[i].MemoryMB <= all[i-1].MemoryMB || all[i].CPUs < all[i-1].CPUs {
			t.Fatalf("%v does not follow %v", all[i], all[i-1])
		}
	}
	all[0].Name = "changed"
	if vmsize.All()[0].Name != "xsmall" {
		t.Fatal("All returned the package's own table")
	}
}

// TestLookupRefusesOtherNames proves an abbreviation, a capital or an
// unknown name is refused, naming the sizes there are.
func TestLookupRefusesOtherNames(t *testing.T) {
	for _, name := range []string{"", "m", "Medium", "xxlarge", " small"} {
		_, err := vmsize.Lookup(name)
		if err == nil {
			t.Fatalf("Lookup(%q) was accepted", name)
		}
		if !strings.Contains(err.Error(), "xsmall, small, medium, large, xlarge") {
			t.Fatalf("Lookup(%q) = %v; want the list of sizes", name, err)
		}
	}
}

// TestOfFindsNoSizeForOtherNumbers proves a machine between sizes has
// none.
func TestOfFindsNoSizeForOtherNumbers(t *testing.T) {
	for _, c := range []struct{ cpus, memoryMB int }{{1, 1536}, {2, 2048}, {4, 4096}, {0, 0}} {
		if s, ok := vmsize.Of(c.cpus, c.memoryMB); ok {
			t.Fatalf("Of(%d, %d) = %v", c.cpus, c.memoryMB, s)
		}
	}
}

// TestStringReadsAsAMachine proves the form a message names a size in.
func TestStringReadsAsAMachine(t *testing.T) {
	small, _ := vmsize.Lookup("small")
	medium, _ := vmsize.Lookup("medium")
	if got := small.String(); got != "small (1 CPU, 2048 MB)" {
		t.Fatalf("small = %q", got)
	}
	if got := medium.String(); got != "medium (2 CPUs, 4096 MB)" {
		t.Fatalf("medium = %q", got)
	}
}

// TestDescribeListsEverySize proves the sentence a method's documentation
// names the sizes in.
func TestDescribeListsEverySize(t *testing.T) {
	want := "xsmall is 1 CPU and 1024 MB, small 1 CPU and 2048 MB, medium 2 CPUs and 4096 MB, large 4 CPUs and 8192 MB, and xlarge 8 CPUs and 16384 MB"
	if got := vmsize.Describe(); got != want {
		t.Fatalf("Describe() = %q\nwant %q", got, want)
	}
}
