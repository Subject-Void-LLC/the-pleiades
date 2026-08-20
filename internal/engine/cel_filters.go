package engine

import (
	"reflect"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"

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
		cel.Function("filters.flatten",
			cel.FunctionDocs(
				"flattens a nested map/list structure into a single-level map with dot-notation keys.",
			),
			cel.Overload("filters_flatten_map_string_any_map_string_any",
				[]*cel.Type{cel.MapType(cel.StringType, cel.DynType)}, cel.MapType(cel.StringType, cel.DynType),
				cel.OverloadExamples(
					`filters.flatten({"a": {"b": 1}}) // {"a.b": 1}`,
				),
				cel.UnaryBinding(flattenBinding),
			),
		),
		cel.Function("filters.unflatten",
			cel.FunctionDocs(
				"reconstructs a nested map/list structure from a flat map with dot-notation keys, the inverse of filters.flatten.",
			),
			cel.Overload("filters_unflatten_map_string_any_map_string_any",
				[]*cel.Type{cel.MapType(cel.StringType, cel.DynType)}, cel.MapType(cel.StringType, cel.DynType),
				cel.OverloadExamples(
					`filters.unflatten({"a.b": 1}) // {"a": {"b": 1}}`,
				),
				cel.UnaryBinding(unflattenBinding),
			),
		),
		cel.Function("filters.deepMerge",
			cel.FunctionDocs(
				"recursively merges b into a: nested maps merge key by key, lists append, and any other type in b overwrites a.",
			),
			cel.Overload("filters_deep_merge_map_string_any_map_string_any_map_string_any",
				[]*cel.Type{cel.MapType(cel.StringType, cel.DynType), cel.MapType(cel.StringType, cel.DynType)}, cel.MapType(cel.StringType, cel.DynType),
				cel.OverloadExamples(
					`filters.deepMerge({"a": {"x": 1}}, {"a": {"y": 2}}) // {"a": {"x": 1, "y": 2}}`,
				),
				cel.BinaryBinding(deepMergeBinding),
			),
		),
		cel.Function("filters.shallowMerge",
			cel.FunctionDocs(
				"merges b into a at the top level only: a key present in both is overwritten by b's value, with no recursion into nested maps.",
			),
			cel.Overload("filters_shallow_merge_map_string_any_map_string_any_map_string_any",
				[]*cel.Type{cel.MapType(cel.StringType, cel.DynType), cel.MapType(cel.StringType, cel.DynType)}, cel.MapType(cel.StringType, cel.DynType),
				cel.OverloadExamples(
					`filters.shallowMerge({"a": 1}, {"a": 2, "b": 3}) // {"a": 2, "b": 3}`,
				),
				cel.BinaryBinding(shallowMergeBinding),
			),
		),
		cel.Function("filters.csvToList",
			cel.FunctionDocs(
				"parses one CSV line into a list of fields, using encoding/csv for correct quote handling rather than a naive split on comma.",
			),
			cel.Overload("filters_csv_to_list_string_string",
				[]*cel.Type{cel.StringType}, cel.ListType(cel.StringType),
				cel.OverloadExamples(
					`filters.csvToList("a,\"b,c\",d") // ["a", "b,c", "d"]`,
				),
				cel.UnaryBinding(csvToListBinding),
			),
		),
		cel.Function("filters.listToCSV",
			cel.FunctionDocs(
				"encodes a list of fields as one CSV line, the inverse of filters.csvToList, quoting a field only when encoding/csv determines it needs it.",
			),
			cel.Overload("filters_list_to_csv_string_string",
				[]*cel.Type{cel.ListType(cel.StringType)}, cel.StringType,
				cel.OverloadExamples(
					`filters.listToCSV(["a", "b,c", "d"]) // "a,\"b,c\",d"`,
				),
				cel.UnaryBinding(listToCSVBinding),
			),
		),
		cel.Function("filters.pluck",
			cel.FunctionDocs(
				"extracts one key's value from each map in a list, skipping a map that does not have the key.",
			),
			cel.Overload("filters_pluck_map_string_any_string_any",
				[]*cel.Type{cel.ListType(cel.MapType(cel.StringType, cel.DynType)), cel.StringType}, cel.ListType(cel.DynType),
				cel.OverloadExamples(
					`filters.pluck([{"name": "a", "val": 1}, {"name": "b"}], "val") // [1]`,
				),
				cel.BinaryBinding(pluckBinding),
			),
		),
		cel.Function("filters.yamlToJSON",
			cel.FunctionDocs(
				"converts a YAML document to its equivalent JSON text.",
			),
			cel.Overload("filters_yaml_to_json_string_string",
				[]*cel.Type{cel.StringType}, cel.StringType,
				cel.OverloadExamples(
					`filters.yamlToJSON("a: 1\n") // "{\"a\":1}"`,
				),
				cel.UnaryBinding(yamlToJSONBinding),
			),
		),
		cel.Function("filters.jsonToYAML",
			cel.FunctionDocs(
				"converts a JSON document to its equivalent YAML text.",
			),
			cel.Overload("filters_json_to_yaml_string_string",
				[]*cel.Type{cel.StringType}, cel.StringType,
				cel.OverloadExamples(
					`filters.jsonToYAML("{\"a\":1}") // "a: 1\n"`,
				),
				cel.UnaryBinding(jsonToYAMLBinding),
			),
		),
		cel.Function("filters.generateUUIDv4",
			cel.FunctionDocs(
				"generates a random version-4 UUID. Unlike every other filters.* function, this one is not deterministic: it takes no arguments and returns a different value on every call.",
			),
			cel.Overload("filters_generate_uuidv4_string",
				[]*cel.Type{}, cel.StringType,
				cel.OverloadExamples(
					`filters.generateUUIDv4() // e.g. "3b12f1df-5232-4804-897e-917bf397618a" (a new random UUID every call)`,
				),
				cel.FunctionBinding(generateUUIDv4Binding),
			),
		),
		cel.Function("filters.xmlToJSON",
			cel.FunctionDocs(
				"converts an XML document to JSON text using one documented, opinionated element/attribute mapping (see pkg/filters.XMLToJSON's own doc comment); XML has no canonical JSON shape.",
			),
			cel.Overload("filters_xml_to_json_string_string",
				[]*cel.Type{cel.StringType}, cel.StringType,
				cel.OverloadExamples(
					`filters.xmlToJSON("<a><b>1</b></a>") // "{\"a\":{\"b\":\"1\"}}"`,
				),
				cel.UnaryBinding(xmlToJSONBinding),
			),
		),
		cel.Function("filters.urlEncode",
			cel.FunctionDocs(
				"percent-encodes a string for safe inclusion in a URL query component (space becomes +).",
			),
			cel.Overload("filters_url_encode_string_string",
				[]*cel.Type{cel.StringType}, cel.StringType,
				cel.OverloadExamples(
					`filters.urlEncode("hello world") // "hello+world"`,
				),
				cel.UnaryBinding(urlEncodeBinding),
			),
		),
		cel.Function("filters.urlDecode",
			cel.FunctionDocs(
				"reverses percent-encoding applied to a URL query component, the inverse of filters.urlEncode.",
			),
			cel.Overload("filters_url_decode_string_string",
				[]*cel.Type{cel.StringType}, cel.StringType,
				cel.OverloadExamples(
					`filters.urlDecode("hello+world") // "hello world"`,
				),
				cel.UnaryBinding(urlDecodeBinding),
			),
		),
		cel.Function("filters.camelToSnake",
			cel.FunctionDocs(
				"converts a camelCase or PascalCase identifier to snake_case, treating a run of uppercase runes as one acronym.",
			),
			cel.Overload("filters_camel_to_snake_string_string",
				[]*cel.Type{cel.StringType}, cel.StringType,
				cel.OverloadExamples(
					`filters.camelToSnake("classifyIP") // "classify_ip"`,
				),
				cel.UnaryBinding(camelToSnakeBinding),
			),
		),
		cel.Function("filters.snakeToCamel",
			cel.FunctionDocs(
				"converts a snake_case identifier to camelCase, the inverse of filters.camelToSnake.",
			),
			cel.Overload("filters_snake_to_camel_string_string",
				[]*cel.Type{cel.StringType}, cel.StringType,
				cel.OverloadExamples(
					`filters.snakeToCamel("classify_ip") // "classifyIp"`,
				),
				cel.UnaryBinding(snakeToCamelBinding),
			),
		),
		cel.Function("filters.stringToHex",
			cel.FunctionDocs(
				"encodes a string as lowercase hexadecimal.",
			),
			cel.Overload("filters_string_to_hex_string_string",
				[]*cel.Type{cel.StringType}, cel.StringType,
				cel.OverloadExamples(
					`filters.stringToHex("hi") // "6869"`,
				),
				cel.UnaryBinding(stringToHexBinding),
			),
		),
		cel.Function("filters.hexToString",
			cel.FunctionDocs(
				"decodes a hexadecimal string back to its original string, the inverse of filters.stringToHex.",
			),
			cel.Overload("filters_hex_to_string_string_string",
				[]*cel.Type{cel.StringType}, cel.StringType,
				cel.OverloadExamples(
					`filters.hexToString("6869") // "hi"`,
				),
				cel.UnaryBinding(hexToStringBinding),
			),
		),
		cel.Function("filters.regexExtract",
			cel.FunctionDocs(
				"extracts one named capture group's match from s against pattern.",
			),
			cel.Overload("filters_regex_extract_string_string_string_string",
				[]*cel.Type{cel.StringType, cel.StringType, cel.StringType}, cel.StringType,
				cel.OverloadExamples(
					`filters.regexExtract("host1.example.com", "^(?P<host>[^.]+)\\.", "host") // "host1"`,
				),
				cel.FunctionBinding(regexExtractBinding),
			),
		),
		cel.Function("filters.maskSecret",
			cel.FunctionDocs(
				"masks a secret, keeping only its last keepLast characters visible.",
			),
			cel.Overload("filters_mask_secret_string_int_string",
				[]*cel.Type{cel.StringType, cel.IntType}, cel.StringType,
				cel.OverloadExamples(
					`filters.maskSecret("hunter2", 2) // "*****r2"`,
				),
				cel.BinaryBinding(maskSecretBinding),
			),
		),
		cel.Function("filters.windowsPathToPOSIX",
			cel.FunctionDocs(
				"reformats a Windows-style path to use forward slashes, a string transform only: it never opens, joins, or resolves the path.",
			),
			cel.Overload("filters_windows_path_to_posix_string_string",
				[]*cel.Type{cel.StringType}, cel.StringType,
				cel.OverloadExamples(
					`filters.windowsPathToPOSIX("C:\\Users\\foo") // "C:/Users/foo"`,
				),
				cel.UnaryBinding(windowsPathToPOSIXBinding),
			),
		),
		cel.Function("filters.posixPathToWindows",
			cel.FunctionDocs(
				"reformats a POSIX-style path to use backslashes, the inverse of filters.windowsPathToPOSIX; a string transform only.",
			),
			cel.Overload("filters_posix_path_to_windows_string_string",
				[]*cel.Type{cel.StringType}, cel.StringType,
				cel.OverloadExamples(
					`filters.posixPathToWindows("/home/foo") // "\\home\\foo"`,
				),
				cel.UnaryBinding(posixPathToWindowsBinding),
			),
		),
		cel.Function("filters.octalToSymbolicPerms",
			cel.FunctionDocs(
				"converts a 3- or 4-digit octal Unix permission string to its 9-character symbolic form (rwxr-xr-x).",
			),
			cel.Overload("filters_octal_to_symbolic_perms_string_string",
				[]*cel.Type{cel.StringType}, cel.StringType,
				cel.OverloadExamples(
					`filters.octalToSymbolicPerms("755") // "rwxr-xr-x"`,
				),
				cel.UnaryBinding(octalToSymbolicPermsBinding),
			),
		),
		cel.Function("filters.symbolicToOctalPerms",
			cel.FunctionDocs(
				"converts a 9-character symbolic Unix permission string to its 4-digit octal form, the inverse of filters.octalToSymbolicPerms.",
			),
			cel.Overload("filters_symbolic_to_octal_perms_string_string",
				[]*cel.Type{cel.StringType}, cel.StringType,
				cel.OverloadExamples(
					`filters.symbolicToOctalPerms("rwxr-xr-x") // "755"`,
				),
				cel.UnaryBinding(symbolicToOctalPermsBinding),
			),
		),
		cel.Function("filters.bytesToHuman",
			cel.FunctionDocs(
				"formats a byte count as a human-readable binary (base-1024) size, e.g. 1536 -> 1.5KiB.",
			),
			cel.Overload("filters_bytes_to_human_int_string",
				[]*cel.Type{cel.IntType}, cel.StringType,
				cel.OverloadExamples(
					`filters.bytesToHuman(1536) // "1.5KiB"`,
				),
				cel.UnaryBinding(bytesToHumanBinding),
			),
		),
		cel.Function("filters.humanToBytes",
			cel.FunctionDocs(
				"parses a human-readable binary (base-1024) size back to a byte count, the inverse of filters.bytesToHuman.",
			),
			cel.Overload("filters_human_to_bytes_string_int",
				[]*cel.Type{cel.StringType}, cel.IntType,
				cel.OverloadExamples(
					`filters.humanToBytes("1.5KiB") // 1536`,
				),
				cel.UnaryBinding(humanToBytesBinding),
			),
		),
		cel.Function("filters.isAbsolutePath",
			cel.FunctionDocs(
				"reports whether path is absolute under POSIX or Windows conventions (drive-letter or UNC).",
			),
			cel.Overload("filters_is_absolute_path_string_bool",
				[]*cel.Type{cel.StringType}, cel.BoolType,
				cel.OverloadExamples(
					`filters.isAbsolutePath("/etc/passwd") // true`,
				),
				cel.UnaryBinding(isAbsolutePathBinding),
			),
		),
		cel.Function("filters.isEmptyOrWhitespace",
			cel.FunctionDocs(
				"reports whether s is empty or contains only whitespace.",
			),
			cel.Overload("filters_is_empty_or_whitespace_string_bool",
				[]*cel.Type{cel.StringType}, cel.BoolType,
				cel.OverloadExamples(
					`filters.isEmptyOrWhitespace("   ") // true`,
				),
				cel.UnaryBinding(isEmptyOrWhitespaceBinding),
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

// celToAny recursively converts an arbitrary CEL value to a plain Go
// value shaped map[string]any/[]any/string/int64/float64/bool/nil (and
// so on down through every nested map and list), the shared walker
// behind celToMap/celToDynList/celToMapList.
//
// This does not use ref.Val's own ConvertToNative for the map/list
// cases, and the reason is a real, verified-not-assumed divergence:
// ConvertToNative(map[string]any{}) converts a *top-level* map's keys
// and values correctly, but for a map built from a CEL map literal
// (filters.flatten({"a": {"b": 1}}), not a map arriving from a plain Go
// value already wrapped by types.NewDynamicMap), it converts a *nested*
// map's value to native Go via cel-go's internal ConvertToNative(any)
// path, which substitutes map[any]any instead of map[string]any at that
// level -- verified directly with a scratch program before writing this
// comment. A filter's own Go code (Flatten's flattenInto, in
// particular) type-switches on map[string]any/[]any specifically, so a
// silently different nested shape would make it treat a legitimate
// nested map as an opaque leaf value instead of recursing into it. This
// walker sidesteps the discrepancy entirely by always producing
// map[string]any/[]any itself, recursively, regardless of which
// internal representation the source ref.Val happens to use: it type-
// switches on the traits.Mapper/traits.Lister interfaces every cel-go
// map/list representation implements (a literal, a value wrapped from a
// native Go map via types.NewDynamicMap, a value read out of a proto
// struct) and only falls back to Value() -- ref.Val's own "give me the
// underlying native representation" accessor -- for an actual scalar
// leaf.
func celToAny(v ref.Val) (any, bool) {
	switch t := v.(type) {
	case traits.Mapper:
		it := t.Iterator()
		m := make(map[string]any, int(t.Size().(types.Int)))
		for it.HasNext() == types.True {
			k := it.Next()
			ks, ok := celToString(k)
			if !ok {
				return nil, false
			}
			ev, found := t.Find(k)
			if !found {
				return nil, false
			}
			gv, ok := celToAny(ev)
			if !ok {
				return nil, false
			}
			m[ks] = gv
		}
		return m, true
	case traits.Lister:
		it := t.Iterator()
		list := make([]any, 0, int(t.Size().(types.Int)))
		for it.HasNext() == types.True {
			gv, ok := celToAny(it.Next())
			if !ok {
				return nil, false
			}
			list = append(list, gv)
		}
		return list, true
	default:
		return v.Value(), true
	}
}

// celToMap converts a CEL map value (declared cel.MapType(cel.StringType,
// cel.DynType) on the overloads that use it) to a Go map[string]any, for
// Phase 52's structured-data filters (Flatten, Unflatten, DeepMerge,
// ShallowMerge). See celToAny's own doc comment for why this walks the
// value itself rather than calling ConvertToNative directly.
func celToMap(v ref.Val) (map[string]any, bool) {
	a, ok := celToAny(v)
	if !ok {
		return nil, false
	}
	m, ok := a.(map[string]any)
	return m, ok
}

// wrapMap wraps a Go map[string]any as a CEL map value, the return side
// of celToMap. types.NewDynamicMap mirrors wrapStringList's own use of
// types.NewDynamicList: cel-go's exported constructor for adapting an
// arbitrary Go value via the shared DefaultTypeAdapter, the same
// adapter every filter overload's activation already uses for its plain
// Go inputs. Unlike celToMap's own read side, this direction has no
// nested-shape discrepancy to work around: the adapter re-adapts each
// element lazily, on access, straight from the real Go value this
// function was given, so a nested map[string]any/[]any inside m reaches
// a caller (a subsequent when_cel comparison, a `.` field access) with
// its shape intact.
func wrapMap(m map[string]any) ref.Val {
	return types.NewDynamicMap(types.DefaultTypeAdapter, m)
}

// celToDynList converts a CEL list value (declared cel.ListType(cel.DynType))
// to a Go []any, for Phase 52's Pluck result (one key's value, of
// whatever type each map's own value happened to be, across a list of
// maps). See celToAny's own doc comment for why this walks the value
// itself rather than calling ConvertToNative directly.
func celToDynList(v ref.Val) ([]any, bool) {
	a, ok := celToAny(v)
	if !ok {
		return nil, false
	}
	list, ok := a.([]any)
	return list, ok
}

// wrapDynList wraps a Go []any as a CEL list value, the return side of
// celToDynList.
func wrapDynList(ss []any) ref.Val {
	return types.NewDynamicList(types.DefaultTypeAdapter, ss)
}

// celToMapList converts a CEL list of maps (declared
// cel.ListType(cel.MapType(cel.StringType, cel.DynType))) to a Go
// []map[string]any, for Phase 52's Pluck parameter (a list of records to
// extract one key from). See celToAny's own doc comment for why this
// walks the value itself rather than calling ConvertToNative directly.
func celToMapList(v ref.Val) ([]map[string]any, bool) {
	a, ok := celToAny(v)
	if !ok {
		return nil, false
	}
	list, ok := a.([]any)
	if !ok {
		return nil, false
	}
	ms := make([]map[string]any, len(list))
	for i, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			return nil, false
		}
		ms[i] = m
	}
	return ms, true
}

// wrapMapList wraps a Go []map[string]any as a CEL list value, the
// return side of celToMapList. Nothing in Phase 52 returns this shape
// yet (Pluck's own result is []any, one value per map, not the maps
// themselves), but it completes the conversionFor/wrapperFor pair
// internal/forge/filterscaffold's wellKnownCELTypes table declares for
// "[]map[string]any", so a future filter returning a list of records
// (a filtered subset of Pluck's own input shape, for instance) needs no
// new helper.
func wrapMapList(ms []map[string]any) ref.Val {
	return types.NewDynamicList(types.DefaultTypeAdapter, ms)
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

// Flatten's CEL binding, registered above.
func flattenBinding(arg0 ref.Val) ref.Val {
	goM, ok := celToMap(arg0)
	if !ok {
		return types.NewErr("filters.flatten: argument m is not convertible to map[string]any")
	}
	return wrapMap(filters.Flatten(goM))
}

// Unflatten's CEL binding, registered above.
func unflattenBinding(arg0 ref.Val) ref.Val {
	goM, ok := celToMap(arg0)
	if !ok {
		return types.NewErr("filters.unflatten: argument m is not convertible to map[string]any")
	}
	return wrapMap(filters.Unflatten(goM))
}

// DeepMerge's CEL binding, registered above.
func deepMergeBinding(arg0 ref.Val, arg1 ref.Val) ref.Val {
	goA, ok := celToMap(arg0)
	if !ok {
		return types.NewErr("filters.deepMerge: argument a is not convertible to map[string]any")
	}
	goB, ok := celToMap(arg1)
	if !ok {
		return types.NewErr("filters.deepMerge: argument b is not convertible to map[string]any")
	}
	return wrapMap(filters.DeepMerge(goA, goB))
}

// ShallowMerge's CEL binding, registered above.
func shallowMergeBinding(arg0 ref.Val, arg1 ref.Val) ref.Val {
	goA, ok := celToMap(arg0)
	if !ok {
		return types.NewErr("filters.shallowMerge: argument a is not convertible to map[string]any")
	}
	goB, ok := celToMap(arg1)
	if !ok {
		return types.NewErr("filters.shallowMerge: argument b is not convertible to map[string]any")
	}
	return wrapMap(filters.ShallowMerge(goA, goB))
}

// CSVToList's CEL binding, registered above.
func csvToListBinding(arg0 ref.Val) ref.Val {
	goLine, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.csvToList: argument line is not convertible to string")
	}
	return wrapStringList(filters.CSVToList(goLine))
}

// ListToCSV's CEL binding, registered above.
func listToCSVBinding(arg0 ref.Val) ref.Val {
	goList, ok := celToStringList(arg0)
	if !ok {
		return types.NewErr("filters.listToCSV: argument list is not convertible to []string")
	}
	return types.String(filters.ListToCSV(goList))
}

// Pluck's CEL binding, registered above.
func pluckBinding(arg0 ref.Val, arg1 ref.Val) ref.Val {
	goList, ok := celToMapList(arg0)
	if !ok {
		return types.NewErr("filters.pluck: argument list is not convertible to []map[string]any")
	}
	goKey, ok := celToString(arg1)
	if !ok {
		return types.NewErr("filters.pluck: argument key is not convertible to string")
	}
	return wrapDynList(filters.Pluck(goList, goKey))
}

// YAMLToJSON's CEL binding, registered above.
func yamlToJSONBinding(arg0 ref.Val) ref.Val {
	goYamlText, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.yamlToJSON: argument yamlText is not convertible to string")
	}
	return types.String(filters.YAMLToJSON(goYamlText))
}

