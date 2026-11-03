// container.image.save's archive, made the same bytes for the same images on
// every engine. Classic Docker (28, overlay2) stamps the archive's
// blobs/sha256/ directory entry with the time of the save, so two saves of
// one image in different seconds differ, though every file in them is the
// same: a check's predicted digest then missed the real one, and saving
// again was a change (FAILURE_PATTERNS 843). Every entry's times are set to
// the Unix epoch as the archive streams through, which no load reads.
package image

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"time"
)

// epoch is the time every normalized entry carries.
var epoch = time.Unix(0, 0).UTC()

// normalizedArchive returns r, an engine's image archive, re-written entry
// by entry with each entry's times set to the epoch and its contents
// unchanged. It reads r in a goroutine through a pipe, so the archive is
// never held whole; closing the result stops that goroutine. An archive
// that is not a tar fails the read with its error.
func normalizedArchive(r io.Reader) io.ReadCloser {
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(normalize(r, pw))
	}()
	return pr
}

// normalize copies the tar archive from r to w with every entry's times at
// the epoch.
func normalize(r io.Reader, w io.Writer) error {
	tr := tar.NewReader(r)
	tw := tar.NewWriter(w)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("reading the engine's archive: %w", err)
		}
		hdr.ModTime, hdr.AccessTime, hdr.ChangeTime = epoch, time.Time{}, time.Time{}
		// A PAX record of a time would carry the engine's value through.
		for _, key := range []string{"mtime", "atime", "ctime"} {
			delete(hdr.PAXRecords, key)
		}
		// The engine's own header format is dropped, so the writer picks
		// one from what the entry needs, the same for the same entry.
		hdr.Format = tar.FormatUnknown
		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("writing the archive: %w", err)
		}
		if _, err := io.Copy(tw, tr); err != nil {
			return fmt.Errorf("copying %s: %w", hdr.Name, err)
		}
	}
	return tw.Close()
}
