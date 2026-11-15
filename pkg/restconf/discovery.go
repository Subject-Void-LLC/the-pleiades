// Package restconf: what a client learns about its server (the RESTCONF
// analogue of a NETCONF session's discovery): its root, its protocol
// capabilities (ietf-restconf-monitoring, RFC 8040 section 9.1), and the YANG
// modules and datastores its YANG library lists, read into pkg/yanglib's
// model, which NETCONF shares. Each protocol's library lists that protocol's
// own modules too (on IOS XE 17.15.1a, NETCONF's ietf-netconf and its kin,
// RESTCONF's tailf-rest-query), which yanglib's DataModules leaves out, so one
// device's data models and their digest read the same over either protocol.
package restconf

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
)

// monitoringNamespace is ietf-restconf-monitoring's namespace.
const monitoringNamespace = "urn:ietf:params:xml:ns:yang:ietf-restconf-monitoring"

// maxCapabilities is the most capability URIs a discovery reads.
const maxCapabilities = 1024

// The resources discovery reads, as paths under {+restconf}/data.
var (
	pathYANGLibrary  = Path{Node("ietf-yang-library", "yang-library")}
	pathModulesState = Path{Node("ietf-yang-library", "modules-state")}
	pathCapabilities = Path{Node("ietf-restconf-monitoring", "restconf-state"), Node("", "capabilities")}
)

// Discovery is what one client learned about its server.
type Discovery struct {
	yanglib.Discovery
	// Root is the API root, as an escaped path.
	Root string
	// RootSource is how Root was learned: RootHostMeta, or RootDefault
	// (host-meta answered 404 or a redirect, and a read under /restconf
	// answered).
	RootSource string
	// CapabilitiesError says why the capability list could not be read,
	// when it could not.
	CapabilitiesError string
}

// Learned returns an error when the discovery learned nothing past its root:
// neither the capabilities nor the YANG library could be read. Onboarding and
// net.restconf.facts both refuse such a read rather than record it, since
// recorded it would read as a device that lost every capability and module.
func (d Discovery) Learned() error {
	if d.CapabilitiesError != "" && d.LibraryError != "" {
		return fmt.Errorf("the device names a RESTCONF root, %q, and answered neither its capabilities (%s) nor its YANG library (%s)", d.Root, d.CapabilitiesError, d.LibraryError)
	}
	return nil
}

// Discover finds the root (when DiscoverRoot has not), then reads the
// server's capabilities and its YANG library: RFC 8525's yang-library, and
// RFC 7895's modules-state from a server that does not serve it. A library or
// capability list that cannot be read is said in the Discovery, never an
// error; the root not being found, a refused credential and a transport
// failure are errors, since nothing was learned.
func (c *Client) Discover(ctx context.Context) (Discovery, error) {
	if !c.rootFound {
		if _, err := c.DiscoverRoot(ctx); err != nil {
			return Discovery{}, err
		}
	}
	d := Discovery{Root: c.root, RootSource: c.rootSource}
	caps, capsErr := c.capabilities(ctx)
	switch {
	case isFatal(capsErr):
		return Discovery{}, capsErr
	case capsErr != nil:
		d.CapabilitiesError = capsErr.Error()
	}
	d.Protocol = yanglib.KeepCapabilities(caps, &d.Discovery)
	libraryStatus, err := c.library(ctx, &d.Discovery)
	if err != nil {
		return Discovery{}, err
	}
	if c.rootSource == RootDefault && statusOf(capsErr) == http.StatusNotFound && libraryStatus == http.StatusNotFound {
		// The conventional root was only a guess, and nothing under it
		// answered: no RESTCONF is served there.
		return Discovery{}, fmt.Errorf("%w (capabilities: %s; YANG library: %s)", ErrNoRestconf, d.CapabilitiesError, d.LibraryError)
	}
	return d, nil
}

// statusOf is the status of the server's refusal err carries, 0 for none.
func statusOf(err error) int {
	var status *StatusError
	if errors.As(err, &status) {
		return status.Status
	}
	return 0
}

// isFatal reports whether err ended discovery rather than one read: anything
// but a server's refusal of one resource or an answer that could not be
// used.
func isFatal(err error) bool {
	var status *StatusError
	return err != nil && !errors.As(err, &status) && !errors.Is(err, errUnusable)
}

// errUnusable marks an answer that arrived but could not be read.
var errUnusable = errors.New("the answer could not be read")

// Capabilities reads the capability URIs ietf-restconf-monitoring lists,
// under the root DiscoverRoot found, as they were sent (not yet held to
// yanglib's rules). It is the authenticated read under the root that says a
// server is answering RESTCONF, not merely HTTPS: a refused credential is a
// RefusedError, another status a *StatusError, and an answer that is not the
// list an unusable one.
func (c *Client) Capabilities(ctx context.Context) ([]string, error) {
	return c.capabilities(ctx)
}

