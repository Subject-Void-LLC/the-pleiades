package filters

// ValidateVLAN reports whether vlan is a valid IEEE 802.1Q VLAN ID: 1
// through 4094. 0 and 4095 are reserved by the standard itself (0 means
// "no VLAN tag" in the priority-tagged frame case; 4095 is reserved for
// implementation use) and are rejected here, not merely discouraged.
func ValidateVLAN(vlan int) bool {
	return vlan >= 1 && vlan <= 4094
}

// IsCiscoReservedVLAN reports whether vlan is one Cisco reserves by
// default on its own platforms: 1 (the default VLAN, never deletable)
// and 1002 through 1005 (the legacy FDDI/Token Ring VLANs every Catalyst
// switch still carries). This is a separate, non-mandatory predicate
// from ValidateVLAN deliberately: the reservation is a Cisco default,
// not an IEEE rule, so folding it into ValidateVLAN's hard-error path
// would reject configurations that are entirely valid on non-Cisco gear
// or on a Cisco switch with those VLANs' default use reassigned.
func IsCiscoReservedVLAN(vlan int) bool {
	return vlan == 1 || (vlan >= 1002 && vlan <= 1005)
}

// ValidateASN reports whether asn is a valid Autonomous System Number,
// spanning both the 16-bit range (RFC 1930) and the 32-bit range
// (RFC 6793) this filter does not distinguish between: 1 through
// 4294967294. 0 (RFC 7607, reserved) and 65535 and 4294967295 (both RFC
// 7300, reserved for documentation/private use signaling) are rejected.
func ValidateASN(asn int) bool {
	if asn == 0 || asn == 65535 || asn == 4294967295 {
		return false
	}
	return asn >= 1 && asn <= 4294967294
}

// IsPrivateASN reports whether asn falls in a range reserved for
// private use: the 16-bit private range (RFC 6996, 64512 through 65534)
// or the 32-bit private range (RFC 6996, 4200000000 through
// 4294967294). It does not itself require asn to be a valid ASN by
// ValidateASN's own rule; a caller wanting both checks calls both.
func IsPrivateASN(asn int) bool {
	if asn >= 64512 && asn <= 65534 {
		return true
	}
	return asn >= 4200000000 && asn <= 4294967294
}
