// Package facts: a Windows path that names a stream or is another name for a
// file is refused before anything is read (owner decision 2026-11-19), and a
// Windows answer's streams become the path's file_attached. fact.
package facts

import (
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

func TestWindowsAlias_RefusesStreamsAndOtherNames(t *testing.T) {
	for _, p := range []string{
		`C:\f.txt:secret`, `C:\f.txt::$DATA`, `C:\dir\f.txt.`, `C:\dir \f.txt`, `C:\a\..\b`, `C:\a\.\b`,
		`C:\PROGRA~1\x`, `C:\docs\REPORT~12.DOC`, `C:/a/f.txt:x`,
	} {
		if windowsAlias(p) == "" {
			t.Errorf("%s was not refused", p)
		}
		if err := gatherFilesFor([]string{p}, platformWindows); err == nil || !strings.Contains(err.Error(), "file_attached") {
			t.Errorf("gatherFilesFor(%s) = %v", p, err)
		}
	}
	for _, p := range []string{`C:\Windows\System32\drivers\etc\hosts`, `C:\a~b\c`, `C:\backup~old\x`, `C:\toolongname~1\x`, `C:\v1.2.3\x`, `C:\a\b.config`} {
		if why := windowsAlias(p); why != "" {
			t.Errorf("%s refused: %s", p, why)
		}
	}
}

func TestParseWindowsFiles_StreamsBecomeTheAttachedFact(t *testing.T) {
	digest := strings.Repeat("ab", 32)
	out := `[{"state":"present","type":"file","world_readable":true,"sha256":"` + strings.Repeat("cd", 32) + `","readable":true,` +
		`"_attached_kind":"streams","_attached_state":"listed","_attached_names":["Zone.Identifier","hidden"],"_attached_sha256":"` + digest + `","_attached_unread":1,"_attached_truncated":true},` +
		`{"state":"present","type":"file","world_readable":false,"readable":false,"_attached_kind":"streams","_attached_state":"unlisted"}]`
	facts := filesFacts([]string{`C:\a`, `C:\b`}, parseWindowsFiles(out))
	got := map[string]map[string]any{}
	for _, f := range facts {
		got[f.key], _ = f.value.(map[string]any)
	}
	a := got["file_attached.C:\\a"]
	names, _ := a["names"].([]any)
	if a["kind"] != "streams" || a["state"] != "listed" || len(names) != 2 || a[collection.RawDigestField] != digest || a["unread"] != 1 || a["names_truncated"] != true {
		t.Errorf("C:\\a's attached = %v", a)
	}
	if b := got["file_attached.C:\\b"]; b["state"] != "unlisted" || b[collection.RawDigestField] != nil {
		t.Errorf("C:\\b's attached = %v, want unlisted and no digest", b)
	}
	for k, v := range got {
		for f := range v {
			if strings.HasPrefix(f, "_attached") {
				t.Errorf("%s kept the internal field %s", k, f)
			}
		}
	}
}
