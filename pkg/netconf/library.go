// Package netconf: reading a server's YANG library (RFC 8525, or RFC 7895's
// modules-state from an older server), the authoritative list of the modules
// it implements, and the run-long cache that spares an unchanged device a
// second read.
package netconf

import (
	"context"
	"encoding/xml"
	"fmt"
	"strings"
	"sync"
)

// The YANG library's namespace, which RFC 8525 and RFC 7895 share.
const yangLibraryNamespace = "urn:ietf:params:xml:ns:yang:ietf-yang-library"

// libraryReply is an rpc-reply carrying RFC 8525's yang-library, read with
// namespace-qualified tags on the elements that carry one, so a reply that
// declares its prefixes on rpc-reply (as Junos does) is read the same as one
// that does not.
type libraryReply struct {
	Data struct {
		Library *struct {
			ModuleSets []struct {
				Modules []libraryModule `xml:"module"`
			} `xml:"module-set"`
			Datastores []struct {
				Name string `xml:"name"`
			} `xml:"datastore"`
			ContentID string `xml:"content-id"`
		} `xml:"urn:ietf:params:xml:ns:yang:ietf-yang-library yang-library"`
		State *struct {
			ModuleSetID string `xml:"module-set-id"`
			Modules     []struct {
				libraryModuleFields
				Conformance string `xml:"conformance-type"`
				Deviations  []struct {
					Name string `xml:"name"`
				} `xml:"deviation"`
			} `xml:"module"`
		} `xml:"urn:ietf:params:xml:ns:yang:ietf-yang-library modules-state"`
	} `xml:"urn:ietf:params:xml:ns:netconf:base:1.0 data"`
}

// libraryModuleFields are the fields RFC 8525's and RFC 7895's module entries
// share.
type libraryModuleFields struct {
	Name      string   `xml:"name"`
	Revision  string   `xml:"revision"`
	Namespace string   `xml:"namespace"`
	Features  []string `xml:"feature"`
}

// libraryModule is one implemented module of an RFC 8525 module set; its
// deviation entries name the deviating modules.
type libraryModule struct {
	libraryModuleFields
	Deviations []string `xml:"deviation"`
}

// readLibrary reads the module set (and, from RFC 8525, the datastores) from
// an rpc-reply to a get of the YANG library. An empty data element is not an
// error; it is reported as no modules.
func readLibrary(raw []byte) (modules []Module, datastores []string, err error) {
	var reply libraryReply
	if err := xml.Unmarshal(raw, &reply); err != nil {
		return nil, nil, fmt.Errorf("reading the YANG library: %w", err)
	}
	if lib := reply.Data.Library; lib != nil {
		for _, set := range lib.ModuleSets {
			for _, m := range set.Modules {
				modules = append(modules, libraryModuleOf(m.libraryModuleFields, m.Deviations))
			}
		}
		for _, ds := range lib.Datastores {
			// An identityref such as "ds:running"; the name is after the
			// prefix.
			name := strings.TrimSpace(ds.Name)
			if i := strings.LastIndexByte(name, ':'); i >= 0 {
				name = name[i+1:]
			}
			datastores = append(datastores, name)
		}
		return modules, datastores, nil
	}
	if state := reply.Data.State; state != nil {
		for _, m := range state.Modules {
			if strings.TrimSpace(m.Conformance) == "import" {
				continue
			}
			var deviations []string
			for _, d := range m.Deviations {
				deviations = append(deviations, d.Name)
			}
			modules = append(modules, libraryModuleOf(m.libraryModuleFields, deviations))
		}
	}
	return modules, nil, nil
}

// libraryModuleOf is a library entry as a Module, every field trimmed.
func libraryModuleOf(f libraryModuleFields, deviations []string) Module {
	return Module{
		Name:       strings.TrimSpace(f.Name),
		Namespace:  strings.TrimSpace(f.Namespace),
		Revision:   strings.TrimSpace(f.Revision),
		Features:   f.Features,
		Deviations: deviations,
	}
}

// libraryCache holds a module set read from a server's YANG library, keyed by
// the caller's identity for the device and the content-id the hello carried,
// so a run's later sessions to an unchanged device read the hello alone. A
// session whose caller gave no identity (one whose host key was not
// verified) neither reads nor fills it, and a server naming no content-id is
// never cached.
var libraryCache sync.Map // map[string]cachedLibrary

// cachedLibrary is one cached read of a YANG library.
type cachedLibrary struct {
	modules        []Module
	datastores     []string
	source         string
	dropped        int
	droppedSamples []string
}

// discoverLibrary completes d from the server's YANG library when the hello
// advertises one. A library that cannot be read leaves d as the hello gave
// it, Partial, with LibraryError saying why; it is never a failure of the
// session, unless the read itself broke the session (a context deadline or a
// lost stream), which is returned.
func (s *Session) discoverLibrary(ctx context.Context, d Discovery, cacheKey string) (Discovery, error) {
	var filter, source string
	switch {
	case s.HasCapability(CapabilityYANGLibrary11):
		filter, source = `<yang-library xmlns="`+yangLibraryNamespace+`"/>`, "yang-library:1.1"
	case s.HasCapability(CapabilityYANGLibrary10):
		filter, source = `<modules-state xmlns="`+yangLibraryNamespace+`"/>`, "yang-library:1.0"
	default:
		return d, nil
	}
	key := ""
	if cacheKey != "" && d.ContentID != "" {
		key = cacheKey + "\x00" + d.ContentID
		if hit, ok := libraryCache.Load(key); ok {
			return withLibrary(d, hit.(cachedLibrary)), nil
		}
	}
	raw, err := s.get(ctx, filter)
	if err != nil {
		if _, rejected := err.(RPCErrors); rejected {
			d.LibraryError = "the server refused the YANG library read: " + err.Error()
			return d, nil
		}
		return d, err
	}
	modules, datastores, err := readLibrary(raw)
	if err != nil {
		d.LibraryError = err.Error()
		return d, nil
	}
	// The library replaces the hello's module list, so what it drops is
	// counted afresh, beside what the hello's protocol entries dropped.
	counted := Discovery{Dropped: d.Dropped, DroppedSamples: d.DroppedSamples}
	kept := keepModules(modules, &counted)
	if len(kept) == 0 {
		d.LibraryError = "the YANG library came back empty, as it does for an account the server's access control hides it from"
		return d, nil
	}
	lib := cachedLibrary{modules: kept, datastores: datastores, source: source, dropped: counted.Dropped, droppedSamples: counted.DroppedSamples}
	if key != "" {
		libraryCache.Store(key, lib)
	}
	return withLibrary(d, lib), nil
}

// withLibrary is d completed from a read of the YANG library.
func withLibrary(d Discovery, lib cachedLibrary) Discovery {
	d.Modules, d.Source, d.Dropped, d.DroppedSamples = lib.modules, lib.source, lib.dropped, lib.droppedSamples
	d.Completeness = Complete
	d.Datastores = datastoresFrom(d.Protocol, lib.datastores)
	return d
}
