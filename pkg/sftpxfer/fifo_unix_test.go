//go:build unix

// Tests that a FIFO is never opened, on the platforms that have one.
package sftpxfer

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filexfer"
)

// TestConfine_FIFOIsNeverOpened covers the denial of service a
// symlink-only check misses: opening a FIFO for reading blocks until
// something writes to it, so a Get of one would hang for as long as its
// deadline allowed. It must be refused from its Lstat, fast, in both
// directions.
func TestConfine_FIFOIsNeverOpened(t *testing.T) {
	f := newFixture(t)
	p := f.resolve(t, "pipe")
	if err := syscall.Mkfifo(p.String(), 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mark := f.rec.mark()
	_, getErr := f.client.Get(ctx, p, &bytes.Buffer{}, 1<<20)
	putErr := f.client.Put(ctx, p, strings.NewReader("x"), 1, 0o644)
	f.assertNothingMutated(t, mark)

	for name, err := range map[string]error{"Get": getErr, "Put": putErr} {
		var cErr *filexfer.ContainmentError
		if !errors.As(err, &cErr) || cErr.Reason != filexfer.ContainmentNotRegular {
			t.Errorf("%s() of a FIFO error = %v, want ContainmentNotRegular", name, err)
		}
	}
	info, err := f.client.Stat(ctx, p)
	if err != nil || info.Kind != filexfer.KindOther {
		t.Errorf("Stat() of a FIFO = %+v, %v; want KindOther", info, err)
	}
}
