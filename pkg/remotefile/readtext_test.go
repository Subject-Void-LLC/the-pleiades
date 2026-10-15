// Tests for ReadText against real files on a real SSH session.
package remotefile_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
)

// TestReadText_ReadsTextAndRefusesEveryOtherShape: a regular UTF-8 file
// is read whole with its attributes; an absent path, a directory, a
// symlink, a file over the bound and bytes that are not UTF-8 are each
// refused by name rather than read.
func TestReadText_ReadsTextAndRefusesEveryOtherShape(t *testing.T) {
	conn := connect(t)
	dir := t.TempDir()
	write := func(name string, content []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, content, 0o640); err != nil {
			t.Fatal(err)
		}
		return p
	}
	text := write("Dockerfile", []byte("FROM alpine:3.20\n"))
	got, info, err := remotefile.ReadText(context.Background(), conn, text, 64)
	if err != nil || string(got) != "FROM alpine:3.20\n" || info.Kind != remotefile.KindFile || info.Mode != "0640" {
		t.Fatalf("ReadText = %q, %+v, %v", got, info, err)
	}
	exact := write("exact", []byte(strings.Repeat("a", 64)))
	if _, _, err := remotefile.ReadText(context.Background(), conn, exact, 64); err != nil {
		t.Fatalf("a file of exactly the bound = %v", err)
	}

	link := filepath.Join(dir, "link")
	if err := os.Symlink(text, link); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		path string
		want string
	}{
		"absent":         {filepath.Join(dir, "missing"), "does not exist on the device"},
		"a directory":    {dir, "is a directory, not a regular file"},
		"a symlink":      {link, "is a symlink, not a regular file"},
		"over the bound": {write("big", []byte(strings.Repeat("a", 65))), "over the 64 bytes this reads"},
		"not UTF-8":      {write("binary", []byte{0xff, 0xfe, 'a'}), "is not UTF-8 text"},
	} {
		t.Run(name, func(t *testing.T) {
			got, _, err := remotefile.ReadText(context.Background(), conn, tc.path, 64)
			if err == nil || !strings.Contains(err.Error(), tc.want) || got != nil {
				t.Fatalf("ReadText(%s) = %q, %v; want an error saying %q", tc.path, got, err, tc.want)
			}
		})
	}
}
