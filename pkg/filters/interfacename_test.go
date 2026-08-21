package filters_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

func TestInterfaceShortForm(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"gigabit", "GigabitEthernet0/1", "Gi0/1"},
		{"fastethernet", "FastEthernet0/0", "Fa0/0"},
		{"tengig", "TenGigabitEthernet1/1/1", "Te1/1/1"},
		{"port_channel", "Port-channel1", "Po1"},
		{"vlan", "Vlan100", "Vl100"},
		{"loopback", "Loopback0", "Lo0"},
		{"already_short", "Gi0/1", "Gi0/1"},
		{"case_insensitive", "gigabitethernet0/1", "Gi0/1"},
		{"unrecognized", "Bogus0/1", ""},
		{"embedded_nul_byte", "Bogus\x000/1", ""},
		{"over_cap", strings.Repeat("1", filters.MaxInputBytes+1), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.InterfaceShortForm(tc.in); got != tc.want {
				t.Errorf("InterfaceShortForm(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestInterfaceLongForm(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"gigabit", "Gi0/1", "GigabitEthernet0/1"},
		{"fastethernet", "Fa0/0", "FastEthernet0/0"},
		{"tengig", "Te1/1/1", "TenGigabitEthernet1/1/1"},
		{"port_channel", "Po1", "Port-channel1"},
		{"vlan", "Vl100", "Vlan100"},
		{"already_long", "GigabitEthernet0/1", "GigabitEthernet0/1"},
		{"case_insensitive", "gi0/1", "GigabitEthernet0/1"},
		{"unrecognized", "Bogus0/1", ""},
		{"embedded_nul_byte", "Bogus\x000/1", ""},
		{"over_cap", strings.Repeat("1", filters.MaxInputBytes+1), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.InterfaceLongForm(tc.in); got != tc.want {
				t.Errorf("InterfaceLongForm(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestInterfaceNameRoundTrip(t *testing.T) {
	longNames := []string{"GigabitEthernet0/1", "FastEthernet0/0", "TenGigabitEthernet1/1/1", "Port-channel1", "Vlan100", "Loopback0"}
	for _, name := range longNames {
		short := filters.InterfaceShortForm(name)
		if got := filters.InterfaceLongForm(short); got != name {
			t.Errorf("InterfaceLongForm(InterfaceShortForm(%q)) = %q, want %q", name, got, name)
		}
	}
}
