// Package yangfacts holds the yang.* fact family: what every YANG discovery
// records about a device's schema, whichever protocol read it (owner decision
// 2026-11-14: yang.* is the schema's family, shared by every protocol). The
// module set belongs to the device's management plane rather than to NETCONF
// or RESTCONF, which on one device serve the same YANG library, so
// net.netconf.* and net.restconf.* record the same keys, built here once; each
// protocol's own findings go under its own family (netconf., restconf.). A
// method dispatching across protocols (net.yang.facts) would record these
// same keys too. It is in pkg/ because a Collection package imports only
// pkg/.
package yangfacts

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/yanglib"
)

// The yang.* facts.
const (
	FactModules      = "yang.modules"
	FactModuleCount  = "yang.module_count"
	FactModuleDigest = "yang.modules_digest"
	FactCompleteness = "yang.completeness"
	FactContentID    = "yang.content_id"
	FactDigestRule   = "yang.digest_rule"
)

// FactKeys are the yang.* facts in the order a discovery records them.
var FactKeys = []string{FactModules, FactModuleCount, FactModuleDigest, FactDigestRule, FactCompleteness, FactContentID}

// DigestRuleDescription documents yang.digest_rule, for every method
// recording it and its catalog entry alike.
const DigestRuleDescription = "The rule yang.modules_digest was computed under: which modules it leaves out as the protocols' own, as protocol-modules- and 12 hex digits that change whenever that list changes. Compare two digests only when their rules are equal: a release that adds a module to the protocols' list changes every device's digest once, the devices unchanged."

// MaxListFactBytes is the most a list fact may take as JSON and still be
// recorded, under the fact store's 64 KiB value bound with room for its own
// encoding. The module list is each module's name@revision, which is what a
// fleet query asks of it (devices implementing Cisco-IOS-XE-native); the full
// entries, with namespaces, features and deviations, are what
// yang.modules_digest covers. A list past the bound is left out and the count
// and digest, always present, still are, so the fact never changes type.
// Measured on a Catalyst 8000V (IOS XE 17.15.1a): 559 modules as full entries
// passed 60 KiB, as name@revision they are about 20 KiB.
const MaxListFactBytes = 60 << 10

// Source says, for a protocol, where its discovery reads what the yang.*
// facts hold, for their documentation.
type Source struct {
	// Modules completes "read ..." in yang.modules' description.
	Modules string
	// Completeness is yang.completeness' description: what makes the list
	// complete, partial or unknown over this protocol.
	Completeness string
	// ContentID is yang.content_id's Returned condition.
	ContentID string
	// ContentIDFrom says where the content-id is read from.
	ContentIDFrom string
	// Fallback names, for a warning, the list a protocol keeps when its
	// library is not read, with a trailing ", " ("the hello's, " for
	// NETCONF), or is empty for a protocol that keeps none.
	Fallback string
}

// RestconfSource is where a RESTCONF read finds what the yang.* facts hold:
// the library itself, there being no hello. It lives here beside the
// family's documentation so net.restconf.facts and its catalog entry share
// it word for word.
var RestconfSource = Source{
	Modules:       "from the device's YANG library over RESTCONF (RFC 8525's yang-library, or RFC 7895's modules-state from a device that does not serve it)",
	Completeness:  "How far yang.modules is every module the device implements: complete (the YANG library read whole) or partial (a library that dropped a malformed or oversized module entry; a refused capability does not count). A library that could not be read, or came back empty as it does for an account the device's access control hides it from, records no yang.* fact at all, so it never reads as a device that implements no module.",
	ContentID:     "when the device's YANG library names one",
	ContentIDFrom: "(RFC 8525's content-id, or RFC 7895's module-set-id) from the library itself",
}

// Returns documents the yang.* facts; when is the Returned condition of the
// ones always recorded.
func Returns(when string, src Source) []collection.ReturnField {
	return []collection.ReturnField{
		{Name: FactModules, Type: "list of string", Returned: "when the list fits in 60 KiB as JSON", Description: "Every YANG module the device implements, each as name@revision (the name alone for a module with no revision), sorted, read " + src.Modules + ". The protocols' own modules, which each server lists for itself, are left out (they are in netconf.modules or restconf.modules), so the list is the same whichever protocol read it. An entry that is not a YANG identifier, a revision date or a printable namespace is left out whole, never cut short. Left out when the list does not fit; yang.module_count and yang.modules_digest are always recorded."},
		{Name: FactModuleCount, Type: "int", Returned: when, Description: "How many YANG modules the device implements, as yang.modules counts them."},
		{Name: FactModuleDigest, Type: "string", Returned: when, Description: "The SHA-256 (hex) of the device's data models in their canonical form (every module's name, namespace, revision, features and deviations, sorted, as JSON), the protocols' own modules left out as yang.modules leaves them, so one device read over NETCONF and over RESTCONF gives one value, two devices implementing the same models read alike, and any change to the set reads as a change of this one value."},
		{Name: FactDigestRule, Type: "string", Returned: when, Description: DigestRuleDescription},
		{Name: FactCompleteness, Type: "string", Returned: when, Description: src.Completeness},
		{Name: FactContentID, Type: "string", Returned: src.ContentID, Description: "The YANG library's content-id " + src.ContentIDFrom + ", a hint the device may regenerate on restart; yang.modules_digest is the module set's identity."},
	}
}

