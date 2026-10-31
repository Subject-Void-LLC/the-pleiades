// net.linux.address: an address on an interface.
package linux

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// addresses returns the addresses on dev as ip -o addr prints them: each
// "inet" or "inet6" word is followed by the address and its prefix length.
func (s *session) addresses(ctx context.Context, dev string) ([]netip.Prefix, error) {
	out, err := s.ip(ctx, "-o", "addr", "show", "dev", dev)
	if err != nil {
		return nil, err
	}
	var found []netip.Prefix
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		for i, f := range fields {
			if (f == "inet" || f == "inet6") && i+1 < len(fields) {
				if p, err := netip.ParsePrefix(fields[i+1]); err == nil {
					found = append(found, p)
				}
			}
		}
	}
	return found, nil
}

// addressRun makes an address present on an interface, or absent.
func addressRun(ctx context.Context, rc sdk.RunbookContext, s *session, p map[string]any, check bool) (bool, error) {
	dev, err := iface(p, "interface")
	if err != nil {
		return false, fmt.Errorf("%s: %w", s.fqcn, err)
	}
	want, err := prefix(p, "address")
	if err != nil {
		return false, fmt.Errorf("%s: %w", s.fqcn, err)
	}
	present, err := wantPresent(p)
	if err != nil {
		return false, fmt.Errorf("%s: %w", s.fqcn, err)
	}
	have, err := s.addresses(ctx, dev)
	if err != nil {
		return false, err
	}
	has := false
	for _, a := range have {
		has = has || a == want
	}
	record := func(now []netip.Prefix, isThere bool) error {
		list := make([]string, 0, len(now))
		for _, a := range now {
			list = append(list, a.String())
		}
		if err := rc.SetStat("addresses", list); err != nil {
			return err
		}
		return rc.SetStat("present", isThere)
	}
	if has == present {
		return false, record(have, has)
	}
	if check {
		return true, record(have, present)
	}
	state, other := "present", "absent"
	verb := "add"
	if !present {
		state, other, verb = "absent", "present", "del"
	}
	_ = state
	if err := sdk.RecordInverse(rc, sdk.Inverse{FQCN: s.fqcn, Params: map[string]any{"interface": dev, "address": want.String(), "state": other}, KeepOnFailure: true,
		Description: fmt.Sprintf("Makes %s %s on %s again.", want, other, dev)}); err != nil {
		return false, err
	}
	if _, err := s.ip(ctx, "addr", verb, want.String(), "dev", dev); err != nil {
		return false, err
	}
	now, err := s.addresses(ctx, dev)
	if err != nil {
		return true, err
	}
	return true, record(now, present)
}
