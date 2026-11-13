// Tests that a probe which could not look never answers "not there"
// (Phase 131, R4): a path under a directory the login cannot search is an
// error naming that directory from Stat, Read and Checksum alike, while a
// path that is really not there, one under a missing directory, and one
// under a regular file are still absent.
package remotefile_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remotefile"
)

// TestAProbeThatCannotLookIsNotAbsent: test -e and test -f answer false for
// a path whose directory the login may not search, the same false they
// answer for a path that is not there. Each probe must tell the two apart:
// a file that is there, under a directory closed to this login, is
// ErrCannotLook, at any depth under that directory, never absent.
func TestAProbeThatCannotLookIsNotAbsent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root searches every directory, so no directory can be closed to this test")
	}
	conn := connect(t)
	ctx := context.Background()

	closed := filepath.Join(t.TempDir(), "closed")
	if err := os.MkdirAll(filepath.Join(closed, "inner"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"there", filepath.Join("inner", "deeper")} {
		if err := os.WriteFile(filepath.Join(closed, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(closed, 0o000); err != nil {
		t.Fatal(err)
	}
	// Opened again before TempDir's own cleanup removes it.
	t.Cleanup(func() { _ = os.Chmod(closed, 0o700) })

	probes := map[string]func(string) (bool, error){
		"Stat": func(p string) (bool, error) {
			info, err := remotefile.Stat(ctx, conn, p)
			return info.Exists(), err
		},
		"Read": func(p string) (bool, error) {
			_, found, err := remotefile.Read(ctx, conn, p)
			return found, err
		},
		"Checksum": func(p string) (bool, error) {
			_, found, err := remotefile.Checksum(ctx, conn, p)
			return found, err
		},
	}
	for probe, look := range probes {
		for _, path := range []string{
			filepath.Join(closed, "there"),
			filepath.Join(closed, "inner", "deeper"),
			filepath.Join(closed, "not-there-either"),
		} {
			found, err := look(path)
			if !errors.Is(err, remotefile.ErrCannotLook) {
				t.Errorf("%s(%s) = found %v, err %v; want ErrCannotLook, never absent", probe, path, found, err)
				continue
			}
			if !strings.Contains(err.Error(), closed) {
				t.Errorf("%s(%s): the error does not name the directory it could not search: %v", probe, path, err)
			}
		}
	}
}

// TestAProbeStillAnswersAbsence is the control for the test above: what is
// really not there, under a directory the login can search, under one that
// is itself not there, or under a regular file, is absent with no error,
// from every probe.
func TestAProbeStillAnswersAbsence(t *testing.T) {
	conn := connect(t)
	ctx := context.Background()
	open := t.TempDir()
	file := filepath.Join(open, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(open, "nothing"),
		filepath.Join(open, "no-dir", "no-file"),
		filepath.Join(file, "under-a-file"),
		"relative-and-not-there",
	} {
		info, err := remotefile.Stat(ctx, conn, path)
		if err != nil || info.Exists() {
			t.Errorf("Stat(%s) = %+v, %v; want absent", path, info, err)
		}
		if _, found, err := remotefile.Read(ctx, conn, path); err != nil || found {
			t.Errorf("Read(%s) = found %v, %v; want absent", path, found, err)
		}
		if _, found, err := remotefile.Checksum(ctx, conn, path); err != nil || found {
			t.Errorf("Checksum(%s) = found %v, %v; want absent", path, found, err)
		}
	}
}
