// Tests of the FreeBSD and macOS probes' parsers (gather_bsd.go,
// gather_bsd_items.go) against output captured from the lab's FreeBSD 15.1
// machine and the lab Mac (macOS 27.0.1) on 2026-11-03.
package facts

import (
	"reflect"
	"strings"
	"testing"
)

// valuesOf is facts as a key to value map.
func valuesOf(facts []gatherFact) map[string]any {
	out := map[string]any{}
	for _, f := range facts {
		out[f.key] = f.value
	}
	return out
}

func TestBSDHostParsers_ReadSysctlAndSwVers(t *testing.T) {
	cases := []struct {
		parse func(string) []gatherFact
		out   string
		want  map[string]any
	}{
		{bytesToMB(gatherFactMemTotalMB), "4254609408\n", map[string]any{gatherFactMemTotalMB: 4057}},
		{bytesToMB(gatherFactMemTotalMB), "17179869184\n", map[string]any{gatherFactMemTotalMB: 16384}},
		{parseSwap, "3006267392\n", map[string]any{gatherFactSwapTotalMB: 2867}},
		{parseSwap, "total = 1024.00M  used = 584.69M  free = 439.31M  (encrypted)\n", map[string]any{gatherFactSwapTotalMB: 1024}},
		{wholeNumber(gatherFactProcessorVCPUs), "4\n", map[string]any{gatherFactProcessorVCPUs: 4}},
		{parseBoottime, "{ sec = 1793718135, usec = 706058 } Tue Nov  3 15:02:15 2026\n1793718250\n", map[string]any{gatherFactUptimeSeconds: 115}},
		{parseSwVers, "ProductName:\t\tmacOS\nProductVersion:\t\t27.0.1\nBuildVersion:\t\t26A434\n",
			map[string]any{gatherFactDistribution: "MacOSX", gatherFactDistributionVersion: "27.0.1", gatherFactDistributionMajor: "27"}},
	}
	for _, tc := range cases {
		if got := valuesOf(tc.parse(tc.out)); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: %v, want %v", tc.out, got, tc.want)
		}
	}
	for _, bad := range []string{"", "x", "-5", "{ sec = x }\n1\n", "{ sec = 10 }\n5\n", "total = lots"} {
		for _, parse := range []func(string) []gatherFact{bytesToMB("k"), wholeNumber("k"), parseSwap, parseBoottime} {
			if got := parse(bad); got != nil {
				t.Errorf("%q read as %v", bad, got)
			}
		}
	}
}

