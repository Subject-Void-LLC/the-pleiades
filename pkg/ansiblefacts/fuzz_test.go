// Package ansiblefacts_test holds the fuzz targets for the fact value rules
// and the native-to-Ansible mapping (Phase 41h).
package ansiblefacts_test

import (
	"slices"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/ansiblefacts"
)

// FuzzNativeValue holds the value rules to their shape for any text or
// integer a playbook compares a fact with (Phase 41h): a refusal never
// carries a value, NativeValue agrees with NativeValueReason, and an accepted
// value is a string, an integer or a list of those, never anything else.
func FuzzNativeValue(f *testing.F) {
	for _, s := range []string{"Debian", "12", "", "Stream", "Microsoft Windows Server 2022 Datacenter", "x\" || true || \"", "\xff\xfe", "x86_64", "release"} {
		f.Add(s, int64(12))
	}
	names := ansiblefacts.AnsibleNames()
	f.Fuzz(func(t *testing.T, s string, n int64) {
		for _, name := range names {
			for _, v := range []any{s, n, []any{s, n}, []any{}} {
				got, why := ansiblefacts.NativeValueReason(name, v)
				native, accepted := ansiblefacts.NativeValue(name, v)
				if accepted != (why == "") {
					t.Fatalf("NativeValue(%q) accepted=%v but NativeValueReason says %q", name, accepted, why)
				}
				if why != "" {
					if got != nil {
						t.Fatalf("NativeValueReason(%q) refused with a value %v", name, got)
					}
					continue
				}
				if !sameNative(native, got) {
					t.Fatalf("NativeValue(%q) and NativeValueReason(%q) disagree: %v and %v", name, name, native, got)
				}
				checkAccepted(t, name, got)
			}
		}
	})
}

// checkAccepted fails when an accepted native value is not a string, an
// integer or a list of those.
func checkAccepted(t *testing.T, name string, v any) {
	t.Helper()
	switch x := v.(type) {
	case string, int, int64:
		return
	case []any:
		for _, item := range x {
			switch item.(type) {
			case string, int, int64:
			default:
				t.Fatalf("NativeValue(%q) accepted a list holding %T", name, item)
			}
		}
	default:
		t.Fatalf("NativeValue(%q) accepted a %T", name, v)
	}
}

// sameNative compares two accepted values, which may be lists, element by
// element.
func sameNative(a, b any) bool {
	la, okA := a.([]any)
	lb, okB := b.([]any)
	if okA != okB {
		return false
	}
	if !okA {
		return a == b
	}
	return slices.EqualFunc(la, lb, func(x, y any) bool { return x == y })
}

// FuzzToAnsible holds the native-to-Ansible mapping to its own key table: for
// a fuzzed native key and string or integer values under the mapped keys,
// ToAnsible names only Ansible facts AnsibleNames lists and does not panic. It
// is close to true by construction (ToAnsible writes only keys from the table
// AnsibleNames reads), so it catches a table mismatch and a panic on odd
// values, not a wrong value.
func FuzzToAnsible(f *testing.F) {
	f.Add("os.distribution", "ubuntu", int64(12))
	f.Add("os.family", "debian", int64(0))
	f.Add("os.kernel.name", "", int64(-1))
	f.Add("hardware.memory_mb", "x\"", int64(1<<40))
	known := ansiblefacts.AnsibleNames()
	f.Fuzz(func(t *testing.T, key, value string, n int64) {
		native := map[string]any{
			key:                     value,
			"os.distribution":       value,
			"os.family":             value,
			"os.kernel.name":        value,
			"os.edition":            value,
			"os.version_major":      n,
			"hardware.memory_mb":    n,
			"hardware.swap_mb":      n,
			"os.codename":           value,
			"hardware.architecture": value,
			"os.version":            value,
		}
		for name := range ansiblefacts.ToAnsible(native) {
			if !slices.Contains(known, name) {
				t.Fatalf("ToAnsible named %q, which AnsibleNames does not list", name)
			}
		}
	})
}
