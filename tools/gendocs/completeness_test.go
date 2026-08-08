package main

import (
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/internal/forge/catalogdata"
	"github.com/SubjectVoidLLC/the-pleiades/pkg/collection"
)

// TestImplementedMethodsHaveCompleteDocs is the completeness gate the plan
// calls for: every catalogdata entry whose live Manifest carries
// Status == StatusImplemented must have a real Doc, not just a Summary. A
// declared (not yet implemented) method is exempt by design (see
// collection.Doc's own doc comment); this test only tightens as methods
// flip from declared to implemented, one phase at a time, rather than all
// at once at the end of the catalog.
func TestImplementedMethodsHaveCompleteDocs(t *testing.T) {
	for _, cfg := range catalogdata.Collections {
		fqcn := cfg.Name
		desc, ok := collection.Lookup(fqcn)
		if !ok {
			t.Fatalf("%s: in catalogdata but never registered into pkg/collection", fqcn)
		}
		m := desc.Manifest
		if m.Status != collection.StatusImplemented {
			continue
		}
		t.Run(fqcn, func(t *testing.T) {
			if m.Doc.Summary == "" {
				t.Errorf("%s: implemented but Doc.Summary is empty", fqcn)
			}
			for i, p := range m.Doc.Params {
				if p.Type == "" {
					t.Errorf("%s: param %q (index %d) has no Type", fqcn, p.Name, i)
				}
				if p.Description == "" {
					t.Errorf("%s: param %q (index %d) has no Description", fqcn, p.Name, i)
				}
			}
			if len(m.Doc.Examples) == 0 {
				t.Errorf("%s: implemented but Doc.Examples is empty", fqcn)
			}
			for _, name := range m.Doc.Fragments {
				if _, ok := catalogdata.Fragments[name]; !ok {
					t.Errorf("%s: references fragment %q, not present in catalogdata.Fragments", fqcn, name)
				}
			}
		})
	}
}

// TestFragmentsAreComplete proves every registered Fragment carries fully
// typed and described Params: a Fragment is only ever rendered inline
// under an implemented method's own Parameters table (writeParameters), so
// an incomplete Fragment would silently produce an incomplete-looking row
// with no test catching it.
func TestFragmentsAreComplete(t *testing.T) {
	for name, frag := range catalogdata.Fragments {
		for i, p := range frag.Params {
			if p.Type == "" {
				t.Errorf("fragment %q: param %q (index %d) has no Type", name, p.Name, i)
			}
			if p.Description == "" {
				t.Errorf("fragment %q: param %q (index %d) has no Description", name, p.Name, i)
			}
		}
	}
}
