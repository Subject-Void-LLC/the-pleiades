// Tests for Write's contract: what it refuses, what it accepts at each
// limit, that its output is deterministic, and that a failing writer's
// error comes back.
package iso9660_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/iso9660"
)

// TestWriteRefuses drives every documented limit past its edge and checks
// the refusal names the problem and leaves the writer untouched.
func TestWriteRefuses(t *testing.T) {
	oversize := make([]byte, iso9660.MaxFileSize+1)
	full := oversize[:iso9660.MaxFileSize]
	tooMany := make([]iso9660.File, iso9660.MaxFiles+1)
	for i := range tooMany {
		tooMany[i] = iso9660.File{Name: fmt.Sprintf("f%d", i)}
	}
	ok := []iso9660.File{{Name: "user-data", Data: []byte("x")}}
	eastern := time.FixedZone("UTC-5", -5*60*60)

	tests := []struct {
		name     string
		volumeID string
		when     time.Time
		files    []iso9660.File
		want     string
	}{
		{"empty volume id", "", seedTime, ok, "empty volume identifier"},
		{"volume id too long", strings.Repeat("a", iso9660.MaxVolumeIDLength+1), seedTime, ok, "longer than 16"},
		{"volume id with a hyphen", "ci-data", seedTime, ok, "only ASCII letters, digits and '_'"},
		{"volume id with a space", "ci data", seedTime, ok, "only ASCII letters"},
		{"volume id not ASCII", "c\xc3\xafdata", seedTime, ok, "only ASCII letters"},
		{"year before 1900", seedLabel, time.Date(1899, 12, 31, 23, 59, 59, 0, time.UTC), ok, "year 1899"},
		{"year after 2155", seedLabel, time.Date(2156, 1, 1, 0, 0, 0, 0, time.UTC), ok, "year 2156"},
		{"zero time", seedLabel, time.Time{}, ok, "year 1 "},
		{"year after 2155 once in UTC", seedLabel, time.Date(2155, 12, 31, 23, 0, 0, 0, eastern), ok, "year 2156"},
		{"empty name", seedLabel, seedTime, []iso9660.File{{Name: ""}}, "empty file name"},
		{"name too long", seedLabel, seedTime, []iso9660.File{{Name: longName + "x"}}, "longer than 64"},
		{"name with a slash", seedLabel, seedTime, []iso9660.File{{Name: "dir/user-data"}}, "may hold only"},
		{"name with a space", seedLabel, seedTime, []iso9660.File{{Name: "user data"}}, "may hold only"},
		{"name with a semicolon", seedLabel, seedTime, []iso9660.File{{Name: "user-data;1"}}, "may hold only"},
		{"name not ASCII", seedLabel, seedTime, []iso9660.File{{Name: "us\xc3\xa9r-data"}}, "may hold only"},
		{"name ending in a dot", seedLabel, seedTime, []iso9660.File{{Name: "user-data."}}, "ends in '.'"},
		{"dot", seedLabel, seedTime, []iso9660.File{{Name: "."}}, "ends in '.'"},
		{"dot dot", seedLabel, seedTime, []iso9660.File{{Name: ".."}}, "ends in '.'"},
		{"exact duplicate", seedLabel, seedTime, []iso9660.File{{Name: "meta-data"}, {Name: "meta-data"}}, `"meta-data" repeats "meta-data"`},
		{"duplicate ignoring case", seedLabel, seedTime, []iso9660.File{{Name: "meta-data"}, {Name: "META-data"}}, `"META-data" repeats "meta-data"`},
		{"too many files", seedLabel, seedTime, tooMany, "65 files"},
		{"file too large", seedLabel, seedTime, []iso9660.File{{Name: "big", Data: oversize}}, "more than the 16777216"},
		{"total too large", seedLabel, seedTime, []iso9660.File{
			{Name: "a", Data: full}, {Name: "b", Data: full}, {Name: "c", Data: full},
			{Name: "d", Data: full}, {Name: "e", Data: []byte("x")},
		}, "total more than the 67108864"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := iso9660.Write(&buf, tc.volumeID, tc.when, tc.files)
			if err == nil {
				t.Fatal("Write accepted input outside the documented limits")
			}
			if !strings.HasPrefix(err.Error(), "iso9660: ") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want the iso9660 prefix and %q", err, tc.want)
			}
			if buf.Len() != 0 {
				t.Fatalf("a refused Write wrote %d bytes", buf.Len())
			}
		})
	}
}