// JSONToYAML's CEL binding, registered above.
func jsonToYAMLBinding(arg0 ref.Val) ref.Val {
	goJsonText, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.jsonToYAML: argument jsonText is not convertible to string")
	}
	return types.String(filters.JSONToYAML(goJsonText))
}

// GenerateUUIDv4's CEL binding, registered above. Takes no arguments:
// cel.FunctionBinding's real signature is func(...ref.Val) ref.Val,
// which a zero-arity overload simply never calls with any.
func generateUUIDv4Binding(_ ...ref.Val) ref.Val {
	return types.String(filters.GenerateUUIDv4())
}

// XMLToJSON's CEL binding, registered above.
func xmlToJSONBinding(arg0 ref.Val) ref.Val {
	goXmlText, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.xmlToJSON: argument xmlText is not convertible to string")
	}
	return types.String(filters.XMLToJSON(goXmlText))
}

// URLEncode's CEL binding, registered above.
func urlEncodeBinding(arg0 ref.Val) ref.Val {
	goS, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.urlEncode: argument s is not convertible to string")
	}
	return types.String(filters.URLEncode(goS))
}

// URLDecode's CEL binding, registered above.
func urlDecodeBinding(arg0 ref.Val) ref.Val {
	goS, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.urlDecode: argument s is not convertible to string")
	}
	return types.String(filters.URLDecode(goS))
}