// capabilities reads the capability URIs ietf-restconf-monitoring lists. A
// server answers with the capabilities container itself, as RFC 8040 section
// 3.5 has it (IOS XE, a Tail-f ConfD server, does), or wrapped in its
// restconf-state parent, as rousette (over sysrepo and libyang) does:
// measured 2026-11-14 against the notconf image the NETCONF gates run. Both
// are read; anything else is not the list.
func (c *Client) capabilities(ctx context.Context) ([]string, error) {
	body, mediaType, err := c.GetData(ctx, pathCapabilities)
	if err != nil {
		return nil, err
	}
	var caps []string
	var ok bool
	if mediaType == MediaTypeJSON {
		caps, ok = capabilitiesFromJSON(body)
	} else {
		caps, ok = capabilitiesFromXML(body)
	}
	if !ok {
		return nil, fmt.Errorf("%w: the capabilities answer is not ietf-restconf-monitoring's", errUnusable)
	}
	if len(caps) > maxCapabilities {
		return nil, fmt.Errorf("%w: the server lists %d capabilities, more than the %d read", errUnusable, len(caps), maxCapabilities)
	}
	return caps, nil
}

// capabilityList is the capabilities container in either encoding.
type capabilityList struct {
	Capability []string `xml:"capability" json:"capability"`
}

// capabilitiesFromXML reads the capabilities container, or its
// restconf-state parent holding it, in ietf-restconf-monitoring's namespace.
func capabilitiesFromXML(body []byte) ([]string, bool) {
	var doc struct {
		XMLName xml.Name
		capabilityList
		Capabilities *capabilityList `xml:"capabilities"`
	}
	if err := xml.Unmarshal(body, &doc); err != nil || doc.XMLName.Space != monitoringNamespace {
		return nil, false
	}
	switch {
	case doc.XMLName.Local == "capabilities":
		return doc.Capability, true
	case doc.XMLName.Local == "restconf-state" && doc.Capabilities != nil:
		return doc.Capabilities.Capability, true
	}
	return nil, false
}

// capabilitiesFromJSON reads the JSON encoding of the capabilities container,
// or of its restconf-state parent holding it.
func capabilitiesFromJSON(body []byte) ([]string, bool) {
	var doc struct {
		Capabilities *capabilityList `json:"ietf-restconf-monitoring:capabilities"`
		State        *struct {
			Capabilities *capabilityList `json:"capabilities"`
		} `json:"ietf-restconf-monitoring:restconf-state"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, false
	}
	switch {
	case doc.Capabilities != nil:
		return doc.Capabilities.Capability, true
	case doc.State != nil && doc.State.Capabilities != nil:
		return doc.State.Capabilities.Capability, true
	}
	return nil, false
}

// library completes d from the server's YANG library, returning the status
// of the refusal that left it unread (0 when it was read or not refused): yang-library first,
// then modules-state from a server that answers the first with 404 or 400
// (RFC 8040 section 7: a resource the server does not have). Either answer
// that cannot be used leaves d with no modules, Unknown, and LibraryError
// saying why; an empty library the same, as access control would answer.
func (c *Client) library(ctx context.Context, d *yanglib.Discovery) (int, error) {
	lib, err := c.readLibrary(ctx, pathYANGLibrary)
	if s := statusOf(err); s == http.StatusNotFound || s == http.StatusBadRequest {
		lib, err = c.readLibrary(ctx, pathModulesState)
	}
	if isFatal(err) {
		return 0, err
	}
	d.Completeness = yanglib.Unknown
	if err != nil {
		d.LibraryError = "the YANG library was not read: " + err.Error()
		return statusOf(err), nil
	}
	kept, err := yanglib.Keep(lib, *d)
	if err != nil {
		d.LibraryError = err.Error()
		return 0, nil
	}
	*d = d.WithLibrary(kept)
	d.Datastores = kept.Datastores
	d.ContentID = kept.ContentID
	return 0, nil
}

// readLibrary reads one library resource in whichever encoding the server
// sent.
func (c *Client) readLibrary(ctx context.Context, p Path) (yanglib.Library, error) {
	body, mediaType, err := c.GetData(ctx, p)
	if err != nil {
		return yanglib.Library{}, err
	}
	var lib yanglib.Library
	if mediaType == MediaTypeJSON {
		lib, err = yanglib.ParseJSON(body)
	} else {
		lib, err = yanglib.ParseXML(body)
	}
	if err != nil {
		// The parser's error can quote a name from the answer, so it is
		// bounded and quoted as any far side's words are.
		return yanglib.Library{}, fmt.Errorf("%w: %s", errUnusable, farside.Text(strings.TrimPrefix(err.Error(), "reading the YANG library: ")))
	}
	return lib, nil
}
