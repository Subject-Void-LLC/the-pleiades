package filters_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

func TestCIDRToNetmask(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"slash24", "10.0.0.0/24", "255.255.255.0"},
		{"slash0", "10.0.0.0/0", "0.0.0.0"},
		{"slash32", "10.0.0.1/32", "255.255.255.255"},
		{"slash16", "172.16.0.0/16", "255.255.0.0"},
		{"malformed", "not-a-cidr", ""},
		{"no_prefix_length", "10.0.0.0", ""},
		{"ipv6_rejected", "2001:db8::/32", ""},
		{"empty", "", ""},
		{"over_cap", strings.Repeat("1", filters.MaxInputBytes+1), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.CIDRToNetmask(tc.in); got != tc.want {
				t.Errorf("CIDRToNetmask(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNetmaskToCIDR(t *testing.T) {
	cases := []struct {
		name, in string
		want     int
	}{
		{"slash24", "255.255.255.0", 24},
		{"slash0", "0.0.0.0", 0},
		{"slash32", "255.255.255.255", 32},
		{"non_contiguous", "255.255.255.1", -1},
		{"malformed", "not-a-netmask", -1},
		{"ipv6_rejected", "::", -1},
		{"over_cap", strings.Repeat("1", filters.MaxInputBytes+1), -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.NetmaskToCIDR(tc.in); got != tc.want {
				t.Errorf("NetmaskToCIDR(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestWildcardMask(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"slash24", "10.0.0.0/24", "0.0.0.255"},
		{"slash0", "10.0.0.0/0", "255.255.255.255"},
		{"slash32", "10.0.0.1/32", "0.0.0.0"},
		{"malformed", "garbage", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.WildcardMask(tc.in); got != tc.want {
				t.Errorf("WildcardMask(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestBroadcastAddress(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"slash24", "10.0.0.0/24", "10.0.0.255"},
		{"slash30", "10.0.0.0/30", "10.0.0.3"},
		{"slash32", "10.0.0.5/32", "10.0.0.5"},
		{"malformed", "garbage", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.BroadcastAddress(tc.in); got != tc.want {
				t.Errorf("BroadcastAddress(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSubnetSplit(t *testing.T) {
	cases := []struct {
		name      string
		cidr      string
		newPrefix int
		want      []string
	}{
		{"split_24_into_26", "10.0.0.0/24", 26, []string{"10.0.0.0/26", "10.0.0.64/26", "10.0.0.128/26", "10.0.0.192/26"}},
		{"split_24_into_25", "10.0.0.0/24", 25, []string{"10.0.0.0/25", "10.0.0.128/25"}},
		{"newPrefix_not_longer", "10.0.0.0/24", 24, nil},
		{"newPrefix_shorter", "10.0.0.0/24", 20, nil},
		{"newPrefix_over_32", "10.0.0.0/24", 33, nil},
		{"malformed_cidr", "garbage", 26, nil},
		{"too_many_subnets", "0.0.0.0/0", 24, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filters.SubnetSplit(tc.cidr, tc.newPrefix)
			if len(got) != len(tc.want) {
				t.Fatalf("SubnetSplit(%q, %d) = %v, want %v", tc.cidr, tc.newPrefix, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("SubnetSplit(%q, %d)[%d] = %q, want %q", tc.cidr, tc.newPrefix, i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestSupernet(t *testing.T) {
	cases := []struct {
		name  string
		cidrs []string
		want  string
	}{
		{"two_halves", []string{"10.0.0.0/25", "10.0.0.128/25"}, "10.0.0.0/24"},
		{"adjacent_slash24s", []string{"10.0.0.0/25", "10.0.1.0/25"}, "10.0.0.0/23"},
		{"single", []string{"10.0.0.0/24"}, "10.0.0.0/24"},
		{"identical_twice", []string{"10.0.0.0/24", "10.0.0.0/24"}, "10.0.0.0/24"},
		{"middle_extends_only_min_then_only_max", []string{"10.0.1.0/24", "10.0.0.0/24", "10.0.2.0/24"}, "10.0.0.0/22"},
		{"empty", nil, ""},
		{"malformed_entry", []string{"10.0.0.0/24", "garbage"}, ""},
		{"ipv6_entry", []string{"10.0.0.0/24", "2001:db8::/32"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.Supernet(tc.cidrs); got != tc.want {
				t.Errorf("Supernet(%v) = %q, want %q", tc.cidrs, got, tc.want)
			}
		})
	}
}

func TestIPToInt(t *testing.T) {
	cases := []struct {
		name, in string
		want     int
	}{
		{"one", "0.0.0.1", 1},
		{"max", "255.255.255.255", 4294967295},
		{"zero", "0.0.0.0", 0},
		{"malformed", "garbage", -1},
		{"ipv6_rejected", "::1", -1},
		{"over_cap", strings.Repeat("1", filters.MaxInputBytes+1), -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.IPToInt(tc.in); got != tc.want {
				t.Errorf("IPToInt(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestIntToIP(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want string
	}{
		{"one", 1, "0.0.0.1"},
		{"max", 4294967295, "255.255.255.255"},
		{"zero", 0, "0.0.0.0"},
		{"negative", -1, ""},
		{"over_max", 4294967296, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.IntToIP(tc.in); got != tc.want {
				t.Errorf("IntToIP(%d) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIPToIntRoundTrip(t *testing.T) {
	ips := []string{"0.0.0.0", "10.0.0.5", "192.168.1.1", "255.255.255.255"}
	for _, ip := range ips {
		n := filters.IPToInt(ip)
		if got := filters.IntToIP(n); got != ip {
			t.Errorf("IntToIP(IPToInt(%q)) = %q, want %q", ip, got, ip)
		}
	}
}

func TestToIPv4MappedIPv6(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"basic", "10.0.0.5", "::ffff:10.0.0.5"},
		{"malformed", "garbage", ""},
		{"already_ipv6", "2001:db8::1", ""},
		{"over_cap", strings.Repeat("1", filters.MaxInputBytes+1), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.ToIPv4MappedIPv6(tc.in); got != tc.want {
				t.Errorf("ToIPv4MappedIPv6(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestFromIPv4MappedIPv6(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"basic", "::ffff:10.0.0.5", "10.0.0.5"},
		{"malformed", "garbage", ""},
		{"plain_ipv4_rejected", "10.0.0.5", ""},
		{"genuine_ipv6_rejected", "2001:db8::1", ""},
		{"over_cap", strings.Repeat("1", filters.MaxInputBytes+1), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.FromIPv4MappedIPv6(tc.in); got != tc.want {
				t.Errorf("FromIPv4MappedIPv6(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIPv4MappedIPv6RoundTrip(t *testing.T) {
	ips := []string{"0.0.0.0", "10.0.0.5", "255.255.255.255"}
	for _, ip := range ips {
		mapped := filters.ToIPv4MappedIPv6(ip)
		if got := filters.FromIPv4MappedIPv6(mapped); got != ip {
			t.Errorf("FromIPv4MappedIPv6(ToIPv4MappedIPv6(%q)) = %q, want %q", ip, got, ip)
		}
	}
}

func TestClassifyIP(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"private_10", "10.0.0.5", "private"},
		{"private_192_168", "192.168.1.1", "private"},
		{"private_172_16", "172.16.0.1", "private"},
		{"public", "8.8.8.8", "public"},
		{"loopback", "127.0.0.1", "loopback"},
		{"loopback_v6", "::1", "loopback"},
		{"link_local", "169.254.1.1", "link-local"},
		{"multicast", "224.0.0.1", "multicast"},
		{"malformed", "garbage", ""},
		{"over_cap", strings.Repeat("1", filters.MaxInputBytes+1), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.ClassifyIP(tc.in); got != tc.want {
				t.Errorf("ClassifyIP(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
