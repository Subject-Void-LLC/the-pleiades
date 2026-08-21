package filters_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

// FuzzCIDRToNetmask, FuzzNetmaskToCIDR and the sibling targets in this
// file and mac_fuzz_test.go/interfacename_fuzz_test.go are Phase 51's
// own Fuzz/Stress checklist item: "a Fuzz target per parsing-shaped
// function (CIDR, netmask, MAC, interface name)", matching Phase 34's
// own FuzzImportTasksPath bar (tens of thousands of executions, zero
// panics). None of these functions can return an error, so there is
// nothing to assert about the *result*; the only property under test is
// that no malformed input, however constructed, ever panics.
func FuzzCIDRToNetmask(f *testing.F) {
	seeds := []string{"10.0.0.0/24", "0.0.0.0/0", "garbage", "", "10.0.0.0", "2001:db8::/32", "10.0.0.0/33", "10.0.0.0/-1"}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, cidr string) {
		filters.CIDRToNetmask(cidr)
	})
}

func FuzzNetmaskToCIDR(f *testing.F) {
	seeds := []string{"255.255.255.0", "0.0.0.0", "255.255.255.255", "255.255.255.1", "garbage", "", "::"}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, netmask string) {
		filters.NetmaskToCIDR(netmask)
	})
}

func FuzzWildcardMask(f *testing.F) {
	seeds := []string{"10.0.0.0/24", "garbage", "", "10.0.0.0/0"}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, cidr string) {
		filters.WildcardMask(cidr)
	})
}

func FuzzBroadcastAddress(f *testing.F) {
	seeds := []string{"10.0.0.0/24", "garbage", "", "10.0.0.0/32"}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, cidr string) {
		filters.BroadcastAddress(cidr)
	})
}

func FuzzSubnetSplit(f *testing.F) {
	f.Add("10.0.0.0/24", 26)
	f.Add("garbage", 26)
	f.Add("0.0.0.0/0", 32)
	f.Add("10.0.0.0/24", -1)
	f.Add("10.0.0.0/24", 999999)
	f.Fuzz(func(t *testing.T, cidr string, newPrefix int) {
		// The only real hazard here is an unbounded allocation from a
		// large newPrefix - prefix.Bits() shift; SubnetSplit's own
		// maxSubnetSplitCount cap is what this fuzz target is actually
		// proving holds under adversarial input, not just "no panic".
		filters.SubnetSplit(cidr, newPrefix)
	})
}

func FuzzIPToInt(f *testing.F) {
	seeds := []string{"0.0.0.1", "255.255.255.255", "garbage", "", "::1"}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, ip string) {
		filters.IPToInt(ip)
	})
}

func FuzzClassifyIP(f *testing.F) {
	seeds := []string{"10.0.0.5", "8.8.8.8", "127.0.0.1", "169.254.1.1", "224.0.0.1", "::1", "garbage", ""}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, ip string) {
		filters.ClassifyIP(ip)
	})
}