// TestWriteRefusesNilWriter checks a nil writer is an error, not a panic.
func TestWriteRefusesNilWriter(t *testing.T) {
	if err := iso9660.Write(nil, seedLabel, seedTime, nil); err == nil || err.Error() != "iso9660: nil writer" {
		t.Fatalf("Write(nil) = %v, want the nil writer error", err)
	}
}

// TestWriteAcceptsEachLimit writes an image at the inclusive edge of
// every limit, so an off-by-one in a check shows up as a refusal here.
func TestWriteAcceptsEachLimit(t *testing.T) {
	full := make([]byte, iso9660.MaxFileSize)
	maxFiles := make([]iso9660.File, iso9660.MaxFiles)
	for i := range maxFiles {
		maxFiles[i] = iso9660.File{Name: fmt.Sprintf("f%02d", i), Data: []byte{byte(i)}}
	}
	tests := []struct {
		name     string
		volumeID string
		when     time.Time
		files    []iso9660.File
	}{
		{"one-character volume id", "C", seedTime, nil},
		{"sixteen-character volume id", "ABCDEFGHijklmn_9", seedTime, nil},
		{"year 1900", seedLabel, time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC), nil},
		{"year 2155", seedLabel, time.Date(2155, 12, 31, 23, 59, 59, 990_000_000, time.UTC), nil},
		{"one-character name", seedLabel, seedTime, []iso9660.File{{Name: "a"}}},
		{"name starting with a dot", seedLabel, seedTime, []iso9660.File{{Name: ".hidden"}}},
		{"sixty-four-character name", seedLabel, seedTime, []iso9660.File{{Name: longName}}},
		{"sixty-four files", seedLabel, seedTime, maxFiles},
		{"largest file and largest total", seedLabel, seedTime, []iso9660.File{
			{Name: "a", Data: full}, {Name: "b", Data: full}, {Name: "c", Data: full}, {Name: "d", Data: full},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var n countingWriter
			if err := iso9660.Write(&n, tc.volumeID, tc.when, tc.files); err != nil {
				t.Fatalf("Write refused input inside the limits: %v", err)
			}
			if n%iso9660.SectorSize != 0 {
				t.Fatalf("image is %d bytes, not a whole number of sectors", n)
			}
		})
	}
}

// countingWriter discards what it is given and counts the bytes.
type countingWriter int64

// Write counts p and reports it all written.
func (c *countingWriter) Write(p []byte) (int, error) {
	*c += countingWriter(len(p))
	return len(p), nil
}

// TestWriteIsDeterministic checks that the output depends only on the
// label, the instant, and the names and contents of the files.
func TestWriteIsDeterministic(t *testing.T) {
	files := seedFiles(t)
	first := writeImage(t, seedLabel, files)

	if again := writeImage(t, seedLabel, files); !bytes.Equal(first, again) {
		t.Fatal("two writes of the same input differ")
	}

	reversed := make([]iso9660.File, len(files))
	for i, f := range files {
		reversed[len(files)-1-i] = f
	}
	if got := writeImage(t, seedLabel, reversed); !bytes.Equal(first, got) {
		t.Fatal("listing the same files in another order changed the image")
	}

	var inZone bytes.Buffer
	if err := iso9660.Write(&inZone, seedLabel, seedTime.In(time.FixedZone("UTC+9", 9*60*60)), files); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !bytes.Equal(first, inZone.Bytes()) {
		t.Fatal("the same instant in another time zone changed the image")
	}

	var later bytes.Buffer
	if err := iso9660.Write(&later, seedLabel, seedTime.Add(time.Second), files); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if bytes.Equal(first, later.Bytes()) {
		t.Fatal("a different time produced the same image, so the time is not recorded")
	}
}

