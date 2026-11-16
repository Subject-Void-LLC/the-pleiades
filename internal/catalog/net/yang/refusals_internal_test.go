// Package yang: every refusal net.yang.schema makes, each before the file it
// would have written: before the device is contacted (its parameters, its
// device, its run), and once its library is read (what it lists, the bounds,
// an answer that is not the source asked for).
package yang

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
)

// TestSchema_RefusesBeforeContactingTheDevice holds every refusal its
// parameters, its device and its run make before any source is opened.
func TestSchema_RefusesBeforeContactingTheDevice(t *testing.T) {
	r1 := device("r1", netconfOnly...)
	for name, tc := range map[string]struct {
		dev    inventory.InventoryItem
		noDev  bool
		bare   bool
		params map[string]any
		want   string
	}{
		"a module path":                     {params: map[string]any{paramModules: []any{"../../etc/passwd"}}, want: "does not name a module"},
		"a module with a slash":             {params: map[string]any{paramModules: []any{"a/b@2024-01-01"}}, want: "does not name a module"},
		"a revision word":                   {params: map[string]any{paramModules: []any{"m@latest"}}, want: "does not name a revision"},
		"not a list":                        {params: map[string]any{paramModules: "m"}, want: "must be a list"},
		"an empty list":                     {params: map[string]any{paramModules: []any{}}, want: "empty list"},
		"a number in the list":              {params: map[string]any{paramModules: []any{3}}, want: "not a module name"},
		"a dest above":                      {params: map[string]any{paramDest: "../out"}, want: "inside the project"},
		"a dest climbing back out":          {params: map[string]any{paramDest: "yang/../../out"}, want: "inside the project"},
		"an absolute dest":                  {params: map[string]any{paramDest: "/etc"}, want: "inside the project"},
		"a backslash dest":                  {params: map[string]any{paramDest: `yang\..\..\x`}, want: "inside the project"},
		"a drive dest":                      {params: map[string]any{paramDest: "C:/yang"}, want: "inside the project"},
		"a dest of another type":            {params: map[string]any{paramDest: 3}, want: paramDest},
		"submodules of another type":        {params: map[string]any{paramSubmodules: "yes"}, want: paramSubmodules},
		"a device name for a path":          {dev: device("../r1", netconfOnly...), want: "cannot name a directory"},
		"a device name with a slash":        {dev: device("lab/r1", netconfOnly...), want: "cannot name a directory"},
		"a device name with a NUL":          {dev: device("r1\x00", netconfOnly...), want: "cannot name a directory"},
		"no device":                         {noDev: true, want: "no target device"},
		"too few modules":                   {params: map[string]any{paramMaxModules: 0}, want: "from 1 to 5000"},
		"too many modules":                  {params: map[string]any{paramMaxModules: ceilingMaxModules + 1}, want: "from 1 to 5000"},
		"too large a module":                {params: map[string]any{paramMaxModuleBytes: ceilingMaxModuleBytes + 1}, want: paramMaxModuleBytes},
		"too large a task":                  {params: map[string]any{paramMaxTotalBytes: ceilingMaxTotalBytes + 1}, want: paramMaxTotalBytes},
		"a bound of another type":           {params: map[string]any{paramMaxTotalBytes: "lots"}, want: paramMaxTotalBytes},
		"no project":                        {bare: true, want: sdk.ErrNoProjectFiles.Error()},
		"RESTCONF on a NETCONF-only device": {params: map[string]any{paramVia: viaRESTCONF}, want: "restconf"},
		"NETCONF on a RESTCONF-only device": {dev: device("r2", restconfOnly...), params: map[string]any{paramVia: viaNETCONF}, want: "netconf"},
		"a gNMI-only device":                {dev: device("g1", gnmiOnly...), want: "gNMI defines no way to fetch a module's source"},
	} {
		opened := withSource(t, probeLibrary())
		dev := r1
		if tc.dev != nil {
			dev = tc.dev
		}
		if tc.noDev {
			dev = nil
		}
		ctx := newContext(t)
		var rc sdk.RunbookContext = ctx
		if tc.bare {
			rc = noProject{ctx}
		}
		_, err := Schema(context.Background(), rc, dev, tc.params)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want a refusal naming %q", name, err, tc.want)
		}
		if len(*opened) != 0 {
			t.Errorf("%s: a source was opened (%v) before the refusal", name, *opened)
		}
	}
}

