// container.image.save's archive, made the same bytes for the same images on
// every engine (FAILURE_PATTERNS 843). Classic Docker (28, overlay2) differs
// from save to save in two ways, though the images are the same: it stamps
// the archive's blobs/sha256/ directory entry with the time of the save, and
// with more than one image it writes manifest.json's and index.json's lists
// in a different order each time (measured: three orders in twenty saves of
// three images). A check's predicted digest then missed the real one, and
// saving again was a change. As the archive streams through, every entry's
// times are set to the Unix epoch, only extended-header records an image
// needs are kept, and the two index files' lists are sorted, none of which a
// load reads.
package image

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// epoch is the time every normalized entry carries.
var epoch = time.Unix(0, 0).UTC()

// maxIndexBytes bounds an index file read whole to sort it: far more than
// any archive's manifest.json or index.json, which list one entry per image.
const maxIndexBytes = 16 << 20

// normalizedArchive returns r, an engine's image archive, re-written entry
// by entry as normalize describes. It reads r in a goroutine through a pipe,
// so the archive is never held whole; closing the result stops that
// goroutine. An archive that is not a tar fails the read with its error.
func normalizedArchive(r io.Reader) io.ReadCloser {
	pr, pw := io.Pipe()
	go func() {
		pw.CloseWithError(normalize(r, pw))
	}()
	return pr
}

// normalize copies the tar archive from r to w with every entry's times at
// the epoch, its extended records reduced to the xattrs, and the lists of
// the root manifest.json and index.json in a canonical order.
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
		keepRecords(hdr)
		hdr.Format = tar.FormatUnknown
		// A global header may carry nothing but its records (archive/tar
		// refuses it a time); every other entry carries the epoch.
		if hdr.Typeflag != tar.TypeXGlobalHeader {
			hdr.ModTime, hdr.AccessTime, hdr.ChangeTime = epoch, time.Time{}, time.Time{}
		}
		body := io.Reader(tr)
		if hdr.Typeflag == tar.TypeReg && (hdr.Name == "manifest.json" || hdr.Name == "index.json") {
			sorted, err := sortedIndex(tr, hdr)
			if err != nil {
				return err
			}
			hdr.Size, body = int64(len(sorted)), bytes.NewReader(sorted)
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("writing the archive: %w", err)
		}
		if _, err := io.Copy(tw, body); err != nil {
			return fmt.Errorf("copying %q: %w", remoteexec.Excerpt(hdr.Name, 128), err)
		}
	}
	return tw.Close()
}

// keepRecords reduces hdr's extended-header records to the xattrs, an
// allowlist rather than a strip list: every other record is either one the
// typed fields carry (path, size, the times, which the writer regenerates
// from them) or one no image load reads, and a record left in would carry
// the engine's choice, or a time under another name, through to the bytes.
func keepRecords(hdr *tar.Header) {
	kept := map[string]string{}
	for k, v := range hdr.PAXRecords {
		if strings.HasPrefix(k, "SCHILY.xattr.") {
			kept[k] = v
		}
	}
	if hdr.Typeflag == tar.TypeXGlobalHeader {
		// A global header is only its records; keep them, times aside.
		for k, v := range hdr.PAXRecords {
			if k != "mtime" && k != "atime" && k != "ctime" {
				kept[k] = v
			}
		}
	}
	hdr.PAXRecords = kept
}

// sortedIndex reads an index file whole (manifest.json, a list; index.json,
// an object whose "manifests" is a list) and returns it with that list's
// entries sorted by their own compact JSON, which no load depends on and
// which the engine writes in any order. A file that is neither shape, or past
// maxIndexBytes, is refused rather than passed through unsorted.
func sortedIndex(r io.Reader, hdr *tar.Header) ([]byte, error) {
	if hdr.Size > maxIndexBytes {
		return nil, fmt.Errorf("the engine's %s is %d bytes, more than an index holds", hdr.Name, hdr.Size)
	}
	raw, err := io.ReadAll(io.LimitReader(r, maxIndexBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading the engine's %s: %w", hdr.Name, err)
	}
	if hdr.Name == "manifest.json" {
		var list []json.RawMessage
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, fmt.Errorf("the engine's manifest.json is not a list: %w", err)
		}
		return json.Marshal(sortedRaw(list))
	}
	var index map[string]json.RawMessage
	if err := json.Unmarshal(raw, &index); err != nil {
		return nil, fmt.Errorf("the engine's index.json is not an object: %w", err)
	}
	if manifests, ok := index["manifests"]; ok {
		var list []json.RawMessage
		if err := json.Unmarshal(manifests, &list); err != nil {
			return nil, fmt.Errorf("the engine's index.json manifests is not a list: %w", err)
		}
		sorted, err := json.Marshal(sortedRaw(list))
		if err != nil {
			return nil, err
		}
		index["manifests"] = sorted
	}
	// encoding/json writes an object's keys sorted, so the object is
	// canonical too.
	return json.Marshal(index)
}

// sortedRaw returns list's entries compacted and sorted by their bytes.
func sortedRaw(list []json.RawMessage) []json.RawMessage {
	out := make([]json.RawMessage, len(list))
	for i, item := range list {
		var b bytes.Buffer
		if err := json.Compact(&b, item); err != nil {
			out[i] = item
			continue
		}
		out[i] = b.Bytes()
	}
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i], out[j]) < 0 })
	return out
}
