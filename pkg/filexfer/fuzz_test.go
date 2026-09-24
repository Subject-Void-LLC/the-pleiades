// Fuzz targets for the path resolver and the physical containment check.
package filexfer_test

import (
	"errors"
	"path"
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer/filexfertest"
)

// FuzzResolve attacks the resolver with arbitrary roots and leaves,
// seeded with every shared escape payload. It asserts properties, not
// merely the absence of a panic:
//
//   - an accepted path lies strictly below its root, is in its simplest
//     form, holds no ".." segment, NUL, backslash or control character,
//     and resolves to itself again;
//   - a refusal is always a *filexfer.PathError, and when it blames one
//     segment the error text quotes exactly that segment, so the
//     operator can find it.
func FuzzResolve(f *testing.F) {
	for _, p := range filexfertest.EscapePayloads {
		f.Add("/srv/xfer", p.Leaf)
	}
	for _, leaf := range filexfertest.AllowedLeaves {
		f.Add("/srv/xfer", leaf)
		f.Add("/", leaf)
	}
	for _, root := range []string{"", "relative", `C:\xfer`, "/srv/", "/srv/../etc", "/a\x00b"} {
		f.Add(root, "file")
	}

	f.Fuzz(func(t *testing.T, root, leaf string) {
		p, err := filexfer.Resolve(root, leaf)
		if err != nil {
			var pathErr *filexfer.PathError
			if !errors.As(err, &pathErr) {
				t.Fatalf("Resolve(%q, %q) error is %T, want *filexfer.PathError", root, leaf, err)
			}
			if pathErr.Segment != "" && !strings.Contains(err.Error(), strconv.Quote(pathErr.Segment)) {
				t.Fatalf("error %q does not quote the segment %q it blames", err, pathErr.Segment)
			}
			if !p.IsZero() {
				t.Fatalf("Resolve(%q, %q) returned a Path alongside its error", root, leaf)
			}
			return
		}

		full := p.String()
		if !filexfer.Within(root, full) || full == root {
			t.Fatalf("Resolve(%q, %q) = %q, which is not strictly below the root", root, leaf, full)
		}
		if path.Clean(full) != full {
			t.Fatalf("Resolve(%q, %q) = %q, which is not in its simplest form", root, leaf, full)
		}
		for _, segment := range strings.Split(p.Rel(), "/") {
			if segment == ".." || segment == "." || segment == "" {
				t.Fatalf("Resolve(%q, %q) kept segment %q", root, leaf, segment)
			}
		}
		if !utf8.ValidString(full) || strings.ContainsAny(full, "\x00\\") {
			t.Fatalf("Resolve(%q, %q) = %q holds NUL, a backslash or invalid UTF-8", root, leaf, full)
		}
		for _, r := range full {
			if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
				t.Fatalf("Resolve(%q, %q) = %q holds control character %U", root, leaf, full, r)
			}
		}
		again, err := filexfer.Resolve(p.Root(), p.Rel())
		if err != nil || again != p {
			t.Fatalf("Resolve(%q, %q) = %q does not resolve to itself again: %q, %v", root, leaf, full, again, err)
		}
	})
}

// FuzzContained attacks the physical comparison with arbitrary device
// answers. A device is a trusted-ish upstream, so whatever it says must
// never panic, and a "contained" verdict must only ever be given for two
// absolute, canonical answers where the parent lies within the root.
func FuzzContained(f *testing.F) {
	f.Add("/data/xfer", "/data/xfer/sub")
	f.Add("/data/xfer", "/data/xferX")
	f.Add("/", "/etc")
	f.Add("/data/xfer", "/data/xfer/../../etc")
	f.Add("relative", "/x")
	f.Add("/data/xfer\n", "/data/xfer\n/sub")
	f.Add("/a\x00", "/a\x00/b")

	p, err := filexfer.Resolve("/srv/xfer", "f")
	if err != nil {
		f.Fatalf("Resolve() error = %v", err)
	}
	f.Fuzz(func(t *testing.T, root, parent string) {
		err := filexfer.Contained(p, root, parent)
		if err != nil {
			var cErr *filexfer.ContainmentError
			if !errors.As(err, &cErr) {
				t.Fatalf("Contained(%q, %q) error is %T, want *filexfer.ContainmentError", root, parent, err)
			}
			return
		}
		if !strings.HasPrefix(root, "/") || path.Clean(root) != root || path.Clean(parent) != parent {
			t.Fatalf("Contained(%q, %q) trusted an answer that is not absolute and canonical", root, parent)
		}
		if root != "/" && parent != root && !strings.HasPrefix(parent, root+"/") {
			t.Fatalf("Contained(%q, %q) accepted a parent outside the root", root, parent)
		}
	})
}
