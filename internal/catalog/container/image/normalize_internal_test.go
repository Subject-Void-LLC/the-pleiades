// Tests that an engine's image archive is normalized to the same bytes
// whatever second it was saved in and whatever order the engine listed its
// images in (normalize.go, FAILURE_PATTERNS 843).
package image

import (
	"archive/tar"
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

// entry is one archive entry for a fixture.
type entry struct {
	hdr  tar.Header
	body string
}

// writeArchive writes entries as a tar archive in format.
func writeArchive(t *testing.T, format tar.Format, entries []entry) []byte {
	t.Helper()
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	for _, e := range entries {
		hdr := e.hdr
		if hdr.Typeflag != tar.TypeXGlobalHeader {
			hdr.Size, hdr.Format = int64(len(e.body)), format
		}
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

// engineArchive is an archive shaped as classic Docker 28 saves two images:
// a blobs/sha256 directory stamped with the save's time, two blobs, and
// manifest.json and index.json listing the images first or second.
func engineArchive(t *testing.T, saved time.Time, format tar.Format, swapped bool) []byte {
	t.Helper()
	created := time.Date(2026, 11, 3, 12, 36, 1, 0, time.UTC)
	manifest := `[{"Config":"blobs/sha256/aa","RepoTags":["a:1"],"Layers":["blobs/sha256/la"]},{"Config":"blobs/sha256/bb","RepoTags":["b:1"],"Layers":["blobs/sha256/lb"]}]`
	index := `{"schemaVersion":2,"manifests":[{"digest":"sha256:aa"},{"digest":"sha256:bb"}]}`
	if swapped {
		manifest = `[ {"Config":"blobs/sha256/bb","RepoTags":["b:1"],"Layers":["blobs/sha256/lb"]}, {"Config":"blobs/sha256/aa","RepoTags":["a:1"],"Layers":["blobs/sha256/la"]} ]`
		index = `{"manifests":[{"digest":"sha256:bb"},{"digest":"sha256:aa"}],"schemaVersion":2}`
	}
	return writeArchive(t, format, []entry{
		{tar.Header{Name: "blobs/", Typeflag: tar.TypeDir, Mode: 0o755, ModTime: created}, ""},
		{tar.Header{Name: "blobs/sha256/", Typeflag: tar.TypeDir, Mode: 0o755, ModTime: saved, AccessTime: saved, ChangeTime: saved}, ""},
		{tar.Header{Name: "blobs/sha256/la", Typeflag: tar.TypeReg, Mode: 0o644, ModTime: created}, "a layer's bytes"},
		{tar.Header{Name: "blobs/sha256/lb", Typeflag: tar.TypeReg, Mode: 0o644, ModTime: created}, "another layer"},
		{tar.Header{Name: "index.json", Typeflag: tar.TypeReg, Mode: 0o644, ModTime: time.Unix(0, 0)}, index},
		{tar.Header{Name: "manifest.json", Typeflag: tar.TypeReg, Mode: 0o644, ModTime: time.Unix(0, 0)}, manifest},
	})
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

// TestNormalizedArchive_TheSameImagesGiveTheSameBytes: saves a second apart,
// with the images listed in either order, in every format an engine writes,
// differ as they come from it and are one archive once normalized: every
// entry and blob kept, every time at the epoch, the lists sorted.
func TestNormalizedArchive_TheSameImagesGiveTheSameBytes(t *testing.T) {
	first := time.Date(2026, 11, 3, 12, 36, 33, 0, time.UTC)
	var want []byte
	for _, format := range []tar.Format{tar.FormatUSTAR, tar.FormatPAX, tar.FormatGNU} {
		a := engineArchive(t, first, format, false)
		b := engineArchive(t, first.Add(time.Second+100*time.Millisecond), format, true)
		if bytes.Equal(a, b) {
			t.Fatalf("%v: the two engine archives are already the same, so this proves nothing", format)
		}
		na, nb := normalized(t, a), normalized(t, b)
		if !bytes.Equal(na, nb) {
			t.Errorf("%v: normalized archives differ", format)
		}
		if want == nil {
			want = na
		} else if !bytes.Equal(na, want) {
			t.Errorf("%v: normalizes to other bytes than USTAR does", format)
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
			if !hdr.ModTime.Equal(epoch) || !hdr.AccessTime.IsZero() || !hdr.ChangeTime.IsZero() {
				t.Errorf("%v %s: times %v %v %v, want the epoch and none", format, hdr.Name, hdr.ModTime, hdr.AccessTime, hdr.ChangeTime)
			}
			switch hdr.Name {
			case "blobs/sha256/la":
				if string(body) != "a layer's bytes" {
					t.Errorf("%v: the blob's contents changed: %q", format, body)
				}
			case "manifest.json":
				if !strings.HasPrefix(string(body), `[{"Config":"blobs/sha256/aa"`) {
					t.Errorf("%v: manifest.json not sorted: %s", format, body)
				}
			case "index.json":
				if string(body) != `{"manifests":[{"digest":"sha256:aa"},{"digest":"sha256:bb"}],"schemaVersion":2}` {
					t.Errorf("%v: index.json = %s", format, body)
				}
			}
			names = append(names, hdr.Name)
		}
		if strings.Join(names, ",") != "blobs/,blobs/sha256/,blobs/sha256/la,blobs/sha256/lb,index.json,manifest.json" {
			t.Errorf("%v: entries %v", format, names)
		}
	}
}

// TestNormalizedArchive_KeepsAGlobalHeaderAndXattrs: a PAX global header
// (which archive/tar refuses a time) and an entry's xattrs survive; any
// other extended record does not, so it cannot carry an engine's choice.
func TestNormalizedArchive_KeepsAGlobalHeaderAndXattrs(t *testing.T) {
	raw := writeArchive(t, tar.FormatPAX, []entry{
		{tar.Header{Typeflag: tar.TypeXGlobalHeader, PAXRecords: map[string]string{"comment": "made by an engine"}}, ""},
		{tar.Header{Name: "f", Typeflag: tar.TypeReg, Mode: 0o644, ModTime: time.Now(),
			PAXRecords: map[string]string{"SCHILY.xattr.user.k": "v", "LIBARCHIVE.creationtime": "1762171200"}}, "x"},
	})
	tr := tar.NewReader(bytes.NewReader(normalized(t, raw)))
	global, err := tr.Next()
	if err != nil {
		t.Fatal(err)
	}
	if global.Typeflag != tar.TypeXGlobalHeader || global.PAXRecords["comment"] != "made by an engine" {
		t.Errorf("the global header = %+v, want it kept with its comment", global)
	}
	hdr, err := tr.Next()
	if err != nil {
		t.Fatal(err)
	}
	if hdr.Name != "f" || hdr.PAXRecords["SCHILY.xattr.user.k"] != "v" || hdr.PAXRecords["LIBARCHIVE.creationtime"] != "" {
		t.Errorf("records = %v (name %s), want the xattr kept and the creation time dropped", hdr.PAXRecords, hdr.Name)
	}
}

// TestNormalizedArchive_RefusesWhatItCannotNormalize: bytes that are not a
// tar archive, an index file that is not JSON of its shape, and one past the
// bound fail the read, never pass through; a reader closed early stops the
// copy.
func TestNormalizedArchive_RefusesWhatItCannotNormalize(t *testing.T) {
	read := func(raw []byte) error {
		r := normalizedArchive(bytes.NewReader(raw))
		defer func() { _ = r.Close() }()
		_, err := io.ReadAll(r)
		return err
	}
	if err := read([]byte(strings.Repeat("not a tar archive ", 64))); err == nil || !strings.Contains(err.Error(), "engine's archive") {
		t.Errorf("a broken archive: %v", err)
	}
	for name, body := range map[string]string{
		"manifest.json":                      `{"not":"a list"}`,
		"index.json":                         `["not an object"]`,
		"index.json manifests not a list":    `{"manifests":{"x":1}}`,
		"manifest.json past the index bound": strings.Repeat(" ", maxIndexBytes+1),
	} {
		file, _, _ := strings.Cut(name, " ")
		raw := writeArchive(t, tar.FormatPAX, []entry{{tar.Header{Name: file, Typeflag: tar.TypeReg, Mode: 0o644}, body}})
		if err := read(raw); err == nil {
			t.Errorf("%s: read with no error", name)
		}
	}
	early := normalizedArchive(bytes.NewReader(engineArchive(t, time.Now(), tar.FormatPAX, false)))
	if err := early.Close(); err != nil {
		t.Errorf("closing early: %v", err)
	}
	if _, err := io.ReadAll(early); err == nil {
		t.Error("a closed archive still read")
	}
}