func TestFreeBSDParsers_ReadPkgAndRC(t *testing.T) {
	pkgs := valuesOf(packageFacts(parsePkg("brotli\t1.2.1,1\tFreeBSD:15:amd64\ncurl\t8.23.0\tFreeBSD:15:amd64\nbad line\n")))
	if want := map[string]any{"version": "1.2.1,1", "arch": "amd64", "source": "pkg"}; !reflect.DeepEqual(pkgs["package.brotli"], want) || len(pkgs) != 2 {
		t.Errorf("pkg: %v", pkgs)
	}
	out := "DAEMON\nhostid\nsshd\ncron\nntpd\nsyslogd\n" + serviceSectionMark + "\n/etc/rc.d/hostid\n/etc/rc.d/sshd\n/etc/rc.d/cron\n/etc/rc.d/syslogd\n" +
		serviceSectionMark + "\nhostid\t/etc/rc.d/hostid: unknown directive 'status'.\nsshd\tsshd is running as pid 1538.\n" +
		"cron\teval: cannot open /var/run/cron.pid: Permission denied\nsyslogd\tsyslogd is not running.\n"
	got := valuesOf(parseRC(out))
	want := map[string]any{
		"service.hostid":  map[string]any{"state": "unknown", "enabled": true, "status": "enabled", "source": "rc"},
		"service.sshd":    map[string]any{"state": "running", "enabled": true, "status": "enabled", "source": "rc"},
		"service.cron":    map[string]any{"state": "unknown", "enabled": true, "status": "enabled", "source": "rc"},
		"service.ntpd":    map[string]any{"state": "unknown", "enabled": false, "status": "disabled", "source": "rc"},
		"service.syslogd": map[string]any{"state": "stopped", "enabled": true, "status": "enabled", "source": "rc"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rc = %v\nwant %v", got, want)
	}
	if parseRC("one section only") != nil {
		t.Error("output missing its sections was read")
	}
}

func TestDarwinParsers_ReadBrewPkgutilAndLaunchd(t *testing.T) {
	brew := valuesOf(packageFacts(parseBrew("ada-url 3.4.4\nbrotli 1.1.0 1.2.0\n" + serviceSectionMark + "\nclaude-code 2.1.236\nbrotli 9\n")))
	if brew["package.brotli"].(map[string]any)["version"] != "1.2.0" || brew["package.claude-code"].(map[string]any)["source"] != "brew-cask" {
		t.Errorf("brew: %v", brew)
	}
	if _, ok := brew["package.brotli@brew-cask"]; !ok {
		t.Errorf("a cask sharing a formula's name was not told apart: %v", brew)
	}
	receipts := valuesOf(packageFacts(parsePkgutil("package-id: com.apple.pkg.A\nversion: 3.1.0.0.1\nvolume: /\n\npackage-id: com.apple.pkg.B\nversion: 1\n")))
	if len(receipts) != 2 || receipts["package.com.apple.pkg.A"].(map[string]any)["version"] != "3.1.0.0.1" {
		t.Errorf("pkgutil: %v", receipts)
	}
	out := "system = {\n\ttype = system\n\tservices = {\n\t\t    1016      - \tcom.apple.IOUserBluetoothSerialDriver-0x10000110d\n" +
		"\t\t       0   (pe) \tcom.apple.kernelmanager_helper\n\t\t     420      - \tcom.apple.runningboardd\n\t}\n\tother = {\n\t\t5 - x.y\n\t}\n}\n" +
		serviceSectionMark + "\n\tdisabled services = {\n\t\t\"com.apple.runningboardd\" => disabled\n\t\t\"com.apple.screensharing\" => enabled\n\t}\n"
	got := valuesOf(parseLaunchd(out))
	want := map[string]any{
		"service.com.apple.IOUserBluetoothSerialDriver-0x10000110d": map[string]any{"state": "running", "enabled": true, "status": "-", "source": "launchd"},
		"service.com.apple.kernelmanager_helper":                    map[string]any{"state": "stopped", "enabled": true, "status": "(pe)", "source": "launchd"},
		"service.com.apple.runningboardd":                           map[string]any{"state": "running", "enabled": false, "status": "-", "source": "launchd"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("launchd = %v\nwant %v", got, want)
	}
	if parseLaunchd("no mark") != nil {
		t.Error("output with no mark was read")
	}
}

func TestGatherProbesFor_EachPlatformReadsItsOwnSources(t *testing.T) {
	commands := func(platform string) string {
		var all []string
		for _, p := range gatherProbesFor(platform) {
			all = append(all, p.command)
		}
		return strings.Join(all, "\n")
	}
	for platform, want := range map[string][]string{
		platformLinux:   {"/proc/meminfo", "dpkg-query", "systemctl"},
		platformFreeBSD: {"hw.physmem", "pkg query", "service -l", "/etc/os-release"},
		platformDarwin:  {"hw.memsize", "sw_vers", "brew", "pkgutil", "launchctl print system"},
		"SunOS":         {"/proc/meminfo"},
	} {
		got := commands(platform)
		for _, w := range want {
			if !strings.Contains(got, w) {
				t.Errorf("%s reads no %q", platform, w)
			}
		}
	}
	if strings.Contains(commands(platformDarwin), "/proc") || strings.Contains(commands(platformFreeBSD), "/proc") {
		t.Error("a BSD platform reads /proc")
	}
}
