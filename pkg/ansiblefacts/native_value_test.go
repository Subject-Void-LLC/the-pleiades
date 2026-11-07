// Package ansiblefacts: tests of NativeValue and RendersTheSame.
package ansiblefacts

import (
	"reflect"
	"testing"
)

func TestNativeValueWritesAnsiblesValuesTheNativeWay(t *testing.T) {
	for _, tc := range []struct {
		fact string
		v    any
		want any
		ok   bool
	}{
		{"ansible_distribution", "Debian", "debian", true},
		{"ansible_distribution", "RedHat", "rhel", true},
		{"ansible_distribution", "OracleLinux", "ol", true},
		{"ansible_distribution", "MacOSX", "macos", true},
		{"ansible_distribution", "Microsoft Windows Server 2022 Datacenter", nil, false},
		{"ansible_distribution", 7, nil, false},
		{"ansible_os_family", "RedHat", "redhat", true},
		{"ansible_os_family", "Archlinux", "arch", true},
		{"ansible_os_family", "Darwin", "macos", true},
		{"ansible_os_family", "Windows", "windows", true},
		{"ansible_os_family", "Plan9", nil, false},
		{"ansible_system", "Linux", "linux", true},
		{"ansible_system", "Win32NT", "windows", true},
		{"ansible_system", "SunOS", nil, false},
		{"ansible_distribution_major_version", "12", int64(12), true},
		{"ansible_distribution_major_version", int64(9), int64(9), true},
		{"ansible_distribution_major_version", 9, int64(9), true},
		{"ansible_distribution_major_version", 9.0, int64(9), true},
		{"ansible_distribution_major_version", 9.5, int64(9), false},
		{"ansible_distribution_major_version", "forky/sid", int64(0), false},
		{"ansible_distribution_major_version", true, nil, false},
		{"ansible_distribution_release", "bookworm", "bookworm", true},
		{"ansible_distribution_release", "Stream", nil, false},
		{"ansible_distribution_release", "6", nil, false},
		{"ansible_distribution_release", "NA", nil, false},
		{"ansible_architecture", "x86_64", "x86_64", true},
		{"ansible_architecture", "64-bit", nil, false},
		{"ansible_kernel", "6.1.0", "6.1.0", true},
		{"ansible_memtotal_mb", int64(2048), int64(2048), true},
		{"ansible_swaptotal_mb", int64(0), int64(0), true},
		{"ansible_lsb", "x", nil, false},
		{"ansible_os_family", []any{"Debian", "RedHat"}, []any{"debian", "redhat"}, true},
		{"ansible_os_family", []any{"Debian", "Plan9"}, nil, false},
	} {
		got, ok := NativeValue(tc.fact, tc.v)
		if ok != tc.ok || (ok && !reflect.DeepEqual(got, tc.want)) {
			t.Errorf("NativeValue(%s, %#v) = %#v, %v; want %#v, %v", tc.fact, tc.v, got, ok, tc.want, tc.ok)
		}
	}
	if isDigits("") || !isDigits("15") || isDigits("1a") {
		t.Error("isDigits")
	}
}

func TestEveryAnsibleWordMapsBackToTheNativeValueToAnsibleGives(t *testing.T) {
	// The two directions agree: an Ansible word NativeValue translates is the
	// one ToAnsible gives for that native value.
	for id, name := range distributionNames {
		if got, ok := NativeValue("ansible_distribution", name); !ok || got != id {
			t.Errorf("%s: NativeValue = %v, %v; want %s", name, got, ok, id)
		}
	}
	for native, name := range kernelNames {
		if got, _ := NativeValue("ansible_system", name); got != native {
			t.Errorf("%s: NativeValue = %v; want %s", name, got, native)
		}
	}
	for native, name := range ansibleFamilyOfNative {
		if got, _ := NativeValue("ansible_os_family", name); got != native {
			t.Errorf("%s: NativeValue = %v; want %s", name, got, native)
		}
	}
}
