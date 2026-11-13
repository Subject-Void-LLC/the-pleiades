// The data capabilities granted to a device, and the one property they are
// kept in.
package inventory

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
)

// GrantedProperty holds the capabilities an external program defines as
// data (capability.DataDefinition) that a device has been granted, and how
// each was granted. Like DiscoveredProperty, no person and no sync plugin
// writes it as a property (IsReservedProperty): a grant is written by the
// program's probe during onboarding, or by an operator's explicit
// assertion through its own command and scope, and either way it records
// where it came from.
const GrantedProperty = "granted"

// GrantSource says how a grant was made.
type GrantSource string

const (
	// GrantProbed is a grant the defining program's probe made during
	// onboarding: the device answered its probe.
	GrantProbed GrantSource = "probe"

	// GrantAsserted is a grant an operator made by name, with a reason,
	// that nothing on the device vouched for.
	GrantAsserted GrantSource = "assertion"
)

// Grant is one data capability a device holds.
type Grant struct {
	// Capability is the data capability's name.
	Capability capability.Name

	// Source is how it was granted.
	Source GrantSource

	// Program is the program whose probe made a probed grant.
	Program string

	// Properties are the grant's own, held to the capability's definition.
	Properties map[string]any

	// By and Reason are who asserted an asserted grant, and why.
	By     string
	Reason string

	// At is when it was granted (ValidateGrantTime). It is the zero time
	// only on a grant whose recorded time could not be read, which then
	// says why in Unreadable.
	At time.Time

	// Unreadable is why a recorded grant could not be read whole: its time
	// is missing, is not text, is not an RFC 3339 time, or falls outside
	// the years a grant may hold. GrantsFrom sets it rather than inventing a
	// time, and such a grant grants nothing (record.NewBase), saying this.
	// No writer sets it.
	Unreadable string

	// Binding is, for a probed grant on a device type that binds what its
	// probes trust (record.DiscoveryBinding), the digest of those
	// properties when the probe ran; a grant whose binding no longer
	// matches grants nothing, as a stale discovery does.
	Binding string

	// Definition is the digest of the capability's definition the grant was
	// made against (capability.DefinitionDigest). A grant counts only while
	// the definition this process holds digests the same, so a definition
	// that changed meaning (or whose author bumped its revision) withdraws
	// every grant made before it. Probed and asserted grants carry it alike;
	// one written before it existed carries none (a legacy grant).
	Definition string

	// Build is, for a probed grant, the SHA-256 of the program build whose
	// probe made it ("sha256:<hex>"): where the grant came from, kept so an
	// admin can revoke every grant a build later found to be bad probed. It
	// never decides whether a grant counts, since one source builds to a
	// different digest on every platform.
	Build string
}

// buildDigest is the shape of a program build's digest, as the loader
// writes it: its SHA-256 as "sha256:" and lower case hex.
var buildDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// IsBuildDigest reports whether s has the shape of a program build's
// digest, the shape Grant.Build holds.
func IsBuildDigest(s string) bool { return buildDigest.MatchString(s) }

// GrantNote is what became of one recorded grant when its device was
// built: a grant that grants nothing (Lapsed), or one that counts with a
// warning, and why, in words that name the device, the capability and what
// to do.
type GrantNote struct {
	Capability capability.Name
	Lapsed     bool
	Reason     string
}

// GrantNoter is a device that can say what became of each grant it
// records. A capability check that finds a device without a capability it
// was granted (collection.CheckCapabilities) reads the reason here, so a
// grant that lapsed says why rather than reading as never granted.
type GrantNoter interface {
	GrantNotes() []GrantNote
}

// LapsedGrant returns why device's grant of name grants nothing, or the
// empty string when device records no such grant or cannot say.
func LapsedGrant(device InventoryItem, name capability.Name) string {
	noter, ok := device.(GrantNoter)
	if !ok {
		return ""
	}
	for _, n := range noter.GrantNotes() {
		if n.Capability == name && n.Lapsed {
			return n.Reason
		}
	}
	return ""
}

