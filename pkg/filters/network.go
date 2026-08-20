package filters

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
)

// maxSubnetSplitCount bounds how many subnets SubnetSplit will ever
// allocate and return. Without a bound, a runbook condition asking to
// split a /0 into /32s would try to build over four billion strings in
// one CEL function call; a custom cel.Function carries no automatic
// cost tracking proportional to its own output size (Phase 50's own
// Schema/Injection Hardening finding), so this function has to refuse
// on its own rather than relying on defaultCELCostLimit to catch it.
// 65536 (a /16 split into /32s, or a /0 split into /16s) is generous for
// any real network-planning use and small enough to allocate instantly.
const maxSubnetSplitCount = 65536

// CIDRToNetmask converts a CIDR prefix length to its dotted-decimal
// IPv4 netmask (e.g. "10.0.0.0/24" -> "255.255.255.0"). Returns "" for
// a malformed CIDR, an over-length input, or an IPv6 prefix: "dotted
// decimal" has no IPv6 form, and every filter in this file targets IPv4
// networking, matching the VLAN/ASN/interface-name filters alongside it
// (PLAN.md Section 36's own "Network & addressing" category, read in
// the Cisco/Ansible-flavored context this whole category shares).
func CIDRToNetmask(cidr string) string {
	prefix, ok := parseIPv4Prefix(cidr)
	if !ok {
		return ""
	}
	return net.IP(net.CIDRMask(prefix.Bits(), 32)).String()
}

// NetmaskToCIDR converts a dotted-decimal IPv4 netmask to its CIDR
// prefix length (e.g. "255.255.255.0" -> 24). Returns -1 for a
// malformed, non-contiguous (not a run of ones followed by a run of
// zeros), over-length, or IPv6 input; -1 is never a real prefix length
// (0 through 32), so it is an unambiguous invalid signal.
func NetmaskToCIDR(netmask string) int {
	if len(netmask) > MaxInputBytes {
		return -1
	}
	addr, err := netip.ParseAddr(netmask)
	if err != nil || !addr.Is4() {
		return -1
	}
	mask := net.IPMask(addr.AsSlice())
	ones, bits := mask.Size()
	if bits == 0 {
		// net.IPMask.Size's own documented invalid signal: a mask that
		// is not a contiguous run of ones then zeros (0.0.0.0, a
		// legitimately all-zero /0 mask, reports (0, 32), not (0, 0),
		// so this check does not misclassify it).
		return -1
	}
	return ones
}

// WildcardMask converts a CIDR prefix length to its Cisco-style
// wildcard mask, the bitwise complement of the netmask (e.g.
// "10.0.0.0/24" -> "0.0.0.255"). Returns "" on the same conditions as
// CIDRToNetmask.
func WildcardMask(cidr string) string {
	prefix, ok := parseIPv4Prefix(cidr)
	if !ok {
		return ""
	}
	mask := net.CIDRMask(prefix.Bits(), 32)
	wildcard := make(net.IP, 4)
	for i := range mask {
		wildcard[i] = ^mask[i]
	}
	return wildcard.String()
}

// BroadcastAddress computes a CIDR block's IPv4 broadcast address (e.g.
// "10.0.0.0/24" -> "10.0.0.255"). Returns "" on the same conditions as
// CIDRToNetmask.
func BroadcastAddress(cidr string) string {
	prefix, ok := parseIPv4Prefix(cidr)
	if !ok {
		return ""
	}
	base := prefix.Masked().Addr().As4()
	mask := net.CIDRMask(prefix.Bits(), 32)
	bcast := make(net.IP, 4)
	for i := 0; i < 4; i++ {
		bcast[i] = base[i] | ^mask[i]
	}
	return bcast.String()
}

// SubnetSplit splits an IPv4 CIDR block into every subnet of the given,
// longer newPrefix length, in ascending order (e.g. "10.0.0.0/24", 26
// -> ["10.0.0.0/26", "10.0.0.64/26", "10.0.0.128/26", "10.0.0.192/26"]).
// Returns nil for a malformed or IPv6 cidr, a newPrefix that is not
// strictly longer than cidr's own prefix, a newPrefix outside 0 to 32,
// or a split that would produce more than maxSubnetSplitCount subnets.
func SubnetSplit(cidr string, newPrefix int) []string {
	prefix, ok := parseIPv4Prefix(cidr)
	if !ok {
		return nil
	}
	if newPrefix <= prefix.Bits() || newPrefix > 32 {
		return nil
	}
	shift := uint(newPrefix - prefix.Bits())
	if shift >= 32 {
		return nil
	}
	count := 1 << shift
	if count > maxSubnetSplitCount {
		return nil
	}

	step := uint32(1) << uint(32-newPrefix)
	base := ip4ToUint32(prefix.Masked().Addr())
	subnets := make([]string, count)
	for i := 0; i < count; i++ {
		subnets[i] = fmt.Sprintf("%s/%d", uint32ToIP4(base+uint32(i)*step), newPrefix)
	}
	return subnets
}

