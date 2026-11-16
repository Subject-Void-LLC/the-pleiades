// Package credential: a short SNMP community, which no run can mask by value,
// and the warning every run that uses one gives.
//
// Decided 2026-11-15: a community under wire.MinSNMPCommunityLength is
// refused unless the operator allows it (add-credential
// --allow-short-community, or a pleiades_snmp credential's
// allow_short_community input); allowed, it is stored with a mark, left out
// of every run's value-masking set (masking "public" by value would turn
// "republic" into "re********" in every output), and every run that uses it
// says that it is masked by shape only.
package credential

import (
	"context"
	"fmt"
	"sync"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/wire"
)

// ShortCommunity reports whether a flattened credential carries an SNMP
// community too short to mask by value. wire.MaskedValues, the one masking
// rule, leaves such a community out of the masking set, and the warning below is
// what a run says about it.
func ShortCommunity(secrets map[string]string) bool {
	return wire.SNMPOf(secrets).ShortCommunity()
}

// ShortCommunityWarning is what a run says about device, whose credential
// carries a short community: where it is still masked, and where it is not.
func ShortCommunityWarning(device string) string {
	return fmt.Sprintf("device %q's SNMP community is shorter than %d characters and was stored allowed short, so this run masks it "+
		"only by its shape (a configuration line that holds it, such as snmp-server community or rocommunity), never by value: "+
		"output that shows it any other way shows it", device, wire.MinSNMPCommunityLength)
}

// noticeShortCommunity is a Store that announces each device whose
// credential carries a short community, once.
type noticeShortCommunity struct {
	inner  Store
	notice func(name string)

	mu        sync.Mutex
	announced map[string]bool
}

// NoticeShortCommunity wraps inner, the store a run reads its credentials
// through, so the first lookup of each device whose credential carries a
// short community calls notice with its name. A run builds one wrapper, so
// every run that uses such a credential says so once, and the next run says
// so again: unlike the unbound mark, this is never cleared, since the
// community stays unmaskable by value for as long as it is stored. Like
// NoticeUnbound it answers the named-secret, registry and database lookups
// inner answers.
func NoticeShortCommunity(inner Store, notice func(name string)) Store {
	return keepStores(inner, &noticeShortCommunity{inner: inner, notice: notice, announced: map[string]bool{}})
}

// Lookup implements Store.
func (n *noticeShortCommunity) Lookup(ctx context.Context, deviceName string) (Credential, error) {
	cred, err := n.inner.Lookup(ctx, deviceName)
	if err != nil || !cred.SNMP().ShortCommunity() {
		return cred, err
	}
	n.mu.Lock()
	first := !n.announced[deviceName]
	n.announced[deviceName] = true
	n.mu.Unlock()
	if first {
		n.notice(deviceName)
	}
	return cred, nil
}

// keepStores returns lookup, a wrapper of inner's Lookup, answering the
// named-secret, registry and database lookups inner answers, by passing each
// straight to inner. Callers find those by asserting on the store they were
// handed, so a wrapper that hid them made every secret reference in a run
// fail as unresolvable.
func keepStores(inner, lookup Store) Store {
	if full, ok := inner.(interface {
		SecretStore
		RegistryStore
		DatabaseStore
	}); ok {
		return struct {
			Store
			SecretStore
			RegistryStore
			DatabaseStore
		}{lookup, full, full, full}
	}
	return lookup
}
