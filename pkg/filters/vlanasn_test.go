package filters_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

func TestValidateVLAN(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want bool
	}{
		{"min_valid", 1, true},
		{"max_valid", 4094, true},
		{"mid", 100, true},
		{"zero_rejected", 0, false},
		{"max_reserved_rejected", 4095, false},
		{"negative_rejected", -1, false},
		{"way_over_rejected", 99999, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.ValidateVLAN(tc.in); got != tc.want {
				t.Errorf("ValidateVLAN(%d) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsCiscoReservedVLAN(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want bool
	}{
		{"default_vlan", 1, true},
		{"fddi_start", 1002, true},
		{"fddi_end", 1005, true},
		{"fddi_mid", 1003, true},
		{"ordinary", 100, false},
		{"just_below_range", 1001, false},
		{"just_above_range", 1006, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.IsCiscoReservedVLAN(tc.in); got != tc.want {
				t.Errorf("IsCiscoReservedVLAN(%d) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestValidateASN(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want bool
	}{
		{"min_valid", 1, true},
		{"16bit_typical", 64512, true},
		{"32bit_typical", 4200000000, true},
		{"max_valid", 4294967294, true},
		{"zero_reserved", 0, false},
		{"16bit_reserved", 65535, false},
		{"32bit_reserved", 4294967295, false},
		{"negative", -1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.ValidateASN(tc.in); got != tc.want {
				t.Errorf("ValidateASN(%d) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsPrivateASN(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want bool
	}{
		{"16bit_private_start", 64512, true},
		{"16bit_private_end", 65534, true},
		{"32bit_private_start", 4200000000, true},
		{"32bit_private_end", 4294967294, true},
		{"public_16bit", 30000, false},
		{"just_below_16bit_private", 64511, false},
		{"just_above_16bit_private", 65535, false},
		{"public_32bit", 100000, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.IsPrivateASN(tc.in); got != tc.want {
				t.Errorf("IsPrivateASN(%d) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