// Supernet computes the smallest IPv4 CIDR block that contains every
// given CIDR (e.g. ["10.0.0.0/25", "10.0.0.128/25"] -> "10.0.0.0/24").
// Returns "" for an empty list, or if any entry is malformed or IPv6.
func Supernet(cidrs []string) string {
	if len(cidrs) == 0 {
		return ""
	}

	var minIP, maxIP uint32
	for i, c := range cidrs {
		prefix, ok := parseIPv4Prefix(c)
		if !ok {
			return ""
		}
		start := ip4ToUint32(prefix.Masked().Addr())
		end := start | (^uint32(0) >> uint(prefix.Bits()))
		if i == 0 {
			minIP, maxIP = start, end
			continue
		}
		if start < minIP {
			minIP = start
		}
		if end > maxIP {
			maxIP = end
		}
	}

	// The supernet's prefix length is the count of leading bits minIP
	// and maxIP still agree on: every bit position where they diverge
	// must be a host bit in the result, since the block has to contain
	// both.
	diff := minIP ^ maxIP
	prefixLen := 32
	for diff != 0 {
		diff >>= 1
		prefixLen--
	}
	base := minIP &^ (^uint32(0) >> uint(prefixLen))
	return fmt.Sprintf("%s/%d", uint32ToIP4(base), prefixLen)
}

// IPToInt converts a dotted-decimal IPv4 address to its 32-bit unsigned
// integer form (e.g. "0.0.0.1" -> 1), returned as a Go int since CEL has
// no unsigned integer literal syntax. Returns -1 (never a real result,
// which is always 0 to 4294967295) for a malformed, over-length, or
// IPv6 input.
func IPToInt(ip string) int {
	if len(ip) > MaxInputBytes {
		return -1
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil || !addr.Is4() {
		return -1
	}
	return int(ip4ToUint32(addr))
}

// IntToIP converts a 32-bit unsigned integer to its dotted-decimal IPv4
// form (e.g. 1 -> "0.0.0.1"), the inverse of IPToInt. Returns "" for a
// value outside 0 to 4294967295.
func IntToIP(n int) string {
	if n < 0 || n > 0xFFFFFFFF {
		return ""
	}
	return uint32ToIP4(uint32(n)).String()
}

// ToIPv4MappedIPv6 converts a dotted-decimal IPv4 address to its
// IPv4-mapped IPv6 form (e.g. "10.0.0.5" -> "::ffff:10.0.0.5"). Returns
// "" for a malformed, over-length, or already-IPv6 input.
func ToIPv4MappedIPv6(ip string) string {
	if len(ip) > MaxInputBytes {
		return ""
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil || !addr.Is4() {
		return ""
	}
	b := addr.As4()
	mapped := netip.AddrFrom16([16]byte{10: 0xff, 11: 0xff, 12: b[0], 13: b[1], 14: b[2], 15: b[3]})
	return mapped.String()
}

// FromIPv4MappedIPv6 converts an IPv4-mapped IPv6 address back to plain
// dotted-decimal IPv4 (e.g. "::ffff:10.0.0.5" -> "10.0.0.5"), the
// inverse of ToIPv4MappedIPv6. Returns "" for a malformed, over-length,
// or genuinely IPv6 (not IPv4-mapped) input: this function is
// deliberately narrower than a general "IPv6 to IPv4" unmapper, since
// the only inverse ToIPv4MappedIPv6 needs is of its own output.
func FromIPv4MappedIPv6(ip string) string {
	if len(ip) > MaxInputBytes {
		return ""
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil || !addr.Is4In6() {
		return ""
	}
	return addr.Unmap().String()
}

// ClassifyIP classifies an IPv4 or IPv6 address as one of "private",
// "loopback", "link-local", "multicast", or "public" (the default,
// covering any real, non-special address). Returns "" for a malformed
// or over-length input. This is the ext.Network gap Phase 50's own
// Part XII intro names directly: cel-go's own isGlobalUnicast() follows
// Go's net/netip.Addr.IsGlobalUnicast() semantics, which excludes
// loopback, link-local, and multicast but does not exclude RFC 1918
// private space, so it cannot answer "is this address private" on its
// own.
func ClassifyIP(ip string) string {
	if len(ip) > MaxInputBytes {
		return ""
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	switch {
	case addr.IsLoopback():
		return "loopback"
	case addr.IsMulticast():
		return "multicast"
	case addr.IsLinkLocalUnicast():
		return "link-local"
	case addr.IsPrivate():
		return "private"
	default:
		return "public"
	}
}

// parseIPv4Prefix parses s as a CIDR block and reports whether it
// succeeded and is IPv4, factoring the "length cap, parse, require
// IPv4" sequence every IPv4-CIDR-shaped function in this file repeats.
func parseIPv4Prefix(s string) (netip.Prefix, bool) {
	if len(s) > MaxInputBytes {
		return netip.Prefix{}, false
	}
	prefix, err := netip.ParsePrefix(s)
	if err != nil || !prefix.Addr().Is4() {
		return netip.Prefix{}, false
	}
	return prefix, true
}

// ip4ToUint32 and uint32ToIP4 convert between netip.Addr's IPv4 form and
// the 32-bit unsigned integer representation SubnetSplit and Supernet
// both do their arithmetic in, factored out so IPToInt/IntToIP and the
// two range-arithmetic functions share one conversion rather than four
// slightly different copies.
func ip4ToUint32(a netip.Addr) uint32 {
	b := a.As4()
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func uint32ToIP4(n uint32) netip.Addr {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], n)
	return netip.AddrFrom4(b)
}
