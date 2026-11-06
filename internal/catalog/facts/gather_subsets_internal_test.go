// Tests of gather_subset's selection, the item key encoding and the package
// and service parsers (gather_subsets.go, gather_packages.go,
// gather_services.go), against output shaped as each tool prints it.
package facts

import (
	"reflect"
	"strings"
	"testing"
)

func TestGatherSelection_FollowsAnsiblesRules(t *testing.T) {
	cases := []struct {
		subsets any
		want    []string
		refuse  string
	}{
		{nil, []string{"hardware", "min"}, ""},
		{[]any{}, []string{"hardware", "min"}, ""},
		{[]any{"packages"}, []string{"min", "packages"}, ""},
		// all gathers files only when there are paths to read.
		{[]any{"all"}, []string{"hardware", "min", "packages", "ports", "services", "users"}, ""},
		// Removals alone start from the default, never listing packages.
		{[]any{"!hardware"}, []string{"min"}, ""},
		{[]any{"!packages"}, []string{"hardware", "min"}, ""},
		{[]any{"!all", "services"}, []string{"min", "services"}, ""},
		{[]any{"packages", "!min"}, []string{"packages"}, ""},
		{[]any{"!all", "!min"}, nil, "leaves no subset"},
		{[]any{"pakages"}, nil, "not a subset"},
		{"packages", nil, ""},
	}
	for _, tc := range cases {
		chosen, err := gatherSelection(map[string]any{gatherParamSubset: tc.subsets})
		checkSelection(t, tc.subsets, chosen, err, tc.want, tc.refuse)
	}
}

func TestGatherSelection_FilesFollowItsPaths(t *testing.T) {
	paths := []any{"/etc/hosts"}
	cases := []struct {
		subsets, files any
		want           []string
		refuse         string
	}{
		// Paths add files to whatever else is chosen.
		{nil, paths, []string{"files", "hardware", "min"}, ""},
		{[]any{"all"}, paths, []string{"files", "hardware", "min", "packages", "ports", "services", "users"}, ""},
		{[]any{"!all", "!min"}, paths, []string{"files"}, ""},
		// Naming files with nothing to read, or paths with !files, is a
		// mistake, and is refused rather than guessed at.
		{[]any{"files"}, nil, nil, "names none"},
		{[]any{"!files"}, paths, nil, "leaves files out"},
		{[]any{"files", "!files"}, paths, nil, "leaves files out"},
	}
	for _, tc := range cases {
		chosen, err := gatherSelection(map[string]any{gatherParamSubset: tc.subsets, gatherParamFiles: tc.files})
		checkSelection(t, tc.subsets, chosen, err, tc.want, tc.refuse)
	}
}

// checkSelection holds one selection to its expected subsets or refusal.
func checkSelection(t *testing.T, subsets any, chosen map[string]bool, err error, want []string, refuse string) {
	t.Helper()
	switch {
	case refuse != "":
		if err == nil || !strings.Contains(err.Error(), refuse) {
			t.Errorf("%v: %v, want a refusal naming %q", subsets, err, refuse)
		}
	case want == nil:
		// A bare string is refused or read by sdk.StringSlice's own rule;
		// either way nothing here panics.
	case err != nil:
		t.Errorf("%v: %v", subsets, err)
	case !reflect.DeepEqual(chosenNames(chosen), want):
		t.Errorf("%v: chose %v, want %v", subsets, chosenNames(chosen), want)
	}
}