// errBoom is the failure failingWriter returns.
var errBoom = errors.New("boom")

// failingWriter accepts ok calls to Write and fails every later one.
type failingWriter struct {
	ok int
}

// Write fails once the allowed number of calls is used up.
func (f *failingWriter) Write(p []byte) (int, error) {
	if f.ok == 0 {
		return 0, errBoom
	}
	f.ok--
	return len(p), nil
}

// TestWriteReturnsWriterErrors fails the writer at each kind of write:
// the metadata, a file's data, and a file's sector padding.
func TestWriteReturnsWriterErrors(t *testing.T) {
	// One file of 3 bytes: the calls are metadata, data, then padding.
	files := []iso9660.File{{Name: "meta-data", Data: []byte("abc")}}
	for ok, want := range []string{"writing the volume metadata", `writing "meta-data"`, `padding "meta-data"`} {
		err := iso9660.Write(&failingWriter{ok: ok}, seedLabel, seedTime, files)
		if !errors.Is(err, errBoom) || !strings.Contains(err.Error(), want) {
			t.Errorf("failing after %d writes: error %v, want %q wrapping errBoom", ok, err, want)
		}
	}
}

// TestWriteDoesNotModifyFiles checks Write leaves the caller's slice and
// data exactly as they were, since it sorts its own copy.
func TestWriteDoesNotModifyFiles(t *testing.T) {
	files := seedFiles(t)
	names := make([]string, len(files))
	data := make([][]byte, len(files))
	for i, f := range files {
		names[i], data[i] = f.Name, bytes.Clone(f.Data)
	}
	if err := iso9660.Write(io.Discard, seedLabel, seedTime, files); err != nil {
		t.Fatalf("Write: %v", err)
	}
	for i, f := range files {
		if f.Name != names[i] || !bytes.Equal(f.Data, data[i]) {
			t.Fatalf("file %d changed: now %q", i, f.Name)
		}
	}
}

// FuzzWrite checks that no name, label or content makes Write panic, and
// that every image it does write is whole sectors, starts its descriptor
// set where the standard says, and is reproducible.
func FuzzWrite(f *testing.F) {
	f.Add(seedLabel, "user-data", "meta-data", []byte("#cloud-config\n"))
	f.Add("X", "a-b", "a_b", []byte{})
	f.Add("vol_1", ".x", "A.B.C", bytes.Repeat([]byte{7}, 3000))
	f.Add("", "..", "x.", []byte(nil))
	f.Fuzz(func(t *testing.T, volumeID, name1, name2 string, data []byte) {
		files := []iso9660.File{{Name: name1, Data: data}, {Name: name2}}
		var first bytes.Buffer
		if err := iso9660.Write(&first, volumeID, seedTime, files); err != nil {
			return
		}
		img := first.Bytes()
		if len(img)%iso9660.SectorSize != 0 || string(sectorAt(img, 16)[1:6]) != "CD001" {
			t.Fatalf("image of %d bytes is not a whole-sector ISO 9660 volume", len(img))
		}
		if size := both32(t, sectorAt(img, 16)[80:88]); int(size)*iso9660.SectorSize != len(img) {
			t.Fatalf("volume space size %d does not match the %d-byte image", size, len(img))
		}
		var second bytes.Buffer
		if err := iso9660.Write(&second, volumeID, seedTime, files); err != nil || !bytes.Equal(img, second.Bytes()) {
			t.Fatalf("a second write of accepted input gave %v or different bytes", err)
		}
	})
}