// TestSchema_RefusesWholeOnceTheLibraryIsRead holds every refusal made after
// the library is read and before any file is written: what the library does
// not list or lists twice, a name no file can be built from, two names one
// file system takes for one, each bound, a failed fetch, and an answer that is
// not the source asked for.
func TestSchema_RefusesWholeOnceTheLibraryIsRead(t *testing.T) {
	twoRevisions := probeLibrary()
	twoRevisions.listing.entries = append(twoRevisions.listing.entries, yanglib.Schema{Name: "pleiades-probe", Revision: "2025-01-01"})
	partial := probeLibrary()
	partial.listing.completeness, partial.listing.libraryError = yanglib.Partial, "the server refused the YANG library read"
	unknown := probeLibrary()
	unknown.listing.completeness = yanglib.Unknown
	hostile := probeLibrary()
	hostile.listing.entries = append(hostile.listing.entries, yanglib.Schema{Name: "../../etc/cron.d/x"})
	folded := probeLibrary()
	folded.listing.entries = append(folded.listing.entries, yanglib.Schema{Name: "Pleiades-Probe", Revision: "2026-11-16"})
	empty := probeLibrary()
	empty.listing.entries = nil
	html := probeLibrary()
	html.sources["pleiades-probe@2026-11-16"] = []byte("<html><body>login</body></html>")
	other := probeLibrary()
	other.sources["pleiades-probe@2026-11-16"] = []byte("module pleiades-other { revision 2026-11-16; }")
	broken := probeLibrary()
	broken.fetchErr = errors.New("the session ended")
	unlisted := probeLibrary()
	unlisted.listErr = errors.New("discovery failed")
	for name, tc := range map[string]struct {
		src    *fakeSource
		params map[string]any
		want   string
	}{
		"an unlisted module":          {probeLibrary(), map[string]any{paramModules: []any{"ietf-interfaces"}}, "lists no module ietf-interfaces; net.netconf.facts"},
		"an unlisted revision":        {probeLibrary(), map[string]any{paramModules: []any{"pleiades-probe@2001-01-01"}}, "lists no module pleiades-probe@2001-01-01"},
		"a partial list":              {partial, map[string]any{paramModules: []any{"ietf-interfaces"}}, "its module list is partial: the server refused"},
		"an unknown list":             {unknown, map[string]any{paramModules: []any{"ietf-interfaces"}}, "its module list is unknown"},
		"two revisions":               {twoRevisions, map[string]any{paramModules: []any{"pleiades-probe"}}, "more than one revision (pleiades-probe@2025-01-01, pleiades-probe@2026-11-16)"},
		"a hostile library name":      {hostile, map[string]any{}, "not a YANG identifier"},
		"names one file system folds": {folded, map[string]any{}, "name one file on a file system that ignores case"},
		"an empty library":            {empty, map[string]any{}, "lists no module"},
		"too many sources":            {probeLibrary(), map[string]any{paramMaxModules: 3}, "would fetch 4 sources"},
		"too large a source":          {probeLibrary(), map[string]any{paramModules: []any{"pleiades-probe"}, paramMaxModuleBytes: 64}, "larger than max_module_bytes (64 bytes)"},
		"too many bytes":              {probeLibrary(), map[string]any{paramMaxTotalBytes: 100}, "more than max_total_bytes (100 bytes)"},
		"a login page":                {html, map[string]any{paramModules: []any{"pleiades-probe"}}, "not the YANG source of module pleiades-probe@2026-11-16"},
		"another module":              {other, map[string]any{paramModules: []any{"pleiades-probe"}}, "not the YANG source of module pleiades-probe@2026-11-16"},
		"a failed fetch":              {broken, map[string]any{}, "over netconf: fetching ietf-inet-types@2013-07-15: the session ended"},
		"a failed listing":            {unlisted, map[string]any{}, "over netconf: discovery failed"},
	} {
		withSource(t, tc.src)
		rc := newContext(t)
		_, err := Schema(context.Background(), rc, device("r1", netconfOnly...), tc.params)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want a refusal naming %q", name, err, tc.want)
		}
		if rc.writes != 0 {
			t.Errorf("%s: %d files written before the refusal, want none", name, rc.writes)
		}
		if strings.Contains(name, "too many sources") && len(tc.src.fetched) != 0 {
			t.Errorf("%s: %v fetched before the count was refused", name, tc.src.fetched)
		}
	}
}

