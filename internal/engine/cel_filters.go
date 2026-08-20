package engine

import (
	"reflect"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

// filtersLibraryName is this library's namespaced identifier, checked by
// cel.Lib against cel.Env.HasLibrary so registering filtersLib() twice on
// the same *cel.Env (a future EnvOption reuse, or a second NewCELEvaluator
// call sharing options) is a no-op rather than a duplicate-function
// collision error.
const filtersLibraryName = "pleiades.filters"

// filtersLib returns the cel.EnvOption that registers every function in
// pkg/filters into a CEL environment, under the flat "filters." prefix
// PLAN.md Section 36 reserves for first-party filters (a different
// namespace from a Collection's <namespace>.<method> FQCN; the two never
// cross-reference each other, so no collision is possible even in
// principle). This is the only file in the repository that imports both
// cel-go and pkg/filters: pkg/filters stays pure Go so it is independently
// unit-testable, and this file is the sole translation to cel-go's ref.Val
// types.
func filtersLib() cel.EnvOption {
	return cel.Lib(filtersLibrary{})
}

// filtersLibrary implements cel.SingletonLibrary. It carries no state:
// every filter function is a pure translation from CEL argument values to
// a pkg/filters call and back, so there is nothing to construct per
// environment.
type filtersLibrary struct{}

func (filtersLibrary) LibraryName() string {
	return filtersLibraryName
}

func (filtersLibrary) CompileOptions() []cel.EnvOption {
	return []cel.EnvOption{
		cel.Function("filters.safeInt",
			cel.FunctionDocs(
				"parse a value as a base-10 integer, returning fallback if it is present but malformed.",
				"The value may be any type; it is converted to a string before parsing, so this is safe to",
				"call on a dyn field whose runtime type is not known ahead of time (a device may report the",
				"same field as an int on one firmware and a string on the next). For a value that may be",
				"absent entirely, use the environment's own optional-chaining syntax instead:",
				"stat.?retries.orValue(0). filters.safeInt does not solve that problem and should not be",
				"reached for as if it did.",
			),
			cel.Overload("filters_safe_int_dyn_int",
				[]*cel.Type{cel.DynType, cel.IntType}, cel.IntType,
				cel.OverloadExamples(
					`filters.safeInt(stat.retries, 0) > 3 // stat.retries == "7" -> 7 > 3 -> true`,
					`filters.safeInt(stat.retries, 0) // stat.retries == "abc" -> 0 (fallback)`,
				),
				cel.BinaryBinding(safeIntBinding),
			),
		),
		cel.Function("filters.safeFloat",
			cel.FunctionDocs(
				"parse a value as a base-10 floating-point number, returning fallback if it is present but",
				"malformed, or if it parses to NaN or an infinity (refused deliberately: a NaN silently",
				"makes every downstream comparison false, the opposite of what a safe cast should do). See",
				"filters.safeInt's own documentation for the missing-versus-malformed distinction; the same",
				"applies here. The fallback argument must be a double literal (0.0, not 0): CEL does not",
				"promote an int literal to double automatically.",
			),
			cel.Overload("filters_safe_float_dyn_double",
				[]*cel.Type{cel.DynType, cel.DoubleType}, cel.DoubleType,
				cel.OverloadExamples(
					`filters.safeFloat(stat.load, 0.0) > 3.5 // stat.load == "4.2" -> 4.2 > 3.5 -> true`,
					`filters.safeFloat(stat.load, 0.0) // stat.load == "NaN" -> 0.0 (fallback)`,
				),
				cel.BinaryBinding(safeFloatBinding),
			),
		),
		cel.Function("filters.safeBool",
			cel.FunctionDocs(
				"parse a value as a boolean, accepting 1/t/true/yes/on and 0/f/false/no/off case",
				"insensitively (a superset of Go's own strconv.ParseBool, matching the truthy spellings",
				"YAML 1.1 and Ansible both already use and that real device CLI output commonly prints).",
				"Any other value, including an unrecognized word or an empty string, returns fallback:",
				"there is no built-in default for an unrecognized spelling beyond what the caller passes.",
			),
			cel.Overload("filters_safe_bool_dyn_bool",
				[]*cel.Type{cel.DynType, cel.BoolType}, cel.BoolType,
				cel.OverloadExamples(
					`filters.safeBool(stat.enabled, false) // stat.enabled == "yes" -> true`,
					`filters.safeBool(stat.enabled, false) // stat.enabled == "maybe" -> false (fallback)`,
				),
				cel.BinaryBinding(safeBoolBinding),
			),
		),
		cel.Function("filters.cidrToNetmask",
			cel.FunctionDocs(
				"converts a CIDR prefix length to its dotted-decimal netmask.",
			),
			cel.Overload("filters_cidr_to_netmask_string_string",
				[]*cel.Type{cel.StringType}, cel.StringType,
				cel.OverloadExamples(
					"filters.cidrToNetmask(\"10.0.0.0/24\") // \"255.255.255.0\"",
				),
				cel.UnaryBinding(cidrToNetmaskBinding),
			),
		),
		cel.Function("filters.netmaskToCIDR",
			cel.FunctionDocs(
				"converts a dotted-decimal netmask to its CIDR prefix length.",
			),
			cel.Overload("filters_netmask_to_cidr_string_int",
				[]*cel.Type{cel.StringType}, cel.IntType,
				cel.OverloadExamples(
					"filters.netmaskToCIDR(\"255.255.255.0\") // 24",
				),
				cel.UnaryBinding(netmaskToCIDRBinding),
			),
		),
		cel.Function("filters.wildcardMask",
			cel.FunctionDocs(
				"converts a CIDR prefix length to its Cisco-style wildcard mask (the bitwise inverse of the netmask).",
			),
			cel.Overload("filters_wildcard_mask_string_string",
				[]*cel.Type{cel.StringType}, cel.StringType,
				cel.OverloadExamples(
					"filters.wildcardMask(\"10.0.0.0/24\") // \"0.0.0.255\"",
				),
				cel.UnaryBinding(wildcardMaskBinding),
			),
		),
		cel.Function("filters.broadcastAddress",
			cel.FunctionDocs(
				"computes a CIDR block's broadcast address.",
			),
			cel.Overload("filters_broadcast_address_string_string",
				[]*cel.Type{cel.StringType}, cel.StringType,
				cel.OverloadExamples(
					"filters.broadcastAddress(\"10.0.0.0/24\") // \"10.0.0.255\"",
				),
				cel.UnaryBinding(broadcastAddressBinding),
			),
		),
		cel.Function("filters.subnetSplit",
			cel.FunctionDocs(
				"splits a CIDR block into subnets of the given new, longer prefix length.",
			),
			cel.Overload("filters_subnet_split_string_int_string",
				[]*cel.Type{cel.StringType, cel.IntType}, cel.ListType(cel.StringType),
				cel.OverloadExamples(
					"filters.subnetSplit(\"10.0.0.0/24\", 26) // [\"10.0.0.0/26\", \"10.0.0.64/26\", \"10.0.0.128/26\", \"10.0.0.192/26\"]",
				),
				cel.BinaryBinding(subnetSplitBinding),
			),
		),
		cel.Function("filters.supernet",
			cel.FunctionDocs(
				"computes the smallest CIDR block that contains every given CIDR.",
			),
			cel.Overload("filters_supernet_string_string",
				[]*cel.Type{cel.ListType(cel.StringType)}, cel.StringType,
				cel.OverloadExamples(
					"filters.supernet([\"10.0.0.0/25\", \"10.0.0.128/25\"]) // \"10.0.0.0/24\"",
				),
				cel.UnaryBinding(supernetBinding),
			),
		),
		cel.Function("filters.ipToInt",
			cel.FunctionDocs(
				"converts a dotted-decimal IPv4 address to its 32-bit unsigned integer form.",
			),
			cel.Overload("filters_ip_to_int_string_int",
				[]*cel.Type{cel.StringType}, cel.IntType,
				cel.OverloadExamples(
					"filters.ipToInt(\"0.0.0.1\") // 1",
				),
				cel.UnaryBinding(ipToIntBinding),
			),
		),
		cel.Function("filters.intToIP",
			cel.FunctionDocs(
				"converts a 32-bit unsigned integer to its dotted-decimal IPv4 form.",
			),
			cel.Overload("filters_int_to_ip_int_string",
				[]*cel.Type{cel.IntType}, cel.StringType,
				cel.OverloadExamples(
					"filters.intToIP(1) // \"0.0.0.1\"",
				),
				cel.UnaryBinding(intToIPBinding),
			),
		),
		cel.Function("filters.toIPv4MappedIPv6",
			cel.FunctionDocs(
				"converts an IPv4 address to its IPv4-mapped IPv6 form (::ffff:a.b.c.d).",
			),
			cel.Overload("filters_to_i_pv4_mapped_i_pv6_string_string",
				[]*cel.Type{cel.StringType}, cel.StringType,
				cel.OverloadExamples(
					"filters.toIPv4MappedIPv6(\"10.0.0.5\") // \"::ffff:10.0.0.5\"",
				),
				cel.UnaryBinding(toIPv4MappedIPv6Binding),
			),
		),
		cel.Function("filters.fromIPv4MappedIPv6",
			cel.FunctionDocs(
				"converts an IPv4-mapped IPv6 address back to plain dotted-decimal IPv4.",
			),
			cel.Overload("filters_from_i_pv4_mapped_i_pv6_string_string",
				[]*cel.Type{cel.StringType}, cel.StringType,
				cel.OverloadExamples(
					"filters.fromIPv4MappedIPv6(\"::ffff:10.0.0.5\") // \"10.0.0.5\"",
				),
				cel.UnaryBinding(fromIPv4MappedIPv6Binding),
			),
		),
		cel.Function("filters.classifyIP",
			cel.FunctionDocs(
				"classifies an IP address as private, public, loopback, link-local, or multicast.",
			),
			cel.Overload("filters_classify_ip_string_string",
				[]*cel.Type{cel.StringType}, cel.StringType,
				cel.OverloadExamples(
					"filters.classifyIP(\"10.0.0.5\") // \"private\"",
					"filters.classifyIP(\"8.8.8.8\") // \"public\"",
				),
				cel.UnaryBinding(classifyIPBinding),
			),
		),
		cel.Function("filters.macToCiscoFormat",
			cel.FunctionDocs(
				"normalizes a MAC address to Cisco dotted-quad notation (aabb.ccdd.eeff).",
			),
			cel.Overload("filters_mac_to_cisco_format_string_string",
				[]*cel.Type{cel.StringType}, cel.StringType,
				cel.OverloadExamples(
					"filters.macToCiscoFormat(\"00:00:5e:00:53:01\") // \"0000.5e00.5301\"",
				),
				cel.UnaryBinding(macToCiscoFormatBinding),
			),
		),
		cel.Function("filters.macToColonFormat",
			cel.FunctionDocs(
				"normalizes a MAC address to colon-separated notation (aa:bb:cc:dd:ee:ff).",
			),
			cel.Overload("filters_mac_to_colon_format_string_string",
				[]*cel.Type{cel.StringType}, cel.StringType,
				cel.OverloadExamples(
					"filters.macToColonFormat(\"0000.5e00.5301\") // \"00:00:5e:00:53:01\"",
				),
				cel.UnaryBinding(macToColonFormatBinding),
			),
		),
		cel.Function("filters.macToWindowsFormat",
			cel.FunctionDocs(
				"normalizes a MAC address to Windows dash-separated notation (AA-BB-CC-DD-EE-FF).",
			),
			cel.Overload("filters_mac_to_windows_format_string_string",
				[]*cel.Type{cel.StringType}, cel.StringType,
				cel.OverloadExamples(
					"filters.macToWindowsFormat(\"00:00:5e:00:53:01\") // \"00-00-5E-00-53-01\"",
				),
				cel.UnaryBinding(macToWindowsFormatBinding),
			),
		),
		cel.Function("filters.macOUI",
			cel.FunctionDocs(
				"extracts a MAC address's OUI (organizationally unique identifier), its first three octets, colon-separated and uppercase.",
			),
			cel.Overload("filters_mac_oui_string_string",
				[]*cel.Type{cel.StringType}, cel.StringType,
				cel.OverloadExamples(
					"filters.macOUI(\"00:00:5e:00:53:01\") // \"00:00:5E\"",
				),
				cel.UnaryBinding(macOUIBinding),
			),
		),
		cel.Function("filters.validateVLAN",
			cel.FunctionDocs(
				"reports whether an integer is a valid IEEE 802.1Q VLAN ID (1 to 4094; 0 and 4095 are reserved and rejected).",
			),
			cel.Overload("filters_validate_vlan_int_bool",
				[]*cel.Type{cel.IntType}, cel.BoolType,
				cel.OverloadExamples(
					"filters.validateVLAN(100) // true",
					"filters.validateVLAN(4095) // false, reserved",
				),
				cel.UnaryBinding(validateVLANBinding),
			),
		),
		cel.Function("filters.isCiscoReservedVLAN",
			cel.FunctionDocs(
				"reports whether a VLAN ID is one Cisco reserves by default (1, 1002 to 1005), a vendor convention, not an IEEE rule.",
			),
			cel.Overload("filters_is_cisco_reserved_vlan_int_bool",
				[]*cel.Type{cel.IntType}, cel.BoolType,
				cel.OverloadExamples(
					"filters.isCiscoReservedVLAN(1002) // true",
					"filters.isCiscoReservedVLAN(100) // false",
				),
				cel.UnaryBinding(isCiscoReservedVLANBinding),
			),
		),
		cel.Function("filters.validateASN",
			cel.FunctionDocs(
				"reports whether an integer is a valid 16-bit or 32-bit Autonomous System Number (1 to 4294967294; 0 and 65535 and 4294967295 are reserved and rejected).",
			),
			cel.Overload("filters_validate_asn_int_bool",
				[]*cel.Type{cel.IntType}, cel.BoolType,
				cel.OverloadExamples(
					"filters.validateASN(64512) // true",
					"filters.validateASN(65535) // false, reserved",
				),
				cel.UnaryBinding(validateASNBinding),
			),
		),
		cel.Function("filters.isPrivateASN",
			cel.FunctionDocs(
				"reports whether an ASN falls in a private-use range (64512 to 65534, or 4200000000 to 4294967294).",
			),
			cel.Overload("filters_is_private_asn_int_bool",
				[]*cel.Type{cel.IntType}, cel.BoolType,
				cel.OverloadExamples(
					"filters.isPrivateASN(64512) // true",
					"filters.isPrivateASN(30000) // false",
				),
				cel.UnaryBinding(isPrivateASNBinding),
			),
		),
		cel.Function("filters.interfaceShortForm",
			cel.FunctionDocs(
				"normalizes a Cisco IOS interface name to its short form (GigabitEthernet0/1 becomes Gi0/1).",
			),
			cel.Overload("filters_interface_short_form_string_string",
				[]*cel.Type{cel.StringType}, cel.StringType,
				cel.OverloadExamples(
					"filters.interfaceShortForm(\"GigabitEthernet0/1\") // \"Gi0/1\"",
				),
				cel.UnaryBinding(interfaceShortFormBinding),
			),
		),
		cel.Function("filters.interfaceLongForm",
			cel.FunctionDocs(
				"normalizes a Cisco IOS interface name to its long form (Gi0/1 becomes GigabitEthernet0/1).",
			),
			cel.Overload("filters_interface_long_form_string_string",
				[]*cel.Type{cel.StringType}, cel.StringType,
				cel.OverloadExamples(
					"filters.interfaceLongForm(\"Gi0/1\") // \"GigabitEthernet0/1\"",
				),
				cel.UnaryBinding(interfaceLongFormBinding),
			),
		),
		cel.Function("filters.fqdnToHostname",
			cel.FunctionDocs(
				"extracts the hostname (first label) from a fully qualified domain name.",
			),
			cel.Overload("filters_fqdn_to_hostname_string_string",
				[]*cel.Type{cel.StringType}, cel.StringType,
				cel.OverloadExamples(
					"filters.fqdnToHostname(\"host1.example.com\") // \"host1\"",
				),
				cel.UnaryBinding(fqdnToHostnameBinding),
			),
		),
		cel.Function("filters.hostnameToFQDN",
			cel.FunctionDocs(
				"joins a bare hostname with a domain suffix into a fully qualified domain name.",
			),
			cel.Overload("filters_hostname_to_fqdn_string_string_string",
				[]*cel.Type{cel.StringType, cel.StringType}, cel.StringType,
				cel.OverloadExamples(
					"filters.hostnameToFQDN(\"host1\", \"example.com\") // \"host1.example.com\"",
				),
				cel.BinaryBinding(hostnameToFQDNBinding),
			),
		),
		cel.Function("filters.urlDomain",
			cel.FunctionDocs(
				"extracts the host (domain, without port) from a URL.",
			),
			cel.Overload("filters_url_domain_string_string",
				[]*cel.Type{cel.StringType}, cel.StringType,
				cel.OverloadExamples(
					"filters.urlDomain(\"https://example.com:8443/path\") // \"example.com\"",
				),
				cel.UnaryBinding(urlDomainBinding),
			),
		),
		cel.Function("filters.urlPort",
			cel.FunctionDocs(
				"extracts the port from a URL, or its scheme's default port (80 for http, 443 for https) when none is written explicitly.",
			),
			cel.Overload("filters_url_port_string_int",
				[]*cel.Type{cel.StringType}, cel.IntType,
				cel.OverloadExamples(
					"filters.urlPort(\"https://example.com/path\") // 443, the https default",
				),
				cel.UnaryBinding(urlPortBinding),
			),
		),
	}
}

func (filtersLibrary) ProgramOptions() []cel.ProgramOption {
	return nil
}

// celToString converts an arbitrary CEL value to its string form for the
// dyn-typed first argument every filters.safe* function accepts, so a
// device fact reported as an int on one firmware and a string on the next
// reaches pkg/filters identically. ok is false for a value with no string
// conversion at all (a map or a list; cel-go's own ConvertToType returns a
// types.Err for both), which the caller treats as "use fallback" rather
// than propagating a CEL evaluation error out of what is documented as a
// safe cast.
func celToString(v ref.Val) (string, bool) {
	if s, isString := v.(types.String); isString {
		return string(s), true
	}
	converted := v.ConvertToType(types.StringType)
	if types.IsError(converted) {
		return "", false
	}
	s, isString := converted.(types.String)
	if !isString {
		return "", false
	}
	return string(s), true
}

// celToInt mirrors celToString for a Go int argument, used by every
// Phase 51 filter whose CEL declaration types a parameter cel.IntType
// directly (rather than dyn): a VLAN ID, an ASN, a prefix length. Unlike
// the dyn-typed filters.safe* family, these overloads are declared as
// literally cel.IntType, so cel-go's own type checker has already
// guaranteed v is convertible before a binding ever runs; this still
// checks explicitly rather than assuming, since a *Binding function can
// be invoked directly in a test with an arbitrary ref.Val. types.Int's
// underlying width is int64; the conversion to Go int is exact on every
// platform this project builds for (amd64, arm64), both 64-bit.
func celToInt(v ref.Val) (int, bool) {
	if i, isInt := v.(types.Int); isInt {
		return int(i), true
	}
	converted := v.ConvertToType(types.IntType)
	if types.IsError(converted) {
		return 0, false
	}
	i, isInt := converted.(types.Int)
	if !isInt {
		return 0, false
	}
	return int(i), true
}

// celToBool mirrors celToString for a Go bool argument.
func celToBool(v ref.Val) (bool, bool) {
	if b, isBool := v.(types.Bool); isBool {
		return bool(b), true
	}
	converted := v.ConvertToType(types.BoolType)
	if types.IsError(converted) {
		return false, false
	}
	b, isBool := converted.(types.Bool)
	if !isBool {
		return false, false
	}
	return bool(b), true
}

// celToStringList converts a CEL list value (declared cel.ListType(cel.StringType)
// on the overloads that use it, so cel-go's own type checker has
// already guaranteed every element is a string before a binding runs)
// to a Go []string, for filters.supernet's cidrs parameter.
// ConvertToNative is every ref.Val's own generic native-conversion
// method; a CEL list's implementation (common/types/list.go) walks its
// elements converting each one, so this needs no per-element loop of
// its own.
func celToStringList(v ref.Val) ([]string, bool) {
	converted, err := v.ConvertToNative(reflect.TypeOf([]string{}))
	if err != nil {
		return nil, false
	}
	ss, ok := converted.([]string)
	if !ok {
		return nil, false
	}
	return ss, true
}

// wrapStringList wraps a Go []string as a CEL list value, the return
// side of celToStringList, for filters.subnetSplit's []string result.
// types.DefaultTypeAdapter is cel-go's own exported singleton Adapter
// for exactly this Go-value-to-ref.Val direction; a *Binding function
// has no access to the Env's own configured adapter (it only ever
// receives its arguments as bare ref.Val), and the default adapter is
// what every filter overload's activation already uses for its plain
// Go map/slice inputs, so reusing it here keeps the two directions
// consistent rather than introducing a second, only-slightly-different
// adapter.
func wrapStringList(ss []string) ref.Val {
	return types.NewStringList(types.DefaultTypeAdapter, ss)
}

func safeIntBinding(value, fallback ref.Val) ref.Val {
	fb, ok := fallback.(types.Int)
	if !ok {
		return types.NewErr("filters.safeInt: fallback must be an int, got %s", fallback.Type())
	}
	s, ok := celToString(value)
	if !ok {
		return fb
	}
	return types.Int(filters.SafeInt(s, int(fb)))
}

func safeFloatBinding(value, fallback ref.Val) ref.Val {
	fb, ok := fallback.(types.Double)
	if !ok {
		return types.NewErr("filters.safeFloat: fallback must be a double, got %s", fallback.Type())
	}
	s, ok := celToString(value)
	if !ok {
		return fb
	}
	return types.Double(filters.SafeFloat(s, float64(fb)))
}

func safeBoolBinding(value, fallback ref.Val) ref.Val {
	fb, ok := fallback.(types.Bool)
	if !ok {
		return types.NewErr("filters.safeBool: fallback must be a bool, got %s", fallback.Type())
	}
	s, ok := celToString(value)
	if !ok {
		return fb
	}
	return types.Bool(filters.SafeBool(s, bool(fb)))
}

// CIDRToNetmask's CEL binding, registered above.
func cidrToNetmaskBinding(arg0 ref.Val) ref.Val {
	goCidr, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.cidrToNetmask: argument cidr is not convertible to string")
	}
	return types.String(filters.CIDRToNetmask(goCidr))
}

