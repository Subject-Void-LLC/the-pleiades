package filters_test

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

func TestWindowsPathToPOSIX(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"drive_path", `C:\Users\foo\bar.txt`, "C:/Users/foo/bar.txt"},
		{"unc_path", `\\server\share\file`, "//server/share/file"},
		{"already_posix", "/already/posix", "/already/posix"},
		{"empty", "", ""},
		{"over_cap", strings.Repeat("a", filters.MaxInputBytes+1), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.WindowsPathToPOSIX(tc.in); got != tc.want {
				t.Errorf("WindowsPathToPOSIX(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestPOSIXPathToWindows(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"posix_path", "/home/foo/bar.txt", `\home\foo\bar.txt`},
		{"already_windows", `C:\already\windows`, `C:\already\windows`},
		{"empty", "", ""},
		{"over_cap", strings.Repeat("a", filters.MaxInputBytes+1), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.POSIXPathToWindows(tc.in); got != tc.want {
				t.Errorf("POSIXPathToWindows(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIsAbsolutePath(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"posix_absolute", "/etc/passwd", true},
		{"posix_relative", "etc/passwd", false},
		{"posix_dot_relative", "./etc/passwd", false},
		{"windows_drive_backslash", `C:\Users\foo`, true},
		{"windows_drive_forwardslash", "C:/Users/foo", true},
		{"windows_drive_relative", "C:foo", false},
		{"windows_unc", `\\server\share`, true},
		{"windows_relative", `Users\foo`, false},
		{"empty", "", false},
		{"over_cap", strings.Repeat("a", filters.MaxInputBytes+1), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.IsAbsolutePath(tc.in); got != tc.want {
				t.Errorf("IsAbsolutePath(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestOctalToSymbolicPerms(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"rwxr_xr_x", "755", "rwxr-xr-x"},
		{"with_redundant_leading_zero", "0755", "rwxr-xr-x"},
		{"rw_only", "644", "rw-r--r--"},
		{"no_perms", "000", "---------"},
		{"all_perms", "777", "rwxrwxrwx"},
		{"setuid_with_exec", "4755", "rwsr-xr-x"},
		{"setuid_without_exec", "4655", "rwSr-xr-x"},
		{"setgid_with_exec", "2751", "rwxr-s--x"},
		{"sticky_with_exec", "1777", "rwxrwxrwt"},
		{"sticky_without_exec", "1770", "rwxrwx--T"},
		{"all_special_bits", "7777", "rwsrwsrwt"},
		{"non_octal_char", "abc", ""},
		{"too_short", "12", ""},
		{"too_long", "12345", ""},
		{"empty", "", ""},
		{"over_cap", strings.Repeat("7", filters.MaxInputBytes+1), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.OctalToSymbolicPerms(tc.in); got != tc.want {
				t.Errorf("OctalToSymbolicPerms(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSymbolicToOctalPerms(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"rwxr_xr_x", "rwxr-xr-x", "755"},
		{"rw_only", "rw-r--r--", "644"},
		{"no_perms", "---------", "000"},
		{"all_perms", "rwxrwxrwx", "777"},
		{"setuid_with_exec", "rwsr-xr-x", "4755"},
		{"setuid_without_exec", "rwSr-xr-x", "4655"},
		{"setgid_with_exec", "rwxr-s--x", "2751"},
		{"sticky_with_exec", "rwxrwxrwt", "1777"},
		{"sticky_without_exec", "rwxrwx--T", "1770"},
		{"all_special_bits", "rwsrwsrwt", "7777"},
		{"wrong_length", "rwx", ""},
		{"invalid_read_char", "qwxrwxrwx", ""},
		{"invalid_write_char", "rqxrwxrwx", ""},
		{"invalid_execute_char", "rwxrwxrwq", ""},
		{"empty", "", ""},
		{"over_cap", strings.Repeat("r", filters.MaxInputBytes+1), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.SymbolicToOctalPerms(tc.in); got != tc.want {
				t.Errorf("SymbolicToOctalPerms(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestPermsRoundTrip is this phase's own Adversarial Pattern
// Justification requirement: OctalToSymbolicPerms/SymbolicToOctalPerms
// round-trip for every representative input. Every input here already
// uses the canonical digit count SymbolicToOctalPerms itself would
// produce (3 digits with no special bit, 4 with one), which is where
// the round trip is exact rather than lossy; SymbolicToOctalPerms's own
// doc comment explains the one input shape ("0755", a redundant leading
// zero) that is not.
func TestPermsRoundTrip(t *testing.T) {
	octals := []string{"755", "644", "000", "777", "4755", "4655", "2751", "1777", "1770", "7777"}
	for _, oct := range octals {
		sym := filters.OctalToSymbolicPerms(oct)
		if got := filters.SymbolicToOctalPerms(sym); got != oct {
			t.Errorf("round trip: OctalToSymbolicPerms(%q) = %q, SymbolicToOctalPerms(that) = %q, want %q", oct, sym, got, oct)
		}
	}
}

func TestPathJoin(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"simple", []string{"a", "b", "c"}, "a/b/c"},
		{"dot_dot_cleaned", []string{"a", "b", "..", "c"}, "a/c"},
		{"leading_dot_dot_kept", []string{"..", "..", "etc", "passwd"}, "../../etc/passwd"},
		{"dot_segment_dropped", []string{"a", ".", "b"}, "a/b"},
		{"absolute_first_part", []string{"/etc", "passwd"}, "/etc/passwd"},
		{"empty_parts_dropped", []string{"a", "", "b"}, "a/b"},
		{"single_part", []string{"only"}, "only"},
		{"no_parts", nil, ""},
		{"over_cap_part", []string{strings.Repeat("a", filters.MaxInputBytes+1)}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.PathJoin(tc.in); got != tc.want {
				t.Errorf("PathJoin(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestPathExtractExtension(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"simple", "archive.tar.gz", ".gz"},
		{"single_extension", "report.pdf", ".pdf"},
		{"no_extension", "README", ""},
		{"dotfile", ".bashrc", ".bashrc"},
		{"dot_in_directory_only", "a.b/c", ""},
		{"posix_path", "/var/log/syslog.1", ".1"},
		{"windows_path", `C:\Users\a\archive.tar.gz`, ".gz"},
		{"trailing_dot", "file.", "."},
		{"empty", "", ""},
		{"over_cap", strings.Repeat("a", filters.MaxInputBytes+1), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.PathExtractExtension(tc.in); got != tc.want {
				t.Errorf("PathExtractExtension(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