// CamelToSnake's CEL binding, registered above.
func camelToSnakeBinding(arg0 ref.Val) ref.Val {
	goS, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.camelToSnake: argument s is not convertible to string")
	}
	return types.String(filters.CamelToSnake(goS))
}

// SnakeToCamel's CEL binding, registered above.
func snakeToCamelBinding(arg0 ref.Val) ref.Val {
	goS, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.snakeToCamel: argument s is not convertible to string")
	}
	return types.String(filters.SnakeToCamel(goS))
}

// StringToHex's CEL binding, registered above.
func stringToHexBinding(arg0 ref.Val) ref.Val {
	goS, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.stringToHex: argument s is not convertible to string")
	}
	return types.String(filters.StringToHex(goS))
}

// HexToString's CEL binding, registered above.
func hexToStringBinding(arg0 ref.Val) ref.Val {
	goS, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.hexToString: argument s is not convertible to string")
	}
	return types.String(filters.HexToString(goS))
}

// RegexExtract's CEL binding, registered above. Three arguments: no
// typed OverloadOpt in cel-go covers arity three, so this uses
// cel.FunctionBinding directly (its real signature, func(...ref.Val)
// ref.Val), the same escape hatch internal/forge/filterscaffold's own
// bindingFuncFor names for anything wider than two -- a human-written
// binding, not a generated one, since no arity-three overload has
// existed in this codebase before this filter.
func regexExtractBinding(args ...ref.Val) ref.Val {
	if len(args) != 3 {
		return types.NewErr("filters.regexExtract: expected 3 arguments, got %d", len(args))
	}
	goS, ok := celToString(args[0])
	if !ok {
		return types.NewErr("filters.regexExtract: argument s is not convertible to string")
	}
	goPattern, ok := celToString(args[1])
	if !ok {
		return types.NewErr("filters.regexExtract: argument pattern is not convertible to string")
	}
	goGroupName, ok := celToString(args[2])
	if !ok {
		return types.NewErr("filters.regexExtract: argument groupName is not convertible to string")
	}
	return types.String(filters.RegexExtract(goS, goPattern, goGroupName))
}

