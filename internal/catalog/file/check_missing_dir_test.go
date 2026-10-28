// Package file_test: a check writing into a directory that does not exist
// yet. An earlier task in the same run (file.directory) may make it, and a
// check runs no task, so file.copy's and file.template's checks answer that
// they cannot check the call, naming the directory, while a real run still
// fails saying the directory does not exist.
package file_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

func TestCheck_AMissingDirectoryCannotBeCheckedAndARunStillFails(t *testing.T) {
	server := startCopyServer(t)
	device := newCopyTarget(server)
	for _, fqcn := range []string{"file.copy", "file.template"} {
		t.Run(fqcn, func(t *testing.T) {
			d, ok := collection.Lookup(fqcn)
			if !ok || d.Check == nil || d.Invoke == nil {
				t.Fatalf("%s is not registered with a check", fqcn)
			}
			dir := filepath.Join(t.TempDir(), "made-later")
			dest := filepath.Join(dir, "app.conf")

			_, err := d.Check(context.Background(), newCopyContext(server), device, templateParams(dest, "env=prod\n"))
			var cannot *collection.CannotCheckError
			if !errors.As(err, &cannot) {
				t.Fatalf("check = %v, want a cannot-check answer", err)
			}
			if !strings.Contains(cannot.Reason, dir) {
				t.Errorf("the reason %q does not name the directory %s", cannot.Reason, dir)
			}

			_, err = d.Invoke(context.Background(), newCopyContext(server), device, templateParams(dest, "env=prod\n"))
			if err == nil || errors.As(err, &cannot) || !strings.Contains(err.Error(), "its directory "+dir+" does not exist") {
				t.Errorf("run = %v, want the directory named as missing", err)
			}
		})
	}
}