// NetmaskToCIDR's CEL binding, registered above.
func netmaskToCIDRBinding(arg0 ref.Val) ref.Val {
	goNetmask, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.netmaskToCIDR: argument netmask is not convertible to string")
	}
	return types.Int(filters.NetmaskToCIDR(goNetmask))
}

// WildcardMask's CEL binding, registered above.
func wildcardMaskBinding(arg0 ref.Val) ref.Val {
	goCidr, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.wildcardMask: argument cidr is not convertible to string")
	}
	return types.String(filters.WildcardMask(goCidr))
}

// BroadcastAddress's CEL binding, registered above.
func broadcastAddressBinding(arg0 ref.Val) ref.Val {
	goCidr, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.broadcastAddress: argument cidr is not convertible to string")
	}
	return types.String(filters.BroadcastAddress(goCidr))
}

// SubnetSplit's CEL binding, registered above.
func subnetSplitBinding(arg0 ref.Val, arg1 ref.Val) ref.Val {
	goCidr, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.subnetSplit: argument cidr is not convertible to string")
	}
	goNewPrefix, ok := celToInt(arg1)
	if !ok {
		return types.NewErr("filters.subnetSplit: argument newPrefix is not convertible to int")
	}
	return wrapStringList(filters.SubnetSplit(goCidr, goNewPrefix))
}

