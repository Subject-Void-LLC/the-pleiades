// This file covers the download declaration: what Register refuses, how a
// filename is built, and which formats a record is actually offered.
package view_test

import (
	"context"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
	"github.com/Subject-Void-LLC/the-pleiades/internal/ui/view"
)

// validDownload is a declaration Register accepts, so each case below can
// break exactly one thing.
func validDownload() view.DownloadSpec {
	return view.DownloadSpec{
		Name:        "report",
		Label:       "Report (CSV)",
		ContentType: "text/csv; charset=utf-8",
		Filename:    "report-{id}.csv",
		Write:       func(context.Context, io.Writer, string) error { return nil },
	}
}

// TestValidateDownloads_RefusesADeclarationNothingCouldServe pins each rule
// at registration, which is where a declaration error belongs: a view whose
// download cannot work must fail at process start rather than when somebody
// presses the link.
func TestValidateDownloads_RefusesADeclarationNothingCouldServe(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*view.DownloadSpec)
		want   string
	}{
		{
			name:   "a name that is not a path token",
			mutate: func(d *view.DownloadSpec) { d.Name = "Report CSV" },
			want:   "must match",
		},
		{
			// A link with no text has no accessible name.
			name:   "no label",
			mutate: func(d *view.DownloadSpec) { d.Label = "  " },
			want:   "has no label",
		},
		{
			// Declared and never sniffed, so an absent one is not a
			// default to fill in: it is a decision nobody made.
			name:   "no content type",
			mutate: func(d *view.DownloadSpec) { d.ContentType = "" },
			want:   "declares no content type",
		},
		{
			// Every record would otherwise save under one name, and three
			// jobs' reports in a folder could not be told apart.
			name:   "a filename with no record in it",
			mutate: func(d *view.DownloadSpec) { d.Filename = "report.csv" },
			want:   "no {id}",
		},
		{
			name:   "nothing to write",
			mutate: func(d *view.DownloadSpec) { d.Write = nil },
			want:   "has no Write function",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dl := validDownload()
			tc.mutate(&dl)
			err := registerWithDownloads(t, []view.DownloadSpec{dl})
			if err == nil {
				t.Fatal("Register accepted it")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Register error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestValidateDownloads_RefusesTwoOfTheSameName(t *testing.T) {
	err := registerWithDownloads(t, []view.DownloadSpec{validDownload(), validDownload()})
	if err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("Register error = %v, want a duplicate-name refusal", err)
	}
}

func TestValidateDownloads_AcceptsAWellFormedOne(t *testing.T) {
	if err := registerWithDownloads(t, []view.DownloadSpec{validDownload()}); err != nil {
		t.Fatalf("Register refused a valid download: %v", err)
	}
}

// registerWithDownloads registers a minimal view carrying downloads, under
// a name unique to this call so the process-wide registry does not collide
// across cases or across -count=3 runs.
//
// Implemented rather than declared, which this used to be. A declared view
// serving real files is now a Register-time refusal, and the helper that
// asserts a well-formed download is ACCEPTED must not be built out of the
// one shape that is refused for another reason entirely.
func registerWithDownloads(t *testing.T, downloads []view.DownloadSpec) error {
	t.Helper()
	return view.Register(view.Descriptor{
		Name:      downloadViewName(t),
		Title:     "Downloadables",
		NavLabel:  "DOWNLOADABLES",
		Status:    view.StatusImplemented,
		IDField:   "name",
		Fields:    testFields(),
		Ops:       view.Ops{List: &apispec.ListJobs, Get: &apispec.GetJob},
		Handlers:  view.MustBind[device](fakeReader{}, nil, testProjector()),
		Downloads: downloads,
	})
}

// TestRegister_RefusesDownloadsOnADeclaredView is the contradiction the
// declared status exists to remove, one affordance further than the chart
// and stream arms beside it.
//
// A declared view's record page renders the honest "not implemented"
// panel. Its /download/{format} route, before this, returned 200 and the
// real bytes: a view telling every reader it is unbuilt while handing out
// files. Refused at Register rather than 404'd in the handler, so the
// contradiction is impossible to compose rather than merely unreachable.
func TestRegister_RefusesDownloadsOnADeclaredView(t *testing.T) {
	t.Cleanup(view.SnapshotForTest())
	err := view.Register(view.Descriptor{
		Name:      downloadViewName(t),
		Title:     "Downloadables",
		NavLabel:  "DOWNLOADABLES",
		Status:    view.StatusDeclared,
		IDField:   "id",
		Fields:    []view.Field{{Name: "id", Label: "ID", Kind: view.KindText, InList: true}},
		Ops:       view.Ops{List: &apispec.ListJobs, Get: &apispec.GetJob},
		Downloads: []view.DownloadSpec{validDownload()},
	})
	if err == nil {
		t.Fatal("Register accepted a declared view that serves downloads, so an unbuilt view hands out real files")
	}
	if !strings.Contains(err.Error(), "declares downloads") {
		t.Errorf("Register refused for the wrong reason: %v", err)
	}
}

// TestDownloadSpec_FilenameForSanitisesTheRecord is a header-injection
// question rather than a display one: the name goes into Content-Disposition.
func TestDownloadSpec_FilenameForSanitisesTheRecord(t *testing.T) {
	dl := validDownload()

	cases := map[string]string{
		"job-1":       "report-job-1.csv",
		"a.b_c-1":     "report-a.b_c-1.csv",
		`ev"il`:       "report-ev-il.csv",
		"with space":  "report-with-space.csv",
		"line\nbreak": "report-line-break.csv",
		// Slashes become separators, so the name can never address a
		// directory: this value reaches a Content-Disposition header.
		"../../etc/passwd": "report-..-..-etc-passwd.csv",
		"":                 "report-record.csv",
		"unicode-éè":       "report-unicode---.csv",
	}
	for id, want := range cases {
		if got := dl.FilenameFor(id); got != want {
			t.Errorf("FilenameFor(%q) = %q, want %q", id, got, want)
		}
	}
}

// TestDownloadSpec_OffersGatesPerRecord is the chooser's rule: a format is
// offered only where it has something to give.
func TestDownloadSpec_OffersGatesPerRecord(t *testing.T) {
	// No Available offers it everywhere, which is right only for a format
	// that cannot be empty.
	always := validDownload()
	if !always.Offers(context.Background(), "anything") {
		t.Error("a download declaring no Available was withheld")
	}

	gated := validDownload()
	gated.Available = func(_ context.Context, id string) bool { return id == "has-one" }
	if !gated.Offers(context.Background(), "has-one") {
		t.Error("a record with something to give was not offered its download")
	}
	if gated.Offers(context.Background(), "has-none") {
		t.Error("a record with nothing to give was offered a download that could only be empty")
	}
}

// TestDetailModel_ResolveDownloadsAddressesAndFiltersThem covers what the
// handler hands the template.
func TestDetailModel_ResolveDownloadsAddressesAndFiltersThem(t *testing.T) {
	present := validDownload()
	absent := validDownload()
	absent.Name = "audit"
	absent.Label = "Audit (JSON)"
	absent.Available = func(context.Context, string) bool { return false }

	m := view.DetailModel{
		Page:       view.PageModel{Prefix: "/ui"},
		Descriptor: view.Descriptor{Name: "gadgets", Downloads: []view.DownloadSpec{present, absent}},
		Row:        view.Row{ID: "job 1"},
	}

	got := m.ResolveDownloads(context.Background())
	if len(got) != 1 {
		t.Fatalf("ResolveDownloads returned %d links, want only the one with content: %+v", len(got), got)
	}
	if got[0].Label != "Report (CSV)" {
		t.Errorf("the link is labelled %q", got[0].Label)
	}
	// The record is escaped into the path, so an id carrying a space or a
	// slash addresses the record it names rather than a different route.
	if got[0].Href != "/ui/gadgets/job%201/download/report" {
		t.Errorf("Href = %q, want the record escaped into the path", got[0].Href)
	}

	// A record with no identity has no address, so nothing is offered.
	m.Row = view.Row{}
	if links := m.ResolveDownloads(context.Background()); len(links) != 0 {
		t.Errorf("a record with no id offered %d downloads", len(links))
	}

	// ...and a view declaring none offers none, which is what keeps the
	// control off every other page in the application.
	m.Row = view.Row{ID: "job-1"}
	m.Descriptor.Downloads = nil
	if links := m.ResolveDownloads(context.Background()); len(links) != 0 {
		t.Errorf("a view declaring no downloads offered %d", len(links))
	}
}

// downloadViewName is a registry key unique to each call, because the view
// registry is process-wide and test-repeat runs this file three times in
// one process.
func downloadViewName(t *testing.T) string {
	t.Helper()
	downloadViewSeq++
	return "dl-" + strings.ToLower(strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return '-'
		}
	}, t.Name())) + "-" + strconv.Itoa(downloadViewSeq)
}

var downloadViewSeq int
