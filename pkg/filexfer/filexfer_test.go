// Tests for the mode rules, the argument checks every Store runs first, and
// the names every refusal prints.
package filexfer_test

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer"
)

// TestMode_Valid pins which bits a transfer may set: the nine rwx bits
// and nothing else, so a setuid file cannot be created by a transfer,
// and a decimal typo is caught because it lands on a special bit.
func TestMode_Valid(t *testing.T) {
	tests := []struct {
		mode filexfer.Mode
		want bool
	}{
		{0o644, true},
		{0o600, true},
		{0o777, true},
		{0, true},
		{0o4755, false}, // setuid
		{0o2755, false}, // setgid
		{0o1777, false}, // sticky
		{644, false},    // the decimal typo: 0o1204
		{0o10000, false},
	}
	for _, tc := range tests {
		if got := tc.mode.Valid(); got != tc.want {
			t.Errorf("Mode(%s).Valid() = %v, want %v", tc.mode, got, tc.want)
		}
	}
	if got := filexfer.Mode(0o640).String(); got != "0640" {
		t.Errorf("Mode(0o640).String() = %q, want 0640", got)
	}
	if got := filexfer.Mode(0o4755).Perm(); got != fs.FileMode(0o755) {
		t.Errorf("Mode(0o4755).Perm() = %v, want only the permission bits", got)
	}
}

// TestCheckPut_RefusesBeforeAnyNetworkCall covers the argument checks
// every Store runs first.
func TestCheckPut_RefusesBeforeAnyNetworkCall(t *testing.T) {
	good, _ := filexfer.Resolve("/srv/xfer", "f")
	tests := []struct {
		name string
		dst  filexfer.Path
		size int64
		mode filexfer.Mode
		want error
	}{
		{"zero path", filexfer.Path{}, 1, 0o644, filexfer.ErrUnresolvedPath},
		{"negative size", good, -1, 0o644, filexfer.ErrSizeMismatch},
		{"setuid", good, 1, 0o4755, filexfer.ErrInvalidMode},
		{"ok", good, 0, 0o644, nil},
	}
	for _, tc := range tests {
		if err := filexfer.CheckPut(tc.dst, tc.size, tc.mode); !errors.Is(err, tc.want) || (tc.want == nil) != (err == nil) {
			t.Errorf("%s: CheckPut() error = %v, want %v", tc.name, err, tc.want)
		}
	}
}

// TestCheckGet_RefusesBeforeAnyNetworkCall covers Get's argument checks.
func TestCheckGet_RefusesBeforeAnyNetworkCall(t *testing.T) {
	good, _ := filexfer.Resolve("/srv/xfer", "f")
	if err := filexfer.CheckGet(filexfer.Path{}, 10); !errors.Is(err, filexfer.ErrUnresolvedPath) {
		t.Errorf("CheckGet(zero) error = %v, want ErrUnresolvedPath", err)
	}
	if err := filexfer.CheckGet(good, -1); !errors.Is(err, filexfer.ErrLimitExceeded) {
		t.Errorf("CheckGet(limit -1) error = %v, want ErrLimitExceeded", err)
	}
	if err := filexfer.CheckGet(good, 0); err != nil {
		t.Errorf("CheckGet(limit 0) error = %v, want nil", err)
	}
}

// TestStringers_NameEveryValue keeps every enum's String total, so a
// value added later without a name is caught here rather than printed
// as a bare number in an operator's error message.
func TestStringers_NameEveryValue(t *testing.T) {
	for k := filexfer.KindRegular; k <= filexfer.KindOther; k++ {
		if strings.Contains(k.String(), "unknown") {
			t.Errorf("Kind(%d) has no name", k)
		}
	}
	if !strings.Contains(filexfer.Kind(0).String(), "unknown") {
		t.Error("Kind(0) should be named unknown")
	}
	for r := filexfer.RefusedEmpty; r <= filexfer.RefusedOutsideRoot; r++ {
		if r.String() == "it is not allowed" {
			t.Errorf("Refusal(%d) has no specific explanation", r)
		}
	}
	if filexfer.Refusal(0).String() != "it is not allowed" {
		t.Error("Refusal(0) should fall back to the generic explanation")
	}
	for c := filexfer.ContainmentRootMissing; c <= filexfer.ContainmentUntrustedAnswer; c++ {
		if c.String() == "the target is not contained in the transfer root" {
			t.Errorf("Containment(%d) has no specific explanation", c)
		}
	}
	if filexfer.Containment(0).String() != "the target is not contained in the transfer root" {
		t.Error("Containment(0) should fall back to the generic explanation")
	}
	if filexfer.PathPart(0).String() != "path" {
		t.Error("PathPart(0) should be named path")
	}
}

// TestContainmentError_WithoutDeviceAnswers renders the short form a
// leaf-kind refusal uses, which has no device answers to report.
func TestContainmentError_WithoutDeviceAnswers(t *testing.T) {
	err := &filexfer.ContainmentError{Path: "/srv/xfer/f", Reason: filexfer.ContainmentSymlinkLeaf}
	if strings.Contains(err.Error(), "device resolved") {
		t.Errorf("error %q reports device answers it does not have", err)
	}
}