// Supernet's CEL binding, registered above.
func supernetBinding(arg0 ref.Val) ref.Val {
	goCidrs, ok := celToStringList(arg0)
	if !ok {
		return types.NewErr("filters.supernet: argument cidrs is not convertible to a list of string")
	}
	return types.String(filters.Supernet(goCidrs))
}

// IPToInt's CEL binding, registered above.
func ipToIntBinding(arg0 ref.Val) ref.Val {
	goIp, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.ipToInt: argument ip is not convertible to string")
	}
	return types.Int(filters.IPToInt(goIp))
}

// IntToIP's CEL binding, registered above.
func intToIPBinding(arg0 ref.Val) ref.Val {
	goN, ok := celToInt(arg0)
	if !ok {
		return types.NewErr("filters.intToIP: argument n is not convertible to int")
	}
	return types.String(filters.IntToIP(goN))
}

// ToIPv4MappedIPv6's CEL binding, registered above.
func toIPv4MappedIPv6Binding(arg0 ref.Val) ref.Val {
	goIp, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.toIPv4MappedIPv6: argument ip is not convertible to string")
	}
	return types.String(filters.ToIPv4MappedIPv6(goIp))
}

// FromIPv4MappedIPv6's CEL binding, registered above.
func fromIPv4MappedIPv6Binding(arg0 ref.Val) ref.Val {
	goIp, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.fromIPv4MappedIPv6: argument ip is not convertible to string")
	}
	return types.String(filters.FromIPv4MappedIPv6(goIp))
}

