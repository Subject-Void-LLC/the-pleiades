// Package wincpu_test holds the tests for reading CPU sets: the lab
// host's captured output, uniform and multi-group hosts, and the output a
// parse refuses.
package wincpu_test

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wincpu"
)

// captured is Script's output on the lab host, an Intel Core Ultra 7 265KF
// (8 P-cores and 12 E-cores, no hyperthreads) under Windows 11.
func captured(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("testdata/cpusets-core-ultra-7-265kf.txt")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestParse_HybridHost proves the captured host reads as the P-cores and
// E-cores Windows' own efficiency classes name, with the mask a process's
// ProcessorAffinity takes.
func TestParse_HybridHost(t *testing.T) {
	topo, err := wincpu.Parse(strings.ReplaceAll(captured(t), "\n", "\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(topo.Processors) != 20 || topo.Cores() != 20 || !topo.Hybrid() {
		t.Fatalf("%d processors on %d cores, hybrid %v", len(topo.Processors), topo.Cores(), topo.Hybrid())
	}
	if got := wincpu.Indexes(topo.Performance()); !reflect.DeepEqual(got, []int{0, 1, 6, 7, 8, 9, 18, 19}) {
		t.Errorf("performance %v", got)
	}
	if got := wincpu.Indexes(topo.Efficiency()); !reflect.DeepEqual(got, []int{2, 3, 4, 5, 10, 11, 12, 13, 14, 15, 16, 17}) {
		t.Errorf("efficiency %v", got)
	}
	if mask, ok := topo.PerformanceMask(); !ok || mask != 0xC03C3 {
		t.Errorf("mask %#x, %v", mask, ok)
	}
	if p := topo.Processors[6]; p != (wincpu.Processor{Index: 6, Core: 6, EfficiencyClass: 1}) {
		t.Errorf("processor 6 %+v", p)
	}
}

// TestParse_UniformHost proves a host whose cores are all alike has every
// processor as a performance one and none as efficiency, and that two
// hyperthreads on one core count as one core.
func TestParse_UniformHost(t *testing.T) {
	topo, err := wincpu.Parse("cpuset lp=0 group=0 core=0 llc=0 numa=0 class=0 flags=0\n" +
		"cpuset lp=1 group=0 core=0 llc=0 numa=0 class=0 flags=1\nnoise\n")
	if err != nil {
		t.Fatal(err)
	}
	if topo.Hybrid() || topo.Cores() != 1 || len(topo.Performance()) != 2 || len(topo.Efficiency()) != 0 || !topo.Processors[1].Parked {
		t.Fatalf("%+v", topo)
	}
	if mask, ok := topo.PerformanceMask(); !ok || mask != 0x3 {
		t.Errorf("mask %#x, %v", mask, ok)
	}
}

// TestPerformanceMask_TwoGroups proves a host with more than one processor
// group gets no mask, since one mask cannot name processors in two.
func TestPerformanceMask_TwoGroups(t *testing.T) {
	topo, err := wincpu.Parse("cpuset lp=0 group=0 core=0 llc=0 numa=0 class=0 flags=0\n" +
		"cpuset lp=0 group=1 core=0 llc=1 numa=1 class=0 flags=0\n")
	if err != nil {
		t.Fatal(err)
	}
	if mask, ok := topo.PerformanceMask(); ok {
		t.Errorf("two groups gave mask %#x", mask)
	}
	if !reflect.DeepEqual(topo.Groups(), []int{0, 1}) || topo.Cores() != 2 {
		t.Errorf("groups %v, cores %d", topo.Groups(), topo.Cores())
	}
}

func TestParse_Refusals(t *testing.T) {
	good := "cpuset lp=0 group=0 core=0 llc=0 numa=0 class=0 flags=0"
	for why, tt := range map[string]struct{ text, want string }{
		"nothing":          {"", "no processors"},
		"a missing field":  {"cpuset lp=0 group=0 core=0 llc=0 numa=0 class=0", "lacks one of"},
		"an unknown field": {good + " speed=5", `"speed=5"`},
		"a bare word":      {good + " parked", `"parked"`},
		"not a number":     {strings.Replace(good, "class=0", "class=x", 1), `class="x"`},
		"out of range":     {strings.Replace(good, "lp=0", "lp=256", 1), `lp="256"`},
		"negative":         {strings.Replace(good, "core=0", "core=-1", 1), `core="-1"`},
		"listed twice":     {good + "\n" + good, "listed twice"},
	} {
		if _, err := wincpu.Parse(tt.text); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: %v, want one mentioning %q", why, err, tt.want)
		}
	}
}

// TestPerformanceMask_IndexBeyond63 proves a processor a 64-bit mask
// cannot hold gives no mask rather than a wrong one.
func TestPerformanceMask_IndexBeyond63(t *testing.T) {
	topo, err := wincpu.Parse("cpuset lp=64 group=0 core=0 llc=0 numa=0 class=0 flags=0\n")
	if err != nil {
		t.Fatal(err)
	}
	if mask, ok := topo.PerformanceMask(); ok {
		t.Errorf("gave mask %#x", mask)
	}
}

// TestScriptShape proves the script keeps the parts the method relies on:
// its language-mode refusal and exit code, and the one API it calls.
func TestScriptShape(t *testing.T) {
	for _, want := range []string{"exit 20", "language=$mode", "GetSystemCpuSetInformation", "cpuset lp={0} group={1} core={2} llc={3} numa={4} class={5} flags={6}"} {
		if !strings.Contains(wincpu.Script, want) {
			t.Errorf("the script lost %q", want)
		}
	}
	if wincpu.ExitConstrained != 20 {
		t.Errorf("ExitConstrained is %d", wincpu.ExitConstrained)
	}
}
