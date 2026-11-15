// Package netconf: reading a server's YANG library (RFC 8525, or RFC 7895's
// modules-state from an older server), the authoritative list of the modules
// it implements, and the run-long cache that spares an unchanged device a
// second read.
package netconf

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
)

// libraryReply is an rpc-reply carrying the YANG library, read with
// namespace-qualified tags on the elements that carry one, so a reply that
// declares its prefixes on rpc-reply (as Junos does) is read the same as one
// that does not. The containers are pkg/yanglib's, which RESTCONF reads too.
type libraryReply struct {
	Data struct {
		Library *yanglib.YANGLibrary  `xml:"urn:ietf:params:xml:ns:yang:ietf-yang-library yang-library"`
		State   *yanglib.ModulesState `xml:"urn:ietf:params:xml:ns:yang:ietf-yang-library modules-state"`
	} `xml:"urn:ietf:params:xml:ns:netconf:base:1.0 data"`
}

// readLibrary reads the library (RFC 8525's, or RFC 7895's modules-state)
// from an rpc-reply to a get of it. An empty data element is not an error;
// it is reported as no modules.
func readLibrary(raw []byte) (yanglib.Library, error) {
	var reply libraryReply
	if err := xml.Unmarshal(raw, &reply); err != nil {
		return yanglib.Library{}, fmt.Errorf("reading the YANG library: %w", err)
	}
	switch {
	case reply.Data.Library != nil:
		return reply.Data.Library.Library(), nil
	case reply.Data.State != nil:
		return reply.Data.State.Library(), nil
	}
	return yanglib.Library{}, nil
}

// libraryCache holds a module set read from a server's YANG library, keyed by
// the caller's identity for the device and account (DiscoveryCacheKey: access
// control may show two accounts different libraries) and the content-id the
// hello carried,
// so a run's later sessions to an unchanged device read the hello alone. A
// session whose caller gave no identity (one whose host key was not
// verified) neither reads nor fills it, and a server naming no content-id is
// never cached.
var libraryCache sync.Map // map[string]cachedLibrary

// libraryCacheEntries counts libraryCache's entries, so the cache stays
// bounded in a long-lived process (a Controller onboards in process): past
// maxLibraryCacheEntries it is emptied rather than grown, which costs a
// re-read, never a wrong answer.
var libraryCacheEntries atomic.Int64

// maxLibraryCacheEntries bounds libraryCache.
const maxLibraryCacheEntries = 512

// cachedLibrary is one cached read of a YANG library, after the refusal
// rules.
type cachedLibrary = yanglib.Kept

// discoverLibrary completes d from the server's YANG library when the hello
// advertises one. A library that cannot be read leaves d as the hello gave
// it, Partial, with LibraryError saying why; it is never a failure of the
// session, unless the read itself broke the session (a context deadline or a
// lost stream), which is returned.
func (s *Session) discoverLibrary(ctx context.Context, d Discovery, cacheKey string) (Discovery, error) {
	var filter, source string
	switch {
	case s.HasCapability(CapabilityYANGLibrary11):
		filter, source = `<yang-library xmlns="`+yanglib.Namespace+`"/>`, yanglib.SourceLibrary11
	case s.HasCapability(CapabilityYANGLibrary10):
		filter, source = `<modules-state xmlns="`+yanglib.Namespace+`"/>`, yanglib.SourceLibrary10
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
		var rejected RPCErrors
		switch {
		case errors.As(err, &rejected):
			d.LibraryError = "the server refused the YANG library read: " + err.Error()
			return d, nil
		case errors.Is(err, errUnusableReply):
			d.LibraryError = "the server's answer to the YANG library read was not usable: " + err.Error()
			return d, nil
		}
		return d, err
	}
	read, err := readLibrary(raw)
	if err != nil {
		d.LibraryError = err.Error()
		return d, nil
	}
	// The source is the capability the read was chosen by, as it always
	// was, whichever container the reply carried.
	read.Source = source
	// The library replaces the hello's module list, so what it drops is
	// counted afresh, beside what the hello's protocol entries dropped.
	lib, err := yanglib.Keep(read, Discovery{Dropped: d.Dropped, DroppedSamples: d.DroppedSamples})
	if err != nil {
		d.LibraryError = err.Error()
		return d, nil
	}
	if key != "" {
		if libraryCacheEntries.Add(1) > maxLibraryCacheEntries {
			libraryCache.Range(func(k, _ any) bool { libraryCache.Delete(k); return true })
			libraryCacheEntries.Store(1)
		}
		libraryCache.Store(key, lib)
	}
	return withLibrary(d, lib), nil
}

// withLibrary is d completed from a read of the YANG library, its datastores
// the hello's and the library's together.
func withLibrary(d Discovery, lib cachedLibrary) Discovery {
	d = d.WithLibrary(lib)
	d.Datastores = datastoresFrom(d.Protocol, lib.Datastores)
	return d
}
