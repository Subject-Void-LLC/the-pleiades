package filters_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

func TestMACToCiscoFormat(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"from_colon", "00:00:5e:00:53:01", "0000.5e00.5301"},
		{"from_dash", "00-00-5e-00-53-01", "0000.5e00.5301"},
		{"from_dotted", "0000.5e00.5301", "0000.5e00.5301"},
		{"uppercase_input", "00:00:5E:00:53:01", "0000.5e00.5301"},
		{"malformed", "garbage", ""},
		{"eui64_rejected", "02:00:5e:10:00:00:00:01", ""},
		{"over_cap", strings.Repeat("1", filters.MaxInputBytes+1), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.MACToCiscoFormat(tc.in); got != tc.want {
				t.Errorf("MACToCiscoFormat(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestMACToColonFormat(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"from_dotted", "0000.5e00.5301", "00:00:5e:00:53:01"},
		{"from_dash", "00-00-5e-00-53-01", "00:00:5e:00:53:01"},
		{"from_colon", "00:00:5e:00:53:01", "00:00:5e:00:53:01"},
		{"malformed", "garbage", ""},
		{"over_cap", strings.Repeat("1", filters.MaxInputBytes+1), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.MACToColonFormat(tc.in); got != tc.want {
				t.Errorf("MACToColonFormat(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestMACToWindowsFormat(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"from_colon", "00:00:5e:00:53:01", "00-00-5E-00-53-01"},
		{"from_dotted", "0000.5e00.5301", "00-00-5E-00-53-01"},
		{"malformed", "garbage", ""},
		{"over_cap", strings.Repeat("1", filters.MaxInputBytes+1), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.MACToWindowsFormat(tc.in); got != tc.want {
				t.Errorf("MACToWindowsFormat(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestMACOUI(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"from_colon", "00:00:5e:00:53:01", "00:00:5E"},
		{"from_dotted", "0000.5e00.5301", "00:00:5E"},
		{"malformed", "garbage", ""},
		{"short_mac_rejected", "00:00", ""},
		{"over_cap", strings.Repeat("1", filters.MaxInputBytes+1), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.MACOUI(tc.in); got != tc.want {
				t.Errorf("MACOUI(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