// ClassifyIP's CEL binding, registered above.
func classifyIPBinding(arg0 ref.Val) ref.Val {
	goIp, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.classifyIP: argument ip is not convertible to string")
	}
	return types.String(filters.ClassifyIP(goIp))
}

// MACToCiscoFormat's CEL binding, registered above.
func macToCiscoFormatBinding(arg0 ref.Val) ref.Val {
	goMac, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.macToCiscoFormat: argument mac is not convertible to string")
	}
	return types.String(filters.MACToCiscoFormat(goMac))
}

// MACToColonFormat's CEL binding, registered above.
func macToColonFormatBinding(arg0 ref.Val) ref.Val {
	goMac, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.macToColonFormat: argument mac is not convertible to string")
	}
	return types.String(filters.MACToColonFormat(goMac))
}

// MACToWindowsFormat's CEL binding, registered above.
func macToWindowsFormatBinding(arg0 ref.Val) ref.Val {
	goMac, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.macToWindowsFormat: argument mac is not convertible to string")
	}
	return types.String(filters.MACToWindowsFormat(goMac))
}

// MACOUI's CEL binding, registered above.
func macOUIBinding(arg0 ref.Val) ref.Val {
	goMac, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.macOUI: argument mac is not convertible to string")
	}
	return types.String(filters.MACOUI(goMac))
}