// TestSchema_ARouteAndItsOpenings: via names the route on a device serving
// both protocols, the device's own order is NETCONF, and an opening's failure
// is the task's, naming the protocol, with no try over the other.
func TestSchema_ARouteAndItsOpenings(t *testing.T) {
	for via, want := range map[string]string{"": viaNETCONF, "auto": viaNETCONF, viaNETCONF: viaNETCONF, viaRESTCONF: viaRESTCONF} {
		opened := withSource(t, probeLibrary())
		rc := newContext(t)
		params := map[string]any{paramModules: []any{"pleiades-probe"}}
		if via != "" {
			params[paramVia] = via
		}
		if _, err := Schema(context.Background(), rc, device("r1", bothKinds...), params); err != nil {
			t.Fatalf("via %q: %v", via, err)
		}
		if strings.Join(*opened, ",") != want || rc.stats["via"] != want {
			t.Errorf("via %q opened %v and reports %v, want %s alone", via, *opened, rc.stats["via"], want)
		}
	}
	saved := openers
	t.Cleanup(func() { openers = saved })
	tried := 0
	openers = map[string]func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (source, error){
		viaNETCONF: func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (source, error) {
			tried++
			return nil, errors.New("connection refused")
		},
		viaRESTCONF: func(context.Context, sdk.RunbookContext, inventory.InventoryItem, map[string]any) (source, error) {
			t.Error("a failed NETCONF opening was tried again over RESTCONF")
			return nil, errors.New("unreachable")
		},
	}
	if _, err := Schema(context.Background(), newContext(t), device("r1", bothKinds...), map[string]any{}); err == nil || !strings.Contains(err.Error(), "over netconf: connection refused") || tried != 1 {
		t.Errorf("a failed opening: %v after %d tries, want the task's failure naming netconf", err, tried)
	}
}

// TestSchema_RecordsItsSourcesWarningsAndAStatFailure: what the source says
// reaches the task's warnings, and a stat that cannot be recorded fails the
// task.
func TestSchema_RecordsItsSourcesWarningsAndAStatFailure(t *testing.T) {
	src := probeLibrary()
	src.said = []string{"the NETCONF host key was not verified"}
	withSource(t, src)
	rc := newContext(t)
	if _, err := Schema(context.Background(), rc, device("r1", netconfOnly...), map[string]any{paramModules: []any{"pleiades-probe"}}); err != nil {
		t.Fatal(err)
	}
	if warnings, _ := rc.stats[sdk.StatWarnings].([]string); len(warnings) != 1 || warnings[0] != src.said[0] {
		t.Errorf("warnings = %v, want the source's", rc.stats[sdk.StatWarnings])
	}
	for _, stat := range []string{"modules", sdk.StatWarnings} {
		withSource(t, src)
		failing := newContext(t)
		failing.failStat = stat
		if _, err := Schema(context.Background(), failing, device("r1", netconfOnly...), map[string]any{paramModules: []any{"pleiades-probe"}}); err == nil {
			t.Errorf("a failing %s stat: no error", stat)
		}
	}
	// A file the project's writer refuses fails the task, naming it.
	withSource(t, probeLibrary())
	if _, err := Schema(context.Background(), newContext(t), device("r1", netconfOnly...), map[string]any{paramDest: ".pleiades/yang"}); err == nil || !strings.Contains(err.Error(), "writing .pleiades/yang/") {
		t.Errorf("a refused write: %v, want the task's failure naming the file", err)
	}
}

// TestRequest_ReadsItsDefaults is the request a task giving nothing makes.
func TestRequest_ReadsItsDefaults(t *testing.T) {
	r, err := readRequest(map[string]any{}, device("core1", netconfOnly...))
	if err != nil {
		t.Fatal(err)
	}
	if r.dest != "yang/core1" || !r.submodules || r.modules != nil || r.maxModules != defaultMaxModules || r.maxModuleBytes != defaultMaxModuleBytes || r.maxTotalBytes != defaultMaxTotalBytes {
		t.Errorf("readRequest = %+v, want every default", r)
	}
	if r, err := readRequest(map[string]any{paramDest: "yang/./core1/", paramModules: nil}, device("core1")); err != nil || r.dest != "yang/core1" {
		t.Errorf("a dest written loosely = %+v, %v; want it cleaned", r, err)
	}
}
