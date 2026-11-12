// Tests of the operand rule, and a fuzz target holding it to its contract:
// it never panics, it refuses exactly the words a program would read as an
// option or a pattern, and its message never carries the value.
package operand_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/operand"
)

// TestProblem covers the names every method takes, the two kinds of word
// refused, and the edges of each: a dash inside or at the end of a name is
// a name, and so are the characters real package names carry.
func TestProblem(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		want       string // a substring of the problem; "" means none
	}{
		{"a package", "curl", ""},
		{"a package with a version character set", "libstdc++6", ""},
		{"g++, which ends in a plus", "g++", ""},
		{"an architecture qualifier", "libc6:amd64", ""},
		{"a unit with its suffix", "nginx.service", ""},
		{"a template instance", "getty@tty1.service", ""},
		{"an escaped unit name", `dev-disk-by\x2duuid-1.device`, ""},
		{"a Windows service", "Spooler", ""},
		{"a dash inside", "openssh-server", ""},
		{"a dash at the end is the method's own concern", "diffutils-", ""},
		{"empty is the required check's concern", "", ""},
		{"a short option", "-f", `begins with "-"`},
		{"an apt configuration option", "-oDPkg::Pre-Invoke::=touch /tmp/pwned", `begins with "-"`},
		{"a long option", "--autoremove", `begins with "-"`},
		{"a lone dash", "-", `begins with "-"`},
		{"the end of options", "--", `begins with "-"`},
		{"a star", "lib*", "pattern"},
		{"a question mark", "ssh?", "pattern"},
		{"a bracket", "ss[hd]", "pattern"},
		{"a closing bracket alone", "a]", "pattern"},
		{"a star anywhere", "nginx*.service", "pattern"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := operand.Problem(tc.text)
			if tc.want == "" {
				if got != "" {
					t.Fatalf("Problem(%q) = %q, want none", tc.text, got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("Problem(%q) = %q, want it to say %q", tc.text, got, tc.want)
			}
		})
	}
}

// TestCheck names the parameter in the error and returns nil for a name.
func TestCheck(t *testing.T) {
	if err := operand.Check("name", "curl"); err != nil {
		t.Fatalf("Check(curl) = %v", err)
	}
	err := operand.Check("name", "--autoremove")
	if err == nil || !strings.HasPrefix(err.Error(), "name is refused: ") {
		t.Fatalf("Check(--autoremove) = %v, want it to name the parameter", err)
	}
	if strings.Contains(err.Error(), "autoremove") {
		t.Errorf("the error carries the value: %v", err)
	}
}

// FuzzProblem holds Problem to its contract over any text: no panic, a
// refusal exactly when the text begins with a dash or holds a pattern
// character, and a message that is one of the two fixed sentences, so it
// can never carry any part of the value.
func FuzzProblem(f *testing.F) {
	for _, seed := range []string{
		"curl", "g++", "nginx.service", "", "-", "--", "-f", "--autoremove",
		"-oDPkg::Pre-Invoke::=touch /tmp/pwned", "--reference=/etc/shadow",
		"lib*", "s*", "ss[hd]", "a?b", "diffutils-", "\x00", "-\n", " -x",
	} {
		f.Add(seed)
	}
	option, pattern := operand.Problem("-"), operand.Problem("*")
	f.Fuzz(func(t *testing.T, text string) {
		got := operand.Problem(text)
		want := strings.HasPrefix(text, "-") || strings.ContainsAny(text, "*?[]")
		if (got != "") != want {
			t.Fatalf("Problem(%q) = %q, want a refusal %v", text, got, want)
		}
		if got != "" && got != option && got != pattern {
			t.Fatalf("Problem(%q) = %q, which is neither fixed sentence", text, got)
		}
	})
}