// ValidateVLAN's CEL binding, registered above.
func validateVLANBinding(arg0 ref.Val) ref.Val {
	goVlan, ok := celToInt(arg0)
	if !ok {
		return types.NewErr("filters.validateVLAN: argument vlan is not convertible to int")
	}
	return types.Bool(filters.ValidateVLAN(goVlan))
}

// IsCiscoReservedVLAN's CEL binding, registered above.
func isCiscoReservedVLANBinding(arg0 ref.Val) ref.Val {
	goVlan, ok := celToInt(arg0)
	if !ok {
		return types.NewErr("filters.isCiscoReservedVLAN: argument vlan is not convertible to int")
	}
	return types.Bool(filters.IsCiscoReservedVLAN(goVlan))
}

// ValidateASN's CEL binding, registered above.
func validateASNBinding(arg0 ref.Val) ref.Val {
	goAsn, ok := celToInt(arg0)
	if !ok {
		return types.NewErr("filters.validateASN: argument asn is not convertible to int")
	}
	return types.Bool(filters.ValidateASN(goAsn))
}

// IsPrivateASN's CEL binding, registered above.
func isPrivateASNBinding(arg0 ref.Val) ref.Val {
	goAsn, ok := celToInt(arg0)
	if !ok {
		return types.NewErr("filters.isPrivateASN: argument asn is not convertible to int")
	}
	return types.Bool(filters.IsPrivateASN(goAsn))
}