func TestItemKey_EncodesWhatAKeyCannotHoldExactly(t *testing.T) {
	for name, want := range map[string]string{
		"nginx":                                 "package.nginx",
		"libc6:i386":                            "package.libc6:i386",
		"Microsoft Visual C++ 2015 (x64)":       "package.Microsoft%20Visual%20C++%202015%20(x64)",
		"a/b%c":                                 "package.a%2Fb%25c",
		"zero" + string(rune(0x200b)) + "width": "package.zero%E2%80%8Bwidth",
	} {
		if got := itemKey(gatherFamilyPackage, name); got != want {
			t.Errorf("itemKey(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestPackageParsers_ReadEachDatabasesOwnOutput(t *testing.T) {
	dpkg := "ii \tbash\t5.2.21-2ubuntu4\tamd64\nrc \tgone\t1.0\tamd64\nii \tlibc6:i386\t2.39-0ubuntu8\ti386\nnot a line\n" +
		"hi \tlinux-image-generic\t6.8.0.48\tamd64\niU \thalfway\t1\tamd64\niH \tbroken\t1\tamd64\npn \tpurged\t1\tamd64\n"
	rpm := "bash\t(none)\t5.2.26-3.el10\tx86_64\nopenssl-libs\t1\t3.2.2-16.el10\tx86_64\nopenssl-libs\t1\t3.2.2-16.el10\ti686\ngpg-pubkey\t(none)\tabc-def\t(none)\n"
	apk := "busybox-1.36.1-r29 x86_64 {busybox} (GPL-2.0-only) [installed]\npy3-foo-bar-2.0.1-r0 noarch {py3-foo-bar} (MIT) [installed]\nWARNING: something\n"
	got := map[string]map[string]any{}
	for _, f := range append(append(distinctAll(packageFacts(parseDpkg(dpkg))), distinctAll(packageFacts(parseRPM(rpm)))...), distinctAll(packageFacts(parseAPK(apk)))...) {
		got[f.key] = f.value.(map[string]any)
	}
	want := map[string]map[string]any{
		"package.bash":                {"version": "5.2.21-2ubuntu4", "arch": "amd64", "source": "dpkg"},
		"package.libc6:i386":          {"version": "2.39-0ubuntu8", "arch": "i386", "source": "dpkg"},
		"package.linux-image-generic": {"version": "6.8.0.48", "arch": "amd64", "source": "dpkg"},
		"package.halfway":             {"version": "1", "arch": "amd64", "source": "dpkg"},
		"package.openssl-libs":        {"version": "1:3.2.2-16.el10", "arch": "x86_64", "source": "rpm"},
		"package.openssl-libs:i686":   {"version": "1:3.2.2-16.el10", "arch": "i686", "source": "rpm"},
		"package.busybox":             {"version": "1.36.1-r29", "arch": "x86_64", "source": "apk"},
		"package.py3-foo-bar":         {"version": "2.0.1-r0", "arch": "noarch", "source": "apk"},
	}
	// rpm's bash and dpkg's bash came from separate calls, so each call
	// named its own; the rpm one is checked separately.
	delete(got, "package.bash")
	for key, value := range want {
		if key == "package.bash" {
			continue
		}
		if !reflect.DeepEqual(got[key], value) {
			t.Errorf("%s = %v, want %v", key, got[key], value)
		}
	}
	for _, gone := range []string{"package.gone", "package.broken", "package.purged"} {
		if _, ok := got[gone]; ok {
			t.Errorf("%s, which dpkg does not hold installed, was reported", gone)
		}
	}
	if _, ok := got["package.gpg-pubkey"]; ok {
		t.Error("rpm's gpg-pubkey entries were reported as packages")
	}
	both := distinctAll(append(packageFacts(parseDpkg("ii \tbash\t5.2\tamd64\n")), packageFacts(parseRPM("bash\t(none)\t5.2-1\tx86_64\n"))...))
	if len(both) != 2 || both[0].key != "package.bash" || both[1].key != "package.bash:x86_64" {
		t.Errorf("two databases naming one package: %v", both)
	}
}

func TestParseSystemd_MergesUnitFilesAndLoadedUnits(t *testing.T) {
	out := "ssh.service enabled enabled\ncron.service disabled enabled\ngetty@.service enabled enabled\nsystemd-fsck-root.service static -\n" +
		serviceSectionMark + "\n" +
		"ssh.service loaded active running OpenBSD Secure Shell server\n● failing.service loaded failed failed A unit that failed\ngetty@tty1.service loaded active running Getty on tty1\n"
	got := map[string]map[string]any{}
	for _, f := range parseSystemd(out) {
		got[f.key] = f.value.(map[string]any)
	}
	want := map[string]map[string]any{
		"service.ssh.service":               {"state": "running", "enabled": true, "status": "enabled", "source": "systemd"},
		"service.cron.service":              {"state": "inactive", "enabled": false, "status": "disabled", "source": "systemd"},
		"service.systemd-fsck-root.service": {"state": "inactive", "enabled": false, "status": "static", "source": "systemd"},
		"service.failing.service":           {"state": "failed", "enabled": false, "status": "unknown", "source": "systemd"},
		"service.getty@tty1.service":        {"state": "running", "enabled": false, "status": "unknown", "source": "systemd"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("services = %v\nwant %v", got, want)
	}
	if parseSystemd("no mark here") != nil {
		t.Error("output with no section mark was read")
	}
}

// TestParseSystemd_LeavesOutUnitsThatLastOnlyAsLongAsALogin feeds the three
// listings as a systemd 252 host printed them during a gather, under an
// Accept=yes sshd.socket and dbus-broker: the connection's own sshd
// instance, the login's user units, a lingering user's, a bus name's
// transient unit and a crash's coredump instance are left out; getty@tty1,
// boot-time instances and a template's numbered instances with no socket of
// their name stay.
func TestParseSystemd_LeavesOutUnitsThatLastOnlyAsLongAsALogin(t *testing.T) {
	out := "sshd.service disabled disabled\nsshd@.service static -\ngetty@.service enabled enabled\nworker@.service static -\n" +
		"dbus-:1.1-org.pleiades.Probe@0.service transient -\nuser@.service static -\n" +
		serviceSectionMark + "\n" +
		"sshd@6-172.17.0.4:22-172.17.0.1:58134.service loaded active running OpenSSH per-connection server daemon (172.17.0.1:58134)\n" +
		"user@0.service loaded active running User Manager for UID 0\n" +
		"user-runtime-dir@0.service loaded active exited User Runtime Directory /run/user/0\n" +
		"user@1000.service loaded active running User Manager for UID 1000\n" +
		"dbus-:1.1-org.pleiades.Probe@0.service loaded active running dbus-:1.1-org.pleiades.Probe@0.service\n" +
		"systemd-coredump@0-1234-0.service loaded active running Process Core Dump (PID 1234/UID 0)\n" +
		"getty@tty1.service loaded active running Getty on tty1\n" +
		"modprobe@drm.service loaded inactive dead Load Kernel Module drm\n" +
		"worker@1-2.service loaded active running A numbered worker\n" +
		serviceSectionMark + "\n" +
		"sshd.socket loaded active listening OpenSSH Server Socket\n" +
		"systemd-coredump.socket loaded active listening Process Core Dump Socket\n" +
		"systemd-journald.socket loaded active running Journal Socket\n"
	got := map[string]bool{}
	for _, f := range parseSystemd(out) {
		got[f.key] = true
	}
	for _, kept := range []string{"service.sshd.service", "service.getty@tty1.service", "service.modprobe@drm.service", "service.worker@1-2.service"} {
		if !got[kept] {
			t.Errorf("%s, which lasts as long as the host, was left out", kept)
		}
	}
	for key := range got {
		for _, gone := range []string{"sshd@6-", "user@", "user-runtime-dir@", "dbus-:", "systemd-coredump@"} {
			if strings.Contains(key, gone) {
				t.Errorf("%s, which lasts only as long as a login, a connection or a process, was reported", key)
			}
		}
	}
	if len(got) != 4 {
		t.Errorf("services %v, want the four that stay", got)
	}
}

// distinctAll is facts as Gather emits them from one run: a key an earlier
// fact took told apart (gatherNames).
func distinctAll(facts []gatherFact) []gatherFact {
	names := gatherNames{}
	for i := range facts {
		facts[i] = names.distinct(facts[i])
	}
	return facts
}