// Values returns the yang.* facts d gives, from its data models alone
// (yanglib's DataModules: the protocols' own modules are left out, so one
// device's yang.* reads the same over either protocol), and a warning for
// each thing left out of them: a module list past its bound, a library that
// was not read, and entries refused as malformed. src words the library's
// warning.
func Values(d yanglib.Discovery, src Source) (map[string]any, []string, error) {
	data := d.DataModules()
	values := map[string]any{
		FactModuleCount:  len(data),
		FactModuleDigest: d.ModulesDigest(),
		FactDigestRule:   yanglib.DigestRule(),
		FactCompleteness: string(d.Completeness),
	}
	if d.ContentID != "" {
		values[FactContentID] = d.ContentID
	}
	modules, err := ModulesValue(data)
	if err != nil {
		return nil, nil, err
	}
	var warnings []string
	if modules != nil {
		values[FactModules] = modules
	} else if len(data) > 0 {
		warnings = append(warnings, fmt.Sprintf("the device implements %d YANG modules, more than %s holds in 60 KiB; recorded by count and digest only", len(data), FactModules))
	}
	if d.LibraryError != "" {
		warnings = append(warnings, "the device's YANG library was not read ("+d.LibraryError+"); the module list is "+src.Fallback+string(d.Completeness))
	}
	if d.Dropped > 0 {
		warnings = append(warnings, fmt.Sprintf("%d malformed or oversized capability or module entries from the device were left out: %s", d.Dropped, strings.Join(d.DroppedSamples, ", ")))
	}
	return values, warnings, nil
}

// OwnModules is the value of a protocol's own modules fact (netconf.modules,
// restconf.modules): d's modules that are a protocol's own, each
// name@revision, sorted, and always a list; ok is false, and the fact is left
// out whole, when they do not fit in MaxListFactBytes.
func OwnModules(d yanglib.Discovery) (value []string, ok bool, err error) {
	own, err := ModulesValue(d.OwnModules())
	if err != nil || own == nil {
		return nil, false, err
	}
	return own, true, nil
}

// OwnModulesDescription documents a protocol's own modules fact.
func OwnModulesDescription(protocol string) string {
	return "The modules " + protocol + "'s server lists for itself, which yang.modules leaves out (owner decision 2026-11-15: yang.* is protocol-neutral), each as name@revision, sorted, from the measured list of protocol modules: ietf-netconf, ietf-netconf-with-defaults and tailf-netconf-extensions over NETCONF, and tailf-rest-query over RESTCONF, each measured on a Catalyst 8000V running IOS XE 17.15.1a on 2026-11-15 as one protocol's library listed it and the other's did not. Empty when the library lists none of them; left out, with a warning, when the list does not fit in 60 KiB."
}

// ModulesValue is modules as yang.modules' value, each name@revision (name
// alone for a module with no revision), nil when the list does not fit.
func ModulesValue(modules []yanglib.Module) ([]string, error) {
	out := make([]string, 0, len(modules))
	for _, m := range modules {
		if m.Revision == "" {
			out = append(out, m.Name)
		} else {
			out = append(out, m.Name+"@"+m.Revision)
		}
	}
	if !Fits(out) {
		return nil, nil
	}
	return out, nil
}

// Fits reports whether list takes at most MaxListFactBytes as JSON.
func Fits(list []string) bool {
	raw, err := json.Marshal(list)
	return err == nil && len(raw) <= MaxListFactBytes
}

// NonNil is list, or an empty list for nil, so a list fact is always a list.
func NonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

// Record sets each of keys that values holds as a stat and, only when
// verified (the device's identity was proved: a host key, a certificate), as
// a fact: a machine in the middle can strip a capability or invent a module,
// and what it says is not kept. The caller says why in its own warning.
func Record(rc sdk.RunbookContext, keys []string, values map[string]any, verified bool) error {
	for _, key := range keys {
		value, ok := values[key]
		if !ok {
			continue
		}
		if err := rc.SetStat(key, value); err != nil {
			return err
		}
		if !verified {
			continue
		}
		if err := rc.EmitFact(key, value); err != nil {
			return err
		}
	}
	return nil
}