// InterfaceShortForm's CEL binding, registered above.
func interfaceShortFormBinding(arg0 ref.Val) ref.Val {
	goName, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.interfaceShortForm: argument name is not convertible to string")
	}
	return types.String(filters.InterfaceShortForm(goName))
}

// InterfaceLongForm's CEL binding, registered above.
func interfaceLongFormBinding(arg0 ref.Val) ref.Val {
	goName, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.interfaceLongForm: argument name is not convertible to string")
	}
	return types.String(filters.InterfaceLongForm(goName))
}

// FQDNToHostname's CEL binding, registered above.
func fqdnToHostnameBinding(arg0 ref.Val) ref.Val {
	goFqdn, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.fqdnToHostname: argument fqdn is not convertible to string")
	}
	return types.String(filters.FQDNToHostname(goFqdn))
}

// HostnameToFQDN's CEL binding, registered above.
func hostnameToFQDNBinding(arg0 ref.Val, arg1 ref.Val) ref.Val {
	goHostname, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.hostnameToFQDN: argument hostname is not convertible to string")
	}
	goDomain, ok := celToString(arg1)
	if !ok {
		return types.NewErr("filters.hostnameToFQDN: argument domain is not convertible to string")
	}
	return types.String(filters.HostnameToFQDN(goHostname, goDomain))
}

// URLDomain's CEL binding, registered above.
func urlDomainBinding(arg0 ref.Val) ref.Val {
	goRawURL, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.urlDomain: argument rawURL is not convertible to string")
	}
	return types.String(filters.URLDomain(goRawURL))
}

// URLPort's CEL binding, registered above.
func urlPortBinding(arg0 ref.Val) ref.Val {
	goRawURL, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.urlPort: argument rawURL is not convertible to string")
	}
	return types.Int(filters.URLPort(goRawURL))
}
