// Package ansiblefacts maps this platform's native fact keys and values to
// the names and values ansible.builtin.setup gives on the same machine, and
// back.
//
// facts.gather stores facts under native, typed keys (os.distribution,
// hardware.memory_mb) holding native values (the os-release ID, a lowercase
// family, an integer major version). A converted playbook still reads
// Ansible's names (ansible_distribution == "Debian"), so the converter and a
// reader that wants Ansible's view go through this package: AnsibleKey and
// NativeKey are the key table, and ToAnsible applies the value rules too.
//
// The value rules are measured, not read from Ansible's source: the facts
// package's live tests gather the native facts in each distribution's own
// container and hold ToAnsible of them to what ansible.builtin.setup printed
// in the same image (internal/catalog/facts/testdata/distribution).
//
// internal/engine never imports this package (internal/archtest holds it to
// that): the engine runs native facts only, and Ansible's names are a
// conversion concern.
package ansiblefacts

import (
	"strconv"
	"strings"
)

// The native keys this package knows, as facts.gather declares them.
const (
	keyHostName         = "host.name"
	keyHostNodename     = "host.nodename"
	keyHostUptime       = "host.uptime_seconds"
	keyKernelName       = "os.kernel.name"
	keyKernelRelease    = "os.kernel.release"
	keyDistribution     = "os.distribution"
	keyFamily           = "os.family"
	keyVersion          = "os.version"
	keyVersionMajor     = "os.version_major"
	keyCodename         = "os.codename"
	keyEdition          = "os.edition"
	keyArchitecture     = "hardware.architecture"
	keyMemoryMB         = "hardware.memory_mb"
	keySwapMB           = "hardware.swap_mb"
	keyCPUSockets       = "hardware.cpu.sockets"
	keyCPUCores         = "hardware.cpu.cores"
	keyCPUThreads       = "hardware.cpu.threads"
	ansibleDistribution = "ansible_distribution"
)

// ansibleByNative is each native key's Ansible name. os.edition has none of
// its own: on Windows it is what Ansible reports as ansible_distribution.
var ansibleByNative = map[string]string{
	keyHostName:      "ansible_hostname",
	keyHostNodename:  "ansible_nodename",
	keyHostUptime:    "ansible_uptime_seconds",
	keyKernelName:    "ansible_system",
	keyKernelRelease: "ansible_kernel",
	keyDistribution:  ansibleDistribution,
	keyFamily:        "ansible_os_family",
	keyVersion:       "ansible_distribution_version",
	keyVersionMajor:  "ansible_distribution_major_version",
	keyCodename:      "ansible_distribution_release",
	keyArchitecture:  "ansible_architecture",
	keyMemoryMB:      "ansible_memtotal_mb",
	keySwapMB:        "ansible_swaptotal_mb",
	keyCPUSockets:    "ansible_processor_count",
	keyCPUCores:      "ansible_processor_cores",
	keyCPUThreads:    "ansible_processor_vcpus",
}

// nativeByAnsible is ansibleByNative read the other way.
var nativeByAnsible = func() map[string]string {
	out := make(map[string]string, len(ansibleByNative))
	for native, ansible := range ansibleByNative {
		out[ansible] = native
	}
	return out
}()

// AnsibleKey is the Ansible fact name of the native key native, and whether
// it has one. A native key with no Ansible counterpart (os.edition, an item
// family such as package.<name>) reports false.
func AnsibleKey(native string) (string, bool) {
	ansible, ok := ansibleByNative[native]
	return ansible, ok
}

// NativeKey is the native key of the Ansible fact name ansible, and whether
// this platform gathers it.
func NativeKey(ansible string) (string, bool) {
	native, ok := nativeByAnsible[ansible]
	return native, ok
}

// copied are the native keys whose value Ansible reports unchanged.
var copied = []string{keyHostName, keyHostNodename, keyHostUptime, keyKernelRelease, keyVersion, keyMemoryMB,
	keyCPUSockets, keyCPUCores, keyCPUThreads}