// MaskSecret's CEL binding, registered above.
func maskSecretBinding(arg0 ref.Val, arg1 ref.Val) ref.Val {
	goS, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.maskSecret: argument s is not convertible to string")
	}
	goKeepLast, ok := celToInt(arg1)
	if !ok {
		return types.NewErr("filters.maskSecret: argument keepLast is not convertible to int")
	}
	return types.String(filters.MaskSecret(goS, goKeepLast))
}

// WindowsPathToPOSIX's CEL binding, registered above.
func windowsPathToPOSIXBinding(arg0 ref.Val) ref.Val {
	goPath, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.windowsPathToPOSIX: argument path is not convertible to string")
	}
	return types.String(filters.WindowsPathToPOSIX(goPath))
}

// POSIXPathToWindows's CEL binding, registered above.
func posixPathToWindowsBinding(arg0 ref.Val) ref.Val {
	goPath, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.posixPathToWindows: argument path is not convertible to string")
	}
	return types.String(filters.POSIXPathToWindows(goPath))
}

// OctalToSymbolicPerms's CEL binding, registered above.
func octalToSymbolicPermsBinding(arg0 ref.Val) ref.Val {
	goOctal, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.octalToSymbolicPerms: argument octal is not convertible to string")
	}
	return types.String(filters.OctalToSymbolicPerms(goOctal))
}

