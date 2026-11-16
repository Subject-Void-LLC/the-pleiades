// The pin onboarding records on first use (inventory.TLSFirstUseProperty),
// and a person's confirmation of it, which moves it into the pin property.
package record

import (
	"errors"
	"fmt"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
)

// ErrNoFirstUsePin is ConfirmFirstUsePin's refusal of a device that holds no
// pin recorded on first use, or holds a different one.
var ErrNoFirstUsePin = errors.New("no such pin recorded on first use")

// RecordFirstUsePin writes pin as the device's first-use pin, with a
// revision like any other change. With RemoveFirstUsePin and
// ConfirmFirstUsePin it is the only writer of inventory.TLSFirstUseProperty:
// AddInfo and RemoveInfo refuse it, as every inventory write path a person or
// a sync plugin reaches does. Only onboarding calls it, on an explicit
// opt-in, with the certificate pin of what the device presented.
func (b *Base) RecordFirstUsePin(pin string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.revise(inventory.TLSFirstUseProperty, pin)
}

// RemoveFirstUsePin removes the device's first-use pin, with a revision,
// reporting whether there was one. Onboarding removes one the record's own
// trust (a pin or an authority a person set) has made unused.
func (b *Base) RemoveFirstUsePin() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, present := b.props[inventory.TLSFirstUseProperty]; !present {
		return false
	}
	b.revise(inventory.TLSFirstUseProperty, nil)
	return true
}

// ConfirmFirstUsePin is a person's confirmation of the device's first-use
// pin: pin must be the one recorded, written as recorded, and into (the
// device's pin property) must be unset, which it is whenever the first-use
// pin is the one trusted. The pin moves into into, and the first-use pin is
// removed, each with a revision.
//
// What the device trusts does not change: the same one certificate before
// and after. So a discovery or a grant bound to the record as it was
// (inventory.Discovery.Binding) is bound again to the record as it is, since
// the move repoints nothing; one bound to anything else is left as it was,
// stale as it already was.
func (b *Base) ConfirmFirstUsePin(pin, into string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	recorded, _ := b.props[inventory.TLSFirstUseProperty].(string)
	if recorded == "" || recorded != pin {
		return fmt.Errorf("device %s: %s is not its %s (%q): %w", b.name, pin, inventory.TLSFirstUseProperty, recorded, ErrNoFirstUsePin)
	}
	if _, set := b.props[into]; set {
		return fmt.Errorf("device %s already sets %s, so its first-use pin is not the one it trusts: onboard it again to remove the first-use pin, or add the pin to %s yourself once checked", b.name, into, into)
	}
	before := DiscoveryBinding(b.deviceType, inventory.NewProperties(b.props))
	b.revise(into, pin)
	b.revise(inventory.TLSFirstUseProperty, nil)
	props := inventory.NewProperties(b.props)
	after := DiscoveryBinding(b.deviceType, props)
	if before == after {
		return nil
	}
	if d, ok, err := inventory.DiscoveryFrom(props); err == nil && ok && d.Binding == before {
		d.Binding = after
		b.revise(inventory.DiscoveredProperty, d.Property())
	}
	if grants, ok, err := inventory.GrantsFrom(props); err == nil && ok {
		rebound := false
		for i := range grants {
			if grants[i].Binding == before {
				grants[i].Binding, rebound = after, true
			}
		}
		if rebound {
			b.writeGrants(grants)
		}
	}
	return nil
}

// revise sets field to value, or removes it for nil, and records the change
// as a revision. The caller holds b.mu.
func (b *Base) revise(field string, value inventory.PropertyValue) {
	old := b.props[field]
	if value == nil {
		delete(b.props, field)
	} else {
		b.props[field] = value
	}
	b.version++
	b.history = append(b.history, inventory.Revision{
		Version:   b.version,
		ChangedAt: time.Now().UTC(),
		Field:     field,
		OldValue:  old,
		NewValue:  value,
	})
}
