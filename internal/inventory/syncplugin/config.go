package syncplugin

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// DefaultPageSize is the page size a Config leaves unset resolves to. It is
// large enough that a few hundred devices arrive in one request and small
// enough that a single page never dominates memory, which is the same
// trade-off internal/inventory's ent iterator already makes for its own
// keyset batches.
const DefaultPageSize = 500

// ErrNotConnected is returned by Discover or Sync when Connect has not run
// or did not succeed. Plugins return it rather than a nil-pointer panic so
// a caller that got the call order wrong gets told which call it missed.
var ErrNotConnected = errors.New("sync plugin is not connected")

// Config is the per-source configuration handed to Connect. It carries no
// secret material: CredentialName names an entry in the credential store,
// which the plugin resolves through internal/credential at Connect time.
// Section 6c is explicit that discovery credentials are mapped and looked
// up rather than inlined, and keeping the secret out of this struct means a
// Config can be logged, serialized, or embedded in an error without leaking
// anything.
type Config struct {
	// Name is the plugin's registration key, and the value stamped into
	// every synced device's SourceAuthority. Section 11 allows exactly one
	// authoritative source per item, so this string is what decides
	// ownership on a later re-sync.
	Name string

	// Endpoint locates the upstream system as a URL: https for a REST API,
	// file for a local inventory document. Modeling a file path as a
	// file:// URL rather than adding a Path field is what keeps this struct
	// shared: a field per implementation is how a shared config type stops
	// being shared. It may be empty for a source that needs no locator.
	Endpoint string

	// CredentialName names the credential store entry Connect looks up. It
	// is empty only for a source that genuinely needs no authentication.
	CredentialName string

	// ReadOnly declares the upstream authoritative and never written back.
	//
	// It has two concrete effects, and it is worth being precise about them
	// because "read-only" could plausibly mean several things.
	//
	// First, devices imported from a read-only source land in
	// StateSimulateLocked rather than StateActive, so LifecycleState's own
	// CanExecute gate stops a runbook from acting on a device Pleiades has
	// only read about and never authenticated to directly. An admin
	// promotes them deliberately.
	//
	// Second, and structurally rather than by flag, no write-back path
	// exists to guard: pkg/catalystcenter has no create, update, or delete
	// anywhere in it. That is a stronger guarantee than a runtime check,
	// and it is why this field has no matching "refuse the write" error. If
	// a future plugin does gain a write-back path, this is the field it
	// checks, and the refusal must be a typed error rather than a silent
	// no-op: a flag that quietly discards writes teaches callers the write
	// succeeded.
	//
	// Local inventory has its own separate read-only mode, which is a
	// different question (may this run change our own records) from this
	// one (may we change theirs). See inventory.NewReadOnlyRepository.
	ReadOnly bool

	// InsecureSkipVerify disables TLS certificate verification. It exists
	// because appliances ship with self-signed certificates and refusing to
	// model that would push users to disable verification somewhere less
	// visible. It defaults to false and every caller that sets it is
	// stating so in configuration a reviewer can grep for.
	InsecureSkipVerify bool

	// PageSize is how many records to request per upstream page. Zero means
	// DefaultPageSize; see EffectivePageSize.
	PageSize int
}

// Validate reports whether the Config is usable, checking the constraints
// that hold for every source rather than any one plugin's specifics. A
// plugin needing more (a required region, a required organization ID)
// validates that itself in Connect, because a shared type that grows a
// field per implementation stops being shared.
func (c Config) Validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return errors.New("sync plugin config: name is required")
	}
	if c.PageSize < 0 {
		return fmt.Errorf("sync plugin config %q: page size must not be negative, got %d", c.Name, c.PageSize)
	}
	if c.Endpoint != "" {
		parsed, err := url.Parse(c.Endpoint)
		if err != nil {
			return fmt.Errorf("sync plugin config %q: endpoint is not a valid URL: %w", c.Name, err)
		}
		// Reject a scheme-less endpoint here rather than letting it fail
		// later as a confusing request error. url.Parse accepts a bare
		// "sandboxdnac.cisco.com" as a path with no host, which is exactly
		// the mistake a hand-edited config makes.
		switch parsed.Scheme {
		case "http", "https":
			if parsed.Host == "" {
				return fmt.Errorf("sync plugin config %q: endpoint has no host: %s", c.Name, c.Endpoint)
			}
		case "file":
			if parsed.Path == "" {
				return fmt.Errorf("sync plugin config %q: file endpoint has no path: %s", c.Name, c.Endpoint)
			}
		default:
			return fmt.Errorf("sync plugin config %q: endpoint scheme must be http, https, or file, got %q", c.Name, parsed.Scheme)
		}
	}
	return nil
}

// FilePath returns the local path a file:// Endpoint names, and whether the
// Endpoint was in fact a file URL. A file-backed plugin uses it rather than
// slicing the string itself, so escaping and the empty-host form
// (file:///etc/hosts.yaml) are handled in one place.
func (c Config) FilePath() (string, bool) {
	if c.Endpoint == "" {
		return "", false
	}
	parsed, err := url.Parse(c.Endpoint)
	if err != nil || parsed.Scheme != "file" {
		return "", false
	}
	return parsed.Path, true
}

// EffectivePageSize returns the page size to actually request, resolving an
// unset PageSize to DefaultPageSize. Callers use this rather than reading
// PageSize directly so the default lives in one place.
func (c Config) EffectivePageSize() int {
	if c.PageSize <= 0 {
		return DefaultPageSize
	}
	return c.PageSize
}
