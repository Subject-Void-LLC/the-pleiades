// Package ansiblefacts: Ansible's values written the native way, for a
// condition the playbook converter translates.
package ansiblefacts

import (
	"slices"
	"strconv"
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
// word rather than a codename (CentOS Stream's Stream, a SUSE minor version,
// OpenBSD's release words), Windows' 64-bit or 32-bit architecture, a major
// version that is not an integer, or a fact this package does not map. The
// converter then leaves the condition for a person to write, and
// NativeValueReason says why.
func NativeValue(ansible string, v any) (any, bool) {
	native, why := NativeValueReason(ansible, v)
	return native, why == ""
}

// NoNativeValue is the reason NativeValueReason gives for a value with no
// one native meaning when it has nothing more particular to say.
const NoNativeValue = "has no one native value on every host"

// openBSDReleases are the values Ansible reports as
// ansible_distribution_release on OpenBSD: the suffix of kern.version
// (current, stable or beta), or release when it has none (ansible-core's
// Distribution.get_distribution_OpenBSD). They are not codenames, and this
// platform gathers no facts on OpenBSD, so none has a native value: a
// condition on one translated as a codename would compare os.codename with a
// word no gathered host reports.
var openBSDReleases = []string{"release", "current", "stable", "beta"}

// NativeValueReason is NativeValue with, in place of its bool, why the value
// has no one native meaning: empty when it has one, otherwise a phrase a
// refusal reads after the value's kind and the fact ("the text compared with
// ansible_distribution_release <why>"). The phrase never holds the value
// itself, only Ansible's own fixed words.
func NativeValueReason(ansible string, v any) (any, string) {
	if list, ok := v.([]any); ok {
		out := make([]any, 0, len(list))
		for _, item := range list {
			n, why := NativeValueReason(ansible, item)
			if why != "" {
				return nil, why
			}
			out = append(out, n)
		}
		return out, ""
	}
	native, ok := nativeByAnsible[ansible]
	if !ok {
		return nil, NoNativeValue
	}
	if slices.Contains(copied, native) {
		return v, ""
	}
	switch native {
	case keyVersionMajor:
		if n, ok := majorValue(v); ok {
			return n, ""
		}
		return nil, NoNativeValue
	case keySwapMB:
		// Ansible writes 0 on Windows where the native fact is absent, so no
		// one native value means the same on every host.
		return nil, NoNativeValue
	}
	s, isString := v.(string)
	if !isString {
		return nil, NoNativeValue
	}
	switch native {
	case keyDistribution:
		if id, ok := ansibleDistributionIDs[s]; ok {
			return id, ""
		}
	case keyFamily:
		if s == "Windows" {
			return windowsName, ""
		}
		if _, known := ansibleOSFamilyMap[s]; known {
			return nativeFamily(s), ""
		}
	case keyKernelName:
		if name, ok := nativeKernelNames[s]; ok {
			return name, ""
		}
	case keyCodename:
		return codenameValue(s)
	}
	// keyArchitecture ends here too: Windows reports 64-bit where the native
	// fact is x86_64, so x86_64 is true natively on a Windows host and false
	// in Ansible.
	return nil, NoNativeValue
}

// codenameValue is Ansible's distribution release s as the native codename,
// or why it is not one. A codename is a word; Ansible's own release values
// (CentOS Stream's Stream, a SUSE minor version, the kernel release on
// FreeBSD and macOS) start with a digit or are its placeholder, and
// OpenBSD's release words name no codename, so none has a native fact.
func codenameValue(s string) (any, string) {
	switch {
	case slices.Contains(openBSDReleases, s):
		return nil, "is one of the release words Ansible reports on OpenBSD (release, current, stable, beta), which name no codename, and this platform gathers no facts on OpenBSD"
	case s == "Stream" || s == "" || s == "NA" || (s[0] >= '0' && s[0] <= '9'):
		return nil, NoNativeValue
	}
	return s, ""
}

// majorValue is Ansible's major version (a string such as "12", or a number
// a playbook wrote) as the native integer, and false when it is not one.
func majorValue(v any) (any, bool) {
	switch n := v.(type) {
	case string:
		// Only the canonical spelling: Ansible compares "08" as text, which
		// is not "8".
		i, err := strconv.ParseInt(n, 10, 64)
		return i, err == nil && strconv.FormatInt(i, 10) == n
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
