// Tests for the path resolver and the containment comparisons.
package filexfer_test

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer/filexfertest"
)

// TestResolve_RefusesEveryEscapePayload runs the shared payload table,
// the same one each protocol adapter's adversarial test uses, and
// asserts each refusal is a typed PathError naming the leaf.
func TestResolve_RefusesEveryEscapePayload(t *testing.T) {
	for _, tc := range filexfertest.EscapePayloads {
		t.Run(tc.Name, func(t *testing.T) {
			p, err := filexfer.Resolve("/srv/xfer", tc.Leaf)
			if err == nil {
				t.Fatalf("Resolve(%q) = %q, want a refusal", tc.Leaf, p)
			}
			var pathErr *filexfer.PathError
			if !errors.As(err, &pathErr) {
				t.Fatalf("Resolve(%q) error = %T %v, want *filexfer.PathError", tc.Leaf, err, err)
			}
			if pathErr.Part != filexfer.PartLeaf {
				t.Errorf("Resolve(%q) blamed the %s, want the leaf", tc.Leaf, pathErr.Part)
			}
			if !p.IsZero() {
				t.Errorf("Resolve(%q) returned a non-zero Path alongside its error", tc.Leaf)
			}
		})
	}
}

// TestResolve_AcceptsOrdinaryNames guards against a guard so eager it
// refuses real filenames: dots inside a name, an encoded-looking name,
// a colon, a space, non-ASCII.
func TestResolve_AcceptsOrdinaryNames(t *testing.T) {
	for _, leaf := range filexfertest.AllowedLeaves {
		p, err := filexfer.Resolve("/srv/xfer", leaf)
		if err != nil {
			t.Errorf("Resolve(%q) error = %v, want it accepted", leaf, err)
			continue
		}
		if got, want := p.String(), "/srv/xfer/"+leaf; got != want {
			t.Errorf("Resolve(%q).String() = %q, want %q", leaf, got, want)
		}
		if p.Rel() != leaf || p.Root() != "/srv/xfer" {
			t.Errorf("Resolve(%q) kept root %q and rel %q", leaf, p.Root(), p.Rel())
		}
	}
}

// TestResolve_NamesTheOffendingSegment is the phase's "fail closed with
// an error naming the offending hop": the one segment at fault, quoted
// so a control character in it is visible rather than printed raw.
func TestResolve_NamesTheOffendingSegment(t *testing.T) {
	tests := []struct {
		leaf    string
		segment string
		refusal filexfer.Refusal
	}{
		{"a/b/../../c", "..", filexfer.RefusedDotSegment},
		{"ok/bad\\name/x", "bad\\name", filexfer.RefusedBackslash},
		{"ok/n\x00ul", "n\x00ul", filexfer.RefusedControl},
		{"ok/line\nbreak", "line\nbreak", filexfer.RefusedControl},
		{"ok/\xc0\xae", "\xc0\xae", filexfer.RefusedInvalidUTF8},
		{"ok/" + strings.Repeat("z", 256), strings.Repeat("z", 256), filexfer.RefusedSegmentTooLong},
	}
	for _, tc := range tests {
		_, err := filexfer.Resolve("/srv/xfer", tc.leaf)
		var pathErr *filexfer.PathError
		if !errors.As(err, &pathErr) {
			t.Fatalf("Resolve(%q) error = %v, want *filexfer.PathError", tc.leaf, err)
		}
		if pathErr.Segment != tc.segment || pathErr.Refusal != tc.refusal {
			t.Errorf("Resolve(%q) blamed segment %q for %v, want %q for %v",
				tc.leaf, pathErr.Segment, pathErr.Refusal, tc.segment, tc.refusal)
		}
		if !strings.Contains(err.Error(), `segment "`) {
			t.Errorf("Resolve(%q) error %q does not name a segment", tc.leaf, err)
		}
		if strings.ContainsAny(err.Error(), "\x00\n\r") {
			t.Errorf("Resolve(%q) error %q prints a control character raw", tc.leaf, err)
		}
	}
}

// TestResolve_NotCanonicalOffersTheFix mirrors internal/playbook's
// ValidateReference: a path with a spelling path.Clean would change is
// refused, and the error says what to write instead.
func TestResolve_NotCanonicalOffersTheFix(t *testing.T) {
	_, err := filexfer.Resolve("/srv/xfer", "a//b")
	var pathErr *filexfer.PathError
	if !errors.As(err, &pathErr) || pathErr.Refusal != filexfer.RefusedNotCanonical {
		t.Fatalf("Resolve(a//b) error = %v, want RefusedNotCanonical", err)
	}
	if pathErr.Canonical != "a/b" || !strings.Contains(err.Error(), `write it as "a/b"`) {
		t.Errorf("Resolve(a//b) error = %q, want it to offer \"a/b\"", err)
	}
}

