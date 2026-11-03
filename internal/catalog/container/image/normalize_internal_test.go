// Tests that an engine's image archive is normalized to the same bytes
// whatever second it was saved in (normalize.go, FAILURE_PATTERNS 843).
package image

import (
	"archive/tar"
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

// engineArchive is an archive shaped as classic Docker 28 saves one: a blobs
// directory and a blobs/sha256 directory stamped with the save's time, a
// blob, and the index, written in format.
func engineArchive(t *testing.T, saved time.Time, format tar.Format) []byte {
	t.Helper()
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	created := time.Date(2026, 11, 3, 12, 36, 1, 0, time.UTC)
	entries := []struct {
		hdr  tar.Header
		body string
	}{
		{tar.Header{Name: "blobs/", Typeflag: tar.TypeDir, Mode: 0o755, ModTime: created}, ""},
		{tar.Header{Name: "blobs/sha256/", Typeflag: tar.TypeDir, Mode: 0o755, ModTime: saved, AccessTime: saved, ChangeTime: saved}, ""},
		{tar.Header{Name: "blobs/sha256/72e4b577", Typeflag: tar.TypeReg, Mode: 0o644, ModTime: created}, "a layer's bytes"},
		{tar.Header{Name: "index.json", Typeflag: tar.TypeReg, Mode: 0o644, ModTime: time.Unix(0, 0)}, `{"schemaVersion":2}`},
	}
	for _, e := range entries {
		hdr := e.hdr
		hdr.Size, hdr.Format = int64(len(e.body)), format
		if format == tar.FormatUSTAR {
			// USTAR has no field for these; the other formats carry them.
			hdr.AccessTime, hdr.ChangeTime = time.Time{}, time.Time{}
		}
		if err := tw.WriteHeader(&hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, e.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// normalized is the normalized form of raw.
func normalized(t *testing.T, raw []byte) []byte {
	t.Helper()
	r := normalizedArchive(bytes.NewReader(raw))
	defer func() { _ = r.Close() }()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestNormalizedArchive_TheSameImagesGiveTheSameBytes: two saves a second
// apart, in the formats an engine writes, differ as they come from it and
// are the same once normalized, every entry and its contents kept and every
// time at the epoch.
func TestNormalizedArchive_TheSameImagesGiveTheSameBytes(t *testing.T) {
	first := time.Date(2026, 11, 3, 12, 36, 33, 0, time.UTC)
	for _, format := range []tar.Format{tar.FormatUSTAR, tar.FormatPAX, tar.FormatGNU} {
		a := engineArchive(t, first, format)
		b := engineArchive(t, first.Add(time.Second+100*time.Millisecond), format)
		if bytes.Equal(a, b) {
			t.Fatalf("%v: the two engine archives are already the same, so this proves nothing", format)
		}
		na, nb := normalized(t, a), normalized(t, b)
		if !bytes.Equal(na, nb) {
			t.Errorf("%v: normalized archives differ", format)
		}
		tr := tar.NewReader(bytes.NewReader(na))
		var names []string
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(tr)
			if !hdr.ModTime.Equal(time.Unix(0, 0)) || !hdr.AccessTime.IsZero() && !hdr.AccessTime.Equal(time.Unix(0, 0)) {
				t.Errorf("%v %s: times %v %v, want the epoch", format, hdr.Name, hdr.ModTime, hdr.AccessTime)
			}
			if hdr.Name == "blobs/sha256/72e4b577" && string(body) != "a layer's bytes" {
				t.Errorf("%v: the blob's contents changed: %q", format, body)
			}
			names = append(names, hdr.Name)
		}
		if strings.Join(names, ",") != "blobs/,blobs/sha256/,blobs/sha256/72e4b577,index.json" {
			t.Errorf("%v: entries %v", format, names)
		}
	}
}

// TestNormalizedArchive_ABrokenArchiveFailsTheRead: bytes that are not a tar
// archive fail the reader with an error, never pass through as an archive;
// and a reader closed early stops the copy.
func TestNormalizedArchive_ABrokenArchiveFailsTheRead(t *testing.T) {
	r := normalizedArchive(strings.NewReader(strings.Repeat("not a tar archive ", 64)))
	if _, err := io.ReadAll(r); err == nil || !strings.Contains(err.Error(), "engine's archive") {
		t.Errorf("a broken archive read as %v", err)
	}
	raw := engineArchive(t, time.Now(), tar.FormatPAX)
	early := normalizedArchive(bytes.NewReader(bytes.Repeat(raw, 1)))
	if err := early.Close(); err != nil {
		t.Errorf("closing early: %v", err)
	}
	if _, err := io.ReadAll(early); err == nil {
		t.Error("a closed archive still read")
	}
}