// GrantsProperty encodes grants as the value GrantedProperty holds, keyed
// by capability, in plain types every inventory store round-trips. A
// grant's time is written in UTC to the nanosecond (RFC 3339 with its
// fraction, which a whole second leaves out), so reading it back gives the
// same instant; a time ValidateGrantTime refuses is not written at all, so
// the grant reads back as unreadable rather than as a time it never had.
func GrantsProperty(grants []Grant) map[string]any {
	out := make(map[string]any, len(grants))
	for _, g := range grants {
		props := map[string]any{}
		for k, v := range g.Properties {
			props[k] = v
		}
		entry := map[string]any{
			"source":     string(g.Source),
			"properties": props,
		}
		if ValidateGrantTime(g.At) == nil {
			entry["at"] = g.At.UTC().Format(time.RFC3339Nano)
		}
		if g.Program != "" {
			entry["program"] = g.Program
		}
		if g.By != "" {
			entry["by"] = g.By
		}
		if g.Reason != "" {
			entry["reason"] = g.Reason
		}
		if g.Binding != "" {
			entry["binding"] = g.Binding
		}
		if g.Definition != "" {
			entry["definition"] = g.Definition
		}
		if g.Build != "" {
			entry["build"] = g.Build
		}
		out[string(g.Capability)] = entry
	}
	return out
}

// GrantsFrom decodes props' GrantedProperty, sorted by capability. It
// reports false when the property is absent, and an error when it is
// present but malformed (a name that is not a data capability's, a source
// it does not know), so a device refuses to hydrate from a corrupted
// record rather than read it as "granted nothing".
func GrantsFrom(props Properties) ([]Grant, bool, error) {
	raw, present := props.Raw()[GrantedProperty]
	if !present {
		return nil, false, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, true, fmt.Errorf("property %s is not a mapping", GrantedProperty)
	}
	grants := make([]Grant, 0, len(m))
	for name, v := range m {
		if !capability.IsDataName(capability.Name(name)) {
			return nil, true, fmt.Errorf("property %s grants %q, which is not a data capability's name", GrantedProperty, name)
		}
		entry, ok := v.(map[string]any)
		if !ok {
			return nil, true, fmt.Errorf("property %s's grant of %s is not a mapping", GrantedProperty, name)
		}
		g := Grant{Capability: capability.Name(name), Properties: map[string]any{}}
		source, _ := entry["source"].(string)
		switch GrantSource(source) {
		case GrantProbed, GrantAsserted:
			g.Source = GrantSource(source)
		default:
			return nil, true, fmt.Errorf("property %s's grant of %s has the source %q, which is neither a probe nor an assertion", GrantedProperty, name, source)
		}
		if p, ok := entry["properties"].(map[string]any); ok {
			g.Properties = p
		}
		g.Program, _ = entry["program"].(string)
		g.By, _ = entry["by"].(string)
		g.Reason, _ = entry["reason"].(string)
		g.Binding, _ = entry["binding"].(string)
		// Kept as recorded, whatever their shape: a digest that is not one
		// matches no definition, so the grant lapses and says why, rather
		// than the device refusing to load.
		g.Definition, _ = entry["definition"].(string)
		g.Build, _ = entry["build"].(string)
		g.At, g.Unreadable = grantTimeFrom(entry)
		grants = append(grants, g)
	}
	sort.Slice(grants, func(i, j int) bool { return grants[i].Capability < grants[j].Capability })
	return grants, true, nil
}

// MaxGrantReason bounds an asserted grant's reason.
const MaxGrantReason = 512

// ValidateGrantReason refuses an asserted grant's reason that is empty, too
// long, or holds a control character. An assertion is a claim nothing on
// the device vouched for, so it always says why.
func ValidateGrantReason(reason string) error {
	if strings.TrimSpace(reason) == "" {
		return errors.New("a reason is required: a granted capability is an assertion nothing on the device vouched for, so say why it holds")
	}
	if len(reason) > MaxGrantReason {
		return fmt.Errorf("the reason is longer than %d bytes", MaxGrantReason)
	}
	for _, r := range reason {
		if unicode.IsControl(r) {
			return errors.New("the reason holds a control character")
		}
	}
	return nil
}