// TestResolve_WindowsControllerCannotFailOpen is the phase's
// controller-runs-on-Windows case. The resolver imports path, never
// path/filepath, and internal/archtest's
// TestRemotePathCodeNeverImportsFilepath enforces that, so its answer
// cannot depend on the controller's separator. This test pins the other
// half: every spelling a Windows controller's filepath.Join could
// produce is refused by the backslash rule on EVERY operating system,
// including the one this test runs on, and the answer never contains a
// backslash. filepath.Separator is consulted only to build the inputs a
// Windows controller would.
func TestResolve_WindowsControllerCannotFailOpen(t *testing.T) {
	windowsJoined := []string{
		`..\..\etc\passwd`,
		`sub\..\..\etc`,
		`C:\xfer\file`,
		`a\b`,
		strings.ReplaceAll("../../etc/passwd", "/", `\`),
	}
	for _, leaf := range windowsJoined {
		if _, err := filexfer.Resolve("/srv/xfer", leaf); err == nil {
			t.Errorf("Resolve(%q) accepted a Windows-joined path", leaf)
		}
	}
	for _, root := range []string{`C:\xfer`, `\\server\share`, `/srv\xfer`} {
		if err := filexfer.ValidateRoot(root); err == nil {
			t.Errorf("ValidateRoot(%q) accepted a Windows-style root", root)
		}
	}
	// On this controller filepath.Join is the POSIX join; on Windows it
	// would be the backslash one. Either way the resolver's answer for
	// the same logical path is the forward-slashed one.
	logical := filepath.Join("a", "b")
	if filepath.Separator == '\\' {
		if _, err := filexfer.Resolve("/srv/xfer", logical); err == nil {
			t.Errorf("Resolve(%q) accepted a backslash-joined path on Windows", logical)
		}
	} else {
		p, err := filexfer.Resolve("/srv/xfer", logical)
		if err != nil || strings.Contains(p.String(), `\`) {
			t.Errorf("Resolve(%q) = %q, %v; want a forward-slashed path", logical, p, err)
		}
	}
}

// TestValidateRoot_Table covers the inventory side of the path.
func TestValidateRoot_Table(t *testing.T) {
	tests := []struct {
		root    string
		refusal filexfer.Refusal // zero means accepted
	}{
		{"/", 0},
		{"/srv/xfer", 0},
		{"/srv/xfer with space", 0},
		{"", filexfer.RefusedEmpty},
		{"srv/xfer", filexfer.RefusedRelativeRoot},
		{"~/xfer", filexfer.RefusedRelativeRoot},
		{"/srv/xfer/", filexfer.RefusedNotCanonical},
		{"/srv/../etc", filexfer.RefusedNotCanonical},
		{"/srv/./xfer", filexfer.RefusedNotCanonical},
		{"//srv", filexfer.RefusedNotCanonical},
		{"/srv\x00/xfer", filexfer.RefusedControl},
		{"/srv/\xc0\xae", filexfer.RefusedInvalidUTF8},
		{"/" + strings.Repeat("a", 4096), filexfer.RefusedTooLong},
		{"/" + strings.Repeat("a", 256), filexfer.RefusedSegmentTooLong},
	}
	for _, tc := range tests {
		err := filexfer.ValidateRoot(tc.root)
		if tc.refusal == 0 {
			if err != nil {
				t.Errorf("ValidateRoot(%q) error = %v, want nil", tc.root, err)
			}
			continue
		}
		var pathErr *filexfer.PathError
		if !errors.As(err, &pathErr) || pathErr.Refusal != tc.refusal || pathErr.Part != filexfer.PartRoot {
			t.Errorf("ValidateRoot(%q) error = %v, want a root refusal for %v", tc.root, err, tc.refusal)
		}
	}
}

// TestResolve_RootSlashContainsEverything is the bug a naive prefix
// check against root+"/" has: with root "/", every path fails "//".
func TestResolve_RootSlashContainsEverything(t *testing.T) {
	p, err := filexfer.Resolve("/", "etc/motd")
	if err != nil || p.String() != "/etc/motd" || p.Dir() != "/etc" || p.Base() != "motd" {
		t.Fatalf("Resolve(\"/\", \"etc/motd\") = %q (dir %q, base %q), %v", p, p.Dir(), p.Base(), err)
	}
}

// TestResolve_RefusesAnInvalidRootFirst pins the order: a bad root is
// reported as the root's fault even when the leaf is bad too, since the
// root is what an operator must fix.
func TestResolve_RefusesAnInvalidRootFirst(t *testing.T) {
	_, err := filexfer.Resolve("relative", "../x")
	var pathErr *filexfer.PathError
	if !errors.As(err, &pathErr) || pathErr.Part != filexfer.PartRoot {
		t.Fatalf("Resolve with a bad root and a bad leaf error = %v, want the root blamed", err)
	}
	if !strings.Contains(err.Error(), "transfer root") {
		t.Errorf("error %q does not say it is about the transfer root", err)
	}
}

// TestWithin_Table covers segment-wise containment.
func TestWithin_Table(t *testing.T) {
	tests := []struct {
		root, p string
		want    bool
	}{
		{"/srv/xfer", "/srv/xfer", true},
		{"/srv/xfer", "/srv/xfer/a", true},
		{"/srv/xfer", "/srv/xferX", false},
		{"/srv/xfer", "/srv/xferX/a", false},
		{"/srv/xfer", "/srv", false},
		{"/srv/xfer", "srv/xfer/a", false},
		{"/", "/", true},
		{"/", "/anything/at/all", true},
		{"/", "relative", false},
	}
	for _, tc := range tests {
		if got := filexfer.Within(tc.root, tc.p); got != tc.want {
			t.Errorf("Within(%q, %q) = %v, want %v", tc.root, tc.p, got, tc.want)
		}
	}
}

// TestContained_ComparesTheDevicesOwnAnswers covers the physical half,
// including the answers it must refuse to compare at all.
func TestContained_ComparesTheDevicesOwnAnswers(t *testing.T) {
	p, err := filexfer.Resolve("/srv/xfer", "shared/drop/file")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	tests := []struct {
		name         string
		root, parent string
		want         filexfer.Containment // zero means contained
	}{
		{"inside", "/data/xfer", "/data/xfer/shared/drop", 0},
		{"root symlinked elsewhere is fine", "/mnt/real", "/mnt/real/shared/drop", 0},
		{"parent symlinked out", "/data/xfer", "/config/.ssh", filexfer.ContainmentOutsideRoot},
		{"sibling prefix", "/data/xfer", "/data/xferX/shared", filexfer.ContainmentOutsideRoot},
		{"relative answer", "/data/xfer", "data/xfer/shared", filexfer.ContainmentUntrustedAnswer},
		{"unclean answer", "/data/xfer", "/data/xfer/../../etc", filexfer.ContainmentUntrustedAnswer},
		{"NUL in answer", "/data/xfer", "/data/xfer/\x00", filexfer.ContainmentUntrustedAnswer},
		{"empty root answer", "", "/data/xfer", filexfer.ContainmentUntrustedAnswer},
	}
	for _, tc := range tests {
		err := filexfer.Contained(p, tc.root, tc.parent)
		if tc.want == 0 {
			if err != nil {
				t.Errorf("%s: Contained() error = %v, want nil", tc.name, err)
			}
			continue
		}
		var cErr *filexfer.ContainmentError
		if !errors.As(err, &cErr) || cErr.Reason != tc.want {
			t.Errorf("%s: Contained() error = %v, want %v", tc.name, err, tc.want)
			continue
		}
		if !strings.Contains(err.Error(), p.String()) {
			t.Errorf("%s: error %q does not name the requested path", tc.name, err)
		}
	}
}

// TestLeafKind_Table covers what each direction accepts at the final
// component.
func TestLeafKind_Table(t *testing.T) {
	p, _ := filexfer.Resolve("/srv/xfer", "f")
	tests := []struct {
		name          string
		kind          filexfer.Kind
		exists, isGet bool
		want          filexfer.Containment // zero means accepted
		notExist      bool
	}{
		{"put to a new file", 0, false, false, 0, false},
		{"put over a regular file", filexfer.KindRegular, true, false, 0, false},
		{"put over a symlink", filexfer.KindSymlink, true, false, filexfer.ContainmentSymlinkLeaf, false},
		{"put over a directory", filexfer.KindDirectory, true, false, filexfer.ContainmentDirectoryLeaf, false},
		{"put over a FIFO", filexfer.KindOther, true, false, filexfer.ContainmentNotRegular, false},
		{"get a regular file", filexfer.KindRegular, true, true, 0, false},
		{"get a symlink", filexfer.KindSymlink, true, true, filexfer.ContainmentSymlinkLeaf, false},
		{"get a FIFO", filexfer.KindOther, true, true, filexfer.ContainmentNotRegular, false},
		{"get a missing file", 0, false, true, 0, true},
	}
	for _, tc := range tests {
		err := filexfer.LeafKind(p, tc.kind, tc.exists, tc.isGet)
		switch {
		case tc.notExist:
			if !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("%s: error = %v, want fs.ErrNotExist", tc.name, err)
			}
		case tc.want == 0:
			if err != nil {
				t.Errorf("%s: error = %v, want nil", tc.name, err)
			}
		default:
			var cErr *filexfer.ContainmentError
			if !errors.As(err, &cErr) || cErr.Reason != tc.want {
				t.Errorf("%s: error = %v, want %v", tc.name, err, tc.want)
			}
		}
	}
}

// TestPath_ZeroValueIsRecognized pins the property every Store's first
// refusal depends on.
func TestPath_ZeroValueIsRecognized(t *testing.T) {
	var zero filexfer.Path
	if !zero.IsZero() {
		t.Fatal("the zero Path does not report IsZero")
	}
	p, _ := filexfer.Resolve("/srv", "a")
	if p.IsZero() {
		t.Fatal("a resolved Path reports IsZero")
	}
}