// TestKinds covers each program's own grammar: what it admits that the
// general rule does not (systemd's root units, a mount path holding a
// bracket) and what it refuses that the general rule admits (APT's trailing
// "-", a release or a version in the name, an RPM group, file or
// capability, a version's trailing "-", an fstab field holding a space).
func TestKinds(t *testing.T) {
	for _, tc := range []struct {
		kind       operand.Kind
		text, want string // want: a substring of the problem; "" means none
	}{
		{operand.SystemdUnit, "nginx.service", ""},
		{operand.SystemdUnit, "-.mount", ""},
		{operand.SystemdUnit, "-.slice", ""},
		{operand.SystemdUnit, "-.swap", `begins with "-"`},
		{operand.SystemdUnit, "-.mount.d", `begins with "-"`},
		{operand.SystemdUnit, "-f", `begins with "-"`},
		{operand.SystemdUnit, "--now", `begins with "-"`},
		{operand.SystemdUnit, "s*", "pattern"},
		{operand.SystemdUnit, `dev-disk-by\x2duuid-1.device`, ""},

		{operand.AptPackage, "curl", ""},
		{operand.AptPackage, "g++", ""},
		{operand.AptPackage, "libstdc++6", ""},
		{operand.AptPackage, "libc6:amd64", ""},
		{operand.AptPackage, "python3.11", ""},
		{operand.AptPackage, "diffutils-", `ends in "-"`},
		{operand.AptPackage, "jq-:amd64", `ends in "-"`},
		{operand.AptPackage, "jq/bookworm", `holds "/"`},
		{operand.AptPackage, "jq=1.6", `holds "="`},
		{operand.AptPackage, "~nzsh", "does not begin with a letter or a digit"},
		{operand.AptPackage, "^zsh", "does not begin with a letter or a digit"},
		{operand.AptPackage, "zsh$", "a character no Debian package name holds"},
		{operand.AptPackage, "jq:", `":"`},
		{operand.AptPackage, "jq:amd64:x", `":"`},
		{operand.AptPackage, "--autoremove", `begins with "-"`},
		{operand.AptPackage, "lib*", "pattern"},

		{operand.RPMPackage, "NetworkManager", ""},
		{operand.RPMPackage, "libstdc++", ""},
		{operand.RPMPackage, "python3.11", ""},
		{operand.RPMPackage, "bash.x86_64", ""},
		{operand.RPMPackage, "@core", `begins with "@"`},
		{operand.RPMPackage, "/usr/bin/jq", `holds "/"`},
		{operand.RPMPackage, "perl(strict)", `"(" or ")"`},
		{operand.RPMPackage, "nginx >= 1.20", "comparison"},
		{operand.RPMPackage, "nginx-1:1.20", `holds ":"`},
		{operand.RPMPackage, "nginx-", `ends in "-"`},
		{operand.RPMPackage, "ng~inx", "a character no RPM package name holds"},
		{operand.RPMPackage, "--setopt=tsflags=noscripts", `begins with "-"`},

		{operand.Package, "curl", ""},
		{operand.Package, "libc6:amd64", ""},
		{operand.Package, "_unusual", ""},
		{operand.Package, "diffutils-", `ends in "-"`},
		{operand.Package, "@core", "for APT"},
		{operand.Package, "jq/bookworm", "for RPM"},

		{operand.PackageVersion, "", ""},
		{operand.PackageVersion, "1.24.0-2ubuntu7", ""},
		{operand.PackageVersion, "1:3.8-4", ""},
		{operand.PackageVersion, "1.6-2.1+deb12u3", ""},
		{operand.PackageVersion, "2.0~rc1", ""},
		{operand.PackageVersion, "1.0^20230101git1234", ""},
		{operand.PackageVersion, "1.24.0-1.el9_4", ""},
		{operand.PackageVersion, "*", "pattern"},
		{operand.PackageVersion, "1.[0-9]", "pattern"},
		{operand.PackageVersion, "1:3.8-4-", `ends in "-"`},
		{operand.PackageVersion, "-1", `begins with "-"`},
		{operand.PackageVersion, "1.0 2.0", "a character no Debian or RPM version holds"},
		{operand.PackageVersion, "1.0/x", "a character no Debian or RPM version holds"},

		{operand.MountField, "/data", ""},
		{operand.MountField, "UUID=0b1c-2d3e", ""},
		{operand.MountField, "LABEL=data", ""},
		{operand.MountField, "nas:/export", ""},
		{operand.MountField, "//server/share", ""},
		{operand.MountField, "/dev/sda1[/srv]", ""},
		{operand.MountField, "rw,noatime,x-systemd.automount", ""},
		{operand.MountField, "-a", `begins with "-"`},
		{operand.MountField, "#x", `begins with "#"`},
		{operand.MountField, "/mnt/a b", "space"},
		{operand.MountField, "/dev/sdb1\n/dev/sdc1", "line break"},
		{operand.MountField, "a\x00b", "control character"},
		{operand.MountField, `/mnt/a\040b`, "escape"},
	} {
		t.Run(string(tc.kind)+" "+tc.text, func(t *testing.T) {
			got := tc.kind.Problem(tc.text)
			if tc.want == "" {
				if got != "" {
					t.Fatalf("%s.Problem(%q) = %q, want none", tc.kind, tc.text, got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("%s.Problem(%q) = %q, want it to say %q", tc.kind, tc.text, got, tc.want)
			}
		})
	}
}

// TestKind_UnknownRefusesEverything: a kind this build does not know
// passes nothing, so a format mapped to a kind added later fails closed.
func TestKind_UnknownRefusesEverything(t *testing.T) {
	if operand.Kind("later").Problem("curl") == "" {
		t.Fatal("an unknown kind passed a value")
	}
	if err := operand.AptPackage.Check("name", "jq/bookworm"); err == nil || !strings.HasPrefix(err.Error(), "name is refused: ") || strings.Contains(err.Error(), "bookworm") {
		t.Fatalf("Check = %v, want it to name the parameter and not repeat the value", err)
	}
}

// FuzzKinds holds every kind to the properties the rules share over any
// text: no panic; a word beginning with "-" refused by every kind, except
// systemd's two root units by SystemdUnit; a pattern character refused by
// every kind but MountField, whose programs read one as itself; and Package
// admitting exactly what AptPackage or RPMPackage admits.
func FuzzKinds(f *testing.F) {
	for _, seed := range []string{
		"curl", "g++", "-.mount", "-.slice", "-f", "lib*", "jq/bookworm", "jq=1", "diffutils-",
		"@core", "/usr/bin/jq", "perl(x)", "1:3.8-4", "1.0-", "/dev/sdb1\n", "a b", "#x", `\040`, "", "\x00",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		for _, kind := range operand.Kinds() {
			problem := kind.Problem(text)
			root := kind == operand.SystemdUnit && (text == "-.mount" || text == "-.slice")
			if strings.HasPrefix(text, "-") && !root && problem == "" {
				t.Fatalf("%s admitted %q, which begins with a dash", kind, text)
			}
			if strings.ContainsAny(text, "*?[]") && kind != operand.MountField && problem == "" {
				t.Fatalf("%s admitted %q, which holds a pattern character", kind, text)
			}
		}
		either := operand.AptPackage.Problem(text) == "" || operand.RPMPackage.Problem(text) == ""
		if got := operand.Package.Problem(text) == ""; got != either {
			t.Fatalf("Package admitted %q: %v, while APT or RPM admitted it: %v", text, got, either)
		}
	})
}