// SymbolicToOctalPerms's CEL binding, registered above.
func symbolicToOctalPermsBinding(arg0 ref.Val) ref.Val {
	goSymbolic, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.symbolicToOctalPerms: argument symbolic is not convertible to string")
	}
	return types.String(filters.SymbolicToOctalPerms(goSymbolic))
}

// BytesToHuman's CEL binding, registered above.
func bytesToHumanBinding(arg0 ref.Val) ref.Val {
	goN, ok := celToInt(arg0)
	if !ok {
		return types.NewErr("filters.bytesToHuman: argument n is not convertible to int")
	}
	return types.String(filters.BytesToHuman(goN))
}

// HumanToBytes's CEL binding, registered above.
func humanToBytesBinding(arg0 ref.Val) ref.Val {
	goS, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.humanToBytes: argument s is not convertible to string")
	}
	return types.Int(filters.HumanToBytes(goS))
}

// IsAbsolutePath's CEL binding, registered above.
func isAbsolutePathBinding(arg0 ref.Val) ref.Val {
	goPath, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.isAbsolutePath: argument path is not convertible to string")
	}
	return types.Bool(filters.IsAbsolutePath(goPath))
}

// IsEmptyOrWhitespace's CEL binding, registered above.
func isEmptyOrWhitespaceBinding(arg0 ref.Val) ref.Val {
	goS, ok := celToString(arg0)
	if !ok {
		return types.NewErr("filters.isEmptyOrWhitespace: argument s is not convertible to string")
	}
	return types.Bool(filters.IsEmptyOrWhitespace(goS))
}