// ToAnsible is native's facts as ansible.builtin.setup names and words them.
// Only the keys AnsibleKey knows are mapped; any other key (an item family,
// os.edition on its own) is left out. A value Ansible gives that the native
// facts cannot decide is left out rather than guessed: the distribution of an
// os-release ID this package has not measured, a kernel name it does not
// know, a Windows architecture other than x86_64 or i686.
//
// Two native rules differ from Ansible's on Windows and the mapping keeps the
// native value or Ansible's constant: memory is truncated to whole megabytes
// natively and Ansible rounds it up, so ansible_memtotal_mb on Windows can be
// one less than Ansible's; and Windows has no swap natively, where Ansible
// reports 0, which is what this gives.
func ToAnsible(native map[string]any) map[string]any {
	out := map[string]any{}
	for _, key := range copied {
		if v, ok := native[key]; ok {
			out[ansibleByNative[key]] = v
		}
	}
	distribution, _ := native[keyDistribution].(string)
	windows := isWindows(native)

	// The distribution's name: the Windows caption, or Ansible's word for
	// the os-release ID.
	if windows {
		if edition, ok := native[keyEdition].(string); ok && edition != "" {
			out[ansibleDistribution] = edition
		}
	} else if name, ok := distributionNames[distribution]; ok {
		out[ansibleDistribution] = name
	}
	if family, ok := native[keyFamily].(string); ok {
		if name, ok := ansibleFamilyOfNative[family]; ok {
			out[ansibleByNative[keyFamily]] = name
		}
	}
	if kernel, ok := native[keyKernelName].(string); ok {
		if name, ok := kernelNames[kernel]; ok {
			out[ansibleByNative[keyKernelName]] = name
		}
	}
	if major, ok := ansibleMajor(native); ok {
		out[ansibleByNative[keyVersionMajor]] = major
	}
	if release, ok := ansibleRelease(distribution, native); ok {
		out[ansibleByNative[keyCodename]] = release
	}
	if arch, ok := native[keyArchitecture].(string); ok {
		if !windows {
			out[ansibleByNative[keyArchitecture]] = arch
		} else if word, ok := windowsArchitectures[arch]; ok {
			out[ansibleByNative[keyArchitecture]] = word
		}
	}
	if windows {
		// Ansible's Windows setup reports 0: a page file is not swap.
		out[ansibleByNative[keySwapMB]] = 0
	} else if v, ok := native[keySwapMB]; ok {
		out[ansibleByNative[keySwapMB]] = v
	}
	return out
}

// isWindows reports whether native describes a Windows host, by any of the
// three facts that say so (a filtered gather may hold only one).
func isWindows(native map[string]any) bool {
	for _, key := range []string{keyKernelName, keyDistribution, keyFamily} {
		if v, _ := native[key].(string); v == windowsName {
			return true
		}
	}
	return false
}

// ansibleMajor is Ansible's major version: os.version up to its first dot,
// a string, which on Debian testing is the whole forky/sid. With no
// os.version, the native integer major written out.
func ansibleMajor(native map[string]any) (string, bool) {
	if version, ok := native[keyVersion].(string); ok && version != "" {
		major, _, _ := strings.Cut(version, ".")
		return major, true
	}
	switch n := native[keyVersionMajor].(type) {
	case int:
		return strconv.Itoa(n), true
	case int64:
		return strconv.FormatInt(n, 10), true
	}
	return "", false
}

// ansibleRelease is Ansible's distribution release. On the SUSE family it is
// os.version's minor part ("0" on an enterprise release with none, absent on
// openSUSE with none); on CentOS Stream, which names no codename, it is
// Stream; elsewhere it is the codename, absent when there is none (Ansible's
// NA and its empty string).
func ansibleRelease(distribution string, native map[string]any) (string, bool) {
	if enterprise, ok := suseIDs[distribution]; ok {
		version, _ := native[keyVersion].(string)
		m := suseVersion.FindStringSubmatch(version)
		switch {
		case m == nil:
			return "", false
		case m[2] == "" && enterprise:
			return "0", true
		}
		return m[2], m[2] != ""
	}
	if codename, ok := native[keyCodename].(string); ok && codename != "" {
		return codename, true
	}
	if distribution == "centos" {
		return "Stream", true
	}
	return "", false
}
