// Package ansiblefacts: Ansible's values written the native way, for a
// condition the playbook converter translates.
package ansiblefacts

import (
	"slices"
	"strconv"
	"strings"
)

// ansibleDistributionIDs is distributionNames read the other way: Ansible's
// distribution word to the native os-release ID.
var ansibleDistributionIDs = func() map[string]string {
	out := make(map[string]string, len(distributionNames))
	for id, name := range distributionNames {
		out[name] = id
	}
	return out
}()

// nativeKernelNames is kernelNames read the other way.
var nativeKernelNames = func() map[string]string {
	out := make(map[string]string, len(kernelNames))
	for native, name := range kernelNames {
		out[name] = native
	}
	return out
}()

// NativeValue is what Ansible's value v of the fact ansible is, written the
// way the native fact holds it, so a converted condition compares the native
// fact with it: "Debian" for ansible_distribution is "debian", "RedHat" for
// ansible_os_family is "redhat", "12" for ansible_distribution_major_version
// is the integer 12. A list (a condition's in [...]) is translated element by
// element.
//
// It reports false when no one native value means the same on every host:
// a distribution this package has not measured or a Windows caption (which is
// os.edition natively, not os.distribution), a release that is Ansible's own
// word rather than a codename (CentOS Stream's Stream, a SUSE minor version),
// Windows' 64-bit or 32-bit architecture, a major version that is not an
// integer, or a fact this package does not map. The converter then leaves
// the condition for a person to write.
func NativeValue(ansible string, v any) (any, bool) {
	if list, ok := v.([]any); ok {
		out := make([]any, 0, len(list))
		for _, item := range list {
			n, ok := NativeValue(ansible, item)
			if !ok {
				return nil, false
			}
			out = append(out, n)
		}
		return out, true
	}
	native, ok := nativeByAnsible[ansible]
	if !ok {
		return nil, false
	}
	if slices.Contains(copied, native) {
		return v, true
	}
	switch native {
	case keyVersionMajor:
		return majorValue(v)
	case keySwapMB:
		// Linux and the BSDs report swap as Ansible does; on Windows Ansible
		// writes 0 where the native fact is absent, which a person decides.
		return v, true
	}
	s, isString := v.(string)
	if !isString {
		return nil, false
	}
	switch native {
	case keyDistribution:
		id, ok := ansibleDistributionIDs[s]
		return id, ok
	case keyFamily:
		if s == "Windows" {
			return windowsName, true
		}
		if _, known := ansibleOSFamilyMap[s]; known {
			return nativeFamily(s), true
		}
	case keyKernelName:
		name, ok := nativeKernelNames[s]
		return name, ok
	case keyCodename:
		if s == "Stream" || s == "" || s == "NA" || isDigits(s) {
			return nil, false
		}
		return s, true
	case keyArchitecture:
		if s == "64-bit" || s == "32-bit" {
			return nil, false
		}
		return s, true
	}
	return nil, false
}

// majorValue is Ansible's major version (a string such as "12", or a number
// a playbook wrote) as the native integer, and false when it is not one.
func majorValue(v any) (any, bool) {
	switch n := v.(type) {
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		return i, err == nil
	case int64:
		return n, true
	case int:
		return int64(n), true
	case float64:
		return int64(n), n == float64(int64(n))
	}
	return nil, false
}

// isDigits reports whether s is a non-empty run of ASCII digits.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
