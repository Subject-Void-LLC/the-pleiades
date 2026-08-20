package engine

import (
	"testing"

	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
)

// TestCelToXHelpers directly exercises celToString/celToInt/celToBool/
// celToStringList's own branches (fast path on an already-typed value,
// the ConvertToType success path for a convertible-but-differently-typed
// value, and the failure path for a value with no conversion at all),
// a whitebox test (package engine, not engine_test) since these helpers
// are unexported. This is the direct proof for what
// TestCELFilters_Phase51NetworkFilters and TestCELFilters_SafeInt/
// _SafeFloatAndSafeBool already prove indirectly through the real
// compiled CEL path: those tests only ever supply values cel-go's own
// type checker has already approved, so they cannot reach the "not
// convertible" branch a Phase 51 binding's own defensive check guards
// against a caller who invoked the binding function directly with the
// wrong ref.Val shape.
func TestCelToXHelpers(t *testing.T) {
	// A CEL list has no conversion to string, int, or bool (confirmed by
	// reading common/types/list.go's own ConvertToType, which only
	// supports ListType and TypeType); it is the one value this test
	// reuses across all three failure checks.
	list := types.NewDynamicList(types.DefaultTypeAdapter, []int{1, 2, 3})

	if _, ok := celToString(list); ok {
		t.Error("celToString(list) should fail, has no string conversion")
	}
	if _, ok := celToInt(list); ok {
		t.Error("celToInt(list) should fail, has no int conversion")
	}
	if _, ok := celToBool(list); ok {
		t.Error("celToBool(list) should fail, has no bool conversion")
	}
	if _, ok := celToStringList(types.Int(5)); ok {
		t.Error("celToStringList(int) should fail, has no []string conversion")
	}

	if s, ok := celToString(types.String("x")); !ok || s != "x" {
		t.Errorf("celToString(String(\"x\")) = %q, %v; want \"x\", true (fast path)", s, ok)
	}
	if n, ok := celToInt(types.Int(5)); !ok || n != 5 {
		t.Errorf("celToInt(Int(5)) = %d, %v; want 5, true (fast path)", n, ok)
	}
	if b, ok := celToBool(types.Bool(true)); !ok || !b {
		t.Errorf("celToBool(Bool(true)) = %v, %v; want true, true (fast path)", b, ok)
	}

	if s, ok := celToString(types.Int(7)); !ok || s != "7" {
		t.Errorf("celToString(Int(7)) = %q, %v; want \"7\", true (ConvertToType path)", s, ok)
	}
	// No overload in this Part ever passes a non-dyn-typed argument
	// through celToInt/celToBool's ConvertToType path in practice
	// (cel.IntType/cel.BoolType's own static declaration means only an
	// already-Int/Bool-typed expression, or an explicit int()/bool()
	// cast, ever reaches a binding at that position, both of which hit
	// the fast path above instead), but the path exists in the code and
	// is asserted here directly rather than left unverified.
	if n, ok := celToInt(types.String("5")); !ok || n != 5 {
		t.Errorf("celToInt(String(\"5\")) = %d, %v; want 5, true (ConvertToType path)", n, ok)
	}
	if b, ok := celToBool(types.String("true")); !ok || !b {
		t.Errorf("celToBool(String(\"true\")) = %v, %v; want true, true (ConvertToType path)", b, ok)
	}
	if ss, ok := celToStringList(types.NewStringList(types.DefaultTypeAdapter, []string{"a", "b"})); !ok || len(ss) != 2 {
		t.Errorf("celToStringList([\"a\",\"b\"]) = %v, %v; want [a b], true", ss, ok)
	}

	// A scalar (not a container) has no map or list shape for celToAny's
	// traits.Mapper/traits.Lister type switch to recognize, so it falls
	// through to a bare Value() -- the one path celToMap/celToDynList/
	// celToMapList all reject, since none of int64, string, bool, and so
	// on assert to map[string]any/[]any.
	scalar := types.Int(5)
	if _, ok := celToMap(scalar); ok {
		t.Error("celToMap(int) should fail, has no map[string]any conversion")
	}
	if _, ok := celToDynList(scalar); ok {
		t.Error("celToDynList(int) should fail, has no []any conversion")
	}
	if _, ok := celToMapList(scalar); ok {
		t.Error("celToMapList(int) should fail, has no []map[string]any conversion")
	}

	// celToMap(wrapMap(m)) round-trips m's shape (a nested map is still
	// a nested map[string]any, not the map[any]any a naive
	// ConvertToNative(any) call would substitute -- celToAny's own doc
	// comment has the full story), but not m's exact scalar Go types: a
	// leaf int comes back int64, CEL's own int width, since it was
	// re-adapted through the environment's type system on the way out
	// and back in.
	m := map[string]any{"a": 1, "b": map[string]any{"c": 2}}
	got, ok := celToMap(wrapMap(m))
	if !ok {
		t.Fatalf("celToMap(wrapMap(%#v)): ok = false, want true", m)
	}
	if got["a"] != int64(1) {
		t.Errorf("celToMap(wrapMap(%#v))[\"a\"] = %#v, want int64(1)", m, got["a"])
	}
	nested, ok := got["b"].(map[string]any)
	if !ok || nested["c"] != int64(2) {
		t.Errorf("celToMap(wrapMap(%#v))[\"b\"] = %#v, want a nested map[string]any with c = int64(2)", m, got["b"])
	}

	dl := []any{1, "two", true}
	if got, ok := celToDynList(wrapDynList(dl)); !ok || len(got) != 3 {
		t.Errorf("celToDynList(wrapDynList(%#v)) = %#v, %v; want a round trip", dl, got, ok)
	}
	ml := []map[string]any{{"a": 1}, {"b": 2}}
	if got, ok := celToMapList(wrapMapList(ml)); !ok || len(got) != 2 {
		t.Errorf("celToMapList(wrapMapList(%#v)) = %#v, %v; want a round trip", ml, got, ok)
	}
}

// TestPhase51Bindings_RejectUnconvertibleArguments directly calls every
// Phase 51 binding function (all unexported, hence this whitebox test)
// with a value that has no conversion to the argument's declared type,
// asserting each returns a types.Err rather than panicking or silently
// producing a zero value. Through the real compiled CEL path this
// branch is unreachable: cel-go's own type checker already guarantees
// argument convertibility for a statically cel.StringType/cel.IntType-
// declared overload before any binding runs (TestCELFilters_
// Phase51NetworkFilters exercises exactly that reachable path). This
// test exists for the same reason a defensive check is written at all:
// to prove it actually works if a future direct Go-level call, or a
// cel-go behavior change, ever does reach it.
func TestPhase51Bindings_RejectUnconvertibleArguments(t *testing.T) {
	list := types.NewDynamicList(types.DefaultTypeAdapter, []int{1, 2, 3})

	unary := map[string]func(ref.Val) ref.Val{
		"cidrToNetmask":       cidrToNetmaskBinding,
		"netmaskToCIDR":       netmaskToCIDRBinding,
		"wildcardMask":        wildcardMaskBinding,
		"broadcastAddress":    broadcastAddressBinding,
		"supernet":            supernetBinding,
		"ipToInt":             ipToIntBinding,
		"intToIP":             intToIPBinding,
		"toIPv4MappedIPv6":    toIPv4MappedIPv6Binding,
		"fromIPv4MappedIPv6":  fromIPv4MappedIPv6Binding,
		"classifyIP":          classifyIPBinding,
		"macToCiscoFormat":    macToCiscoFormatBinding,
		"macToColonFormat":    macToColonFormatBinding,
		"macToWindowsFormat":  macToWindowsFormatBinding,
		"macOUI":              macOUIBinding,
		"validateVLAN":        validateVLANBinding,
		"isCiscoReservedVLAN": isCiscoReservedVLANBinding,
		"validateASN":         validateASNBinding,
		"isPrivateASN":        isPrivateASNBinding,
		"interfaceShortForm":  interfaceShortFormBinding,
		"interfaceLongForm":   interfaceLongFormBinding,
		"fqdnToHostname":      fqdnToHostnameBinding,
		"urlDomain":           urlDomainBinding,
		"urlPort":             urlPortBinding,
	}
	for name, fn := range unary {
		t.Run(name, func(t *testing.T) {
			if got := fn(list); !types.IsError(got) {
				t.Errorf("%sBinding(list) = %v, want a types.Err", name, got)
			}
		})
	}

	t.Run("subnetSplit_arg0", func(t *testing.T) {
		if got := subnetSplitBinding(list, types.Int(24)); !types.IsError(got) {
			t.Errorf("subnetSplitBinding(list, 24) = %v, want a types.Err", got)
		}
	})
	t.Run("subnetSplit_arg1", func(t *testing.T) {
		if got := subnetSplitBinding(types.String("10.0.0.0/24"), list); !types.IsError(got) {
			t.Errorf("subnetSplitBinding(\"10.0.0.0/24\", list) = %v, want a types.Err", got)
		}
	})
	t.Run("hostnameToFQDN_arg0", func(t *testing.T) {
		if got := hostnameToFQDNBinding(list, types.String("example.com")); !types.IsError(got) {
			t.Errorf("hostnameToFQDNBinding(list, \"example.com\") = %v, want a types.Err", got)
		}
	})
	t.Run("hostnameToFQDN_arg1", func(t *testing.T) {
		if got := hostnameToFQDNBinding(types.String("host1"), list); !types.IsError(got) {
			t.Errorf("hostnameToFQDNBinding(\"host1\", list) = %v, want a types.Err", got)
		}
	})
}

// TestPhase52Bindings_RejectUnconvertibleArguments mirrors
// TestPhase51Bindings_RejectUnconvertibleArguments for Phase 52's own 11
// structured-data bindings: unreachable through the real compiled CEL
// path for the same reason (cel-go's own type checker already guarantees
// argument convertibility for a statically-typed overload), reachable
// only through a direct Go-level call.
func TestPhase52Bindings_RejectUnconvertibleArguments(t *testing.T) {
	scalar := types.Int(5) // no map[string]any, []any or []map[string]any conversion.
	notString := types.NewDynamicList(types.DefaultTypeAdapter, []int{1, 2, 3})

	unary := map[string]func(ref.Val) ref.Val{
		"flatten":    flattenBinding,
		"unflatten":  unflattenBinding,
		"csvToList":  csvToListBinding,
		"listToCSV":  listToCSVBinding,
		"yamlToJSON": yamlToJSONBinding,
		"jsonToYAML": jsonToYAMLBinding,
		"xmlToJSON":  xmlToJSONBinding,
	}
	unaryArg := map[string]ref.Val{
		"flatten":    scalar,
		"unflatten":  scalar,
		"csvToList":  notString,
		"listToCSV":  notString, // notString also has no []string conversion.
		"yamlToJSON": notString,
		"jsonToYAML": notString,
		"xmlToJSON":  notString,
	}
	for name, fn := range unary {
		t.Run(name, func(t *testing.T) {
			if got := fn(unaryArg[name]); !types.IsError(got) {
				t.Errorf("%sBinding(%v) = %v, want a types.Err", name, unaryArg[name], got)
			}
		})
	}

	t.Run("deepMerge_arg0", func(t *testing.T) {
		if got := deepMergeBinding(scalar, scalar); !types.IsError(got) {
			t.Errorf("deepMergeBinding(int, int) = %v, want a types.Err", got)
		}
	})
	t.Run("shallowMerge_arg1", func(t *testing.T) {
		validMap := wrapMap(map[string]any{"a": 1})
		if got := shallowMergeBinding(validMap, scalar); !types.IsError(got) {
			t.Errorf("shallowMergeBinding(map, int) = %v, want a types.Err", got)
		}
	})
	t.Run("pluck_arg0", func(t *testing.T) {
		if got := pluckBinding(scalar, types.String("k")); !types.IsError(got) {
			t.Errorf("pluckBinding(int, string) = %v, want a types.Err", got)
		}
	})
	t.Run("pluck_arg1", func(t *testing.T) {
		validList := wrapMapList([]map[string]any{{"a": 1}})
		if got := pluckBinding(validList, notString); !types.IsError(got) {
			t.Errorf("pluckBinding(list, non-string) = %v, want a types.Err", got)
		}
	})
}

// TestSafeBindings_RejectMismatchedFallbackType directly exercises
// safeIntBinding/safeFloatBinding/safeBoolBinding's own fallback
// type-mismatch branch, Phase 50's counterpart to the "not convertible"
// branches TestPhase51Bindings_RejectUnconvertibleArguments proves
// above: like every Phase 51 overload's argument type, each of these
// three overloads declares its fallback argument as a specific static
// type (cel.IntType/cel.DoubleType/cel.BoolType, never dyn), so this
// branch is likewise unreachable through the real compiled CEL path and
// only reachable through a direct Go-level call.
func TestSafeBindings_RejectMismatchedFallbackType(t *testing.T) {
	wrongTypeFallback := types.String("not-a-number")

	if got := safeIntBinding(types.String("7"), wrongTypeFallback); !types.IsError(got) {
		t.Errorf("safeIntBinding(_, string fallback) = %v, want a types.Err", got)
	}
	if got := safeFloatBinding(types.String("7.0"), wrongTypeFallback); !types.IsError(got) {
		t.Errorf("safeFloatBinding(_, string fallback) = %v, want a types.Err", got)
	}
	if got := safeBoolBinding(types.String("true"), types.Int(1)); !types.IsError(got) {
		t.Errorf("safeBoolBinding(_, int fallback) = %v, want a types.Err", got)
	}
}

// TestPhase53Bindings_RejectUnconvertibleArguments mirrors
// TestPhase51Bindings_RejectUnconvertibleArguments/
// TestPhase52Bindings_RejectUnconvertibleArguments for Phase 53's own 16
// string/encoding/path bindings: unreachable through the real compiled
// CEL path for the same reason, reachable only through a direct
// Go-level call.
func TestPhase53Bindings_RejectUnconvertibleArguments(t *testing.T) {
	notString := types.NewDynamicList(types.DefaultTypeAdapter, []int{1, 2, 3})

	unary := map[string]func(ref.Val) ref.Val{
		"urlEncode":            urlEncodeBinding,
		"urlDecode":            urlDecodeBinding,
		"camelToSnake":         camelToSnakeBinding,
		"snakeToCamel":         snakeToCamelBinding,
		"stringToHex":          stringToHexBinding,
		"hexToString":          hexToStringBinding,
		"windowsPathToPOSIX":   windowsPathToPOSIXBinding,
		"posixPathToWindows":   posixPathToWindowsBinding,
		"octalToSymbolicPerms": octalToSymbolicPermsBinding,
		"symbolicToOctalPerms": symbolicToOctalPermsBinding,
		"humanToBytes":         humanToBytesBinding,
		"isAbsolutePath":       isAbsolutePathBinding,
		"isEmptyOrWhitespace":  isEmptyOrWhitespaceBinding,
	}
	for name, fn := range unary {
		t.Run(name, func(t *testing.T) {
			if got := fn(notString); !types.IsError(got) {
				t.Errorf("%sBinding(list) = %v, want a types.Err", name, got)
			}
		})
	}

	t.Run("bytesToHuman_notInt", func(t *testing.T) {
		if got := bytesToHumanBinding(notString); !types.IsError(got) {
			t.Errorf("bytesToHumanBinding(list) = %v, want a types.Err", got)
		}
	})
	t.Run("maskSecret_arg0", func(t *testing.T) {
		if got := maskSecretBinding(notString, types.Int(2)); !types.IsError(got) {
			t.Errorf("maskSecretBinding(list, 2) = %v, want a types.Err", got)
		}
	})
	t.Run("maskSecret_arg1", func(t *testing.T) {
		if got := maskSecretBinding(types.String("s"), notString); !types.IsError(got) {
			t.Errorf("maskSecretBinding(\"s\", list) = %v, want a types.Err", got)
		}
	})
	t.Run("regexExtract_wrongArity", func(t *testing.T) {
		if got := regexExtractBinding(types.String("a")); !types.IsError(got) {
			t.Errorf("regexExtractBinding(one arg) = %v, want a types.Err", got)
		}
	})
	t.Run("regexExtract_arg0", func(t *testing.T) {
		if got := regexExtractBinding(notString, types.String("p"), types.String("g")); !types.IsError(got) {
			t.Errorf("regexExtractBinding(list, _, _) = %v, want a types.Err", got)
		}
	})
	t.Run("regexExtract_arg1", func(t *testing.T) {
		if got := regexExtractBinding(types.String("s"), notString, types.String("g")); !types.IsError(got) {
			t.Errorf("regexExtractBinding(_, list, _) = %v, want a types.Err", got)
		}
	})
	t.Run("regexExtract_arg2", func(t *testing.T) {
		if got := regexExtractBinding(types.String("s"), types.String("p"), notString); !types.IsError(got) {
			t.Errorf("regexExtractBinding(_, _, list) = %v, want a types.Err", got)
		}
	})
}

// TestPhase54Bindings_RejectUnconvertibleArguments mirrors
// TestPhase53Bindings_RejectUnconvertibleArguments for this phase's own
// 17 bindings. notString (a CEL list) still fails string/int/map
// conversion for the same reason it did in Phase 53: common/types/
// list.go's own ConvertToType supports only ListType and TypeType, and
// celToMap/celToMapList's own map[string]any type assertion fails on a
// []any value just as readily as celToAny's own recursive walk succeeds
// converting it. notScalar (a bare CEL int) is this phase's own addition:
// celToDynList/celToMapList both fail on it because celToAny's Mapper/
// Lister type switch falls through to its scalar default branch, which
// is never []any. There is no "not convertible" value for an "any"
// parameter (filterListByKV/excludeListByKV/listContains's value):
// celToAny's own default branch (v.Value(), true) succeeds for every
// real ref.Val cel-go can produce, so that defensive branch, while
// present for the same structural-uniformity reason every other binding
// carries one, has no reachable failing input to construct here.
func TestPhase54Bindings_RejectUnconvertibleArguments(t *testing.T) {
	notString := types.NewDynamicList(types.DefaultTypeAdapter, []int{1, 2, 3})
	notScalar := types.Int(5)

	unary := map[string]func(ref.Val) ref.Val{
		"isValidFQDN":     isValidFQDNBinding,
		"isValidEmail":    isValidEmailBinding,
		"isValidUUID":     isValidUUIDBinding,
		"isValidBase64":   isValidBase64Binding,
		"isValidJSON":     isValidJSONBinding,
		"isValidYAML":     isValidYAMLBinding,
		"isValidPort":     isValidPortBinding,
		"dropEmptyValues": dropEmptyValuesBinding,
		"isValidCronExpr": isValidCronExprBinding,
	}
	for name, fn := range unary {
		t.Run(name, func(t *testing.T) {
			if got := fn(notString); !types.IsError(got) {
				t.Errorf("%sBinding(list) = %v, want a types.Err", name, got)
			}
		})
	}

	t.Run("listContains_list", func(t *testing.T) {
		if got := listContainsBinding(notScalar, types.String("v")); !types.IsError(got) {
			t.Errorf("listContainsBinding(scalar, _) = %v, want a types.Err", got)
		}
	})
	t.Run("hasMandatoryTags_m", func(t *testing.T) {
		if got := hasMandatoryTagsBinding(notString, types.NewStringList(types.DefaultTypeAdapter, []string{"a"})); !types.IsError(got) {
			t.Errorf("hasMandatoryTagsBinding(list, _) = %v, want a types.Err", got)
		}
	})
	t.Run("hasMandatoryTags_requiredKeys", func(t *testing.T) {
		if got := hasMandatoryTagsBinding(types.NewDynamicMap(types.DefaultTypeAdapter, map[string]any{}), notScalar); !types.IsError(got) {
			t.Errorf("hasMandatoryTagsBinding(_, scalar) = %v, want a types.Err", got)
		}
	})
	t.Run("listIntersect_a", func(t *testing.T) {
		if got := listIntersectBinding(notScalar, notString); !types.IsError(got) {
			t.Errorf("listIntersectBinding(scalar, _) = %v, want a types.Err", got)
		}
	})
	t.Run("listIntersect_b", func(t *testing.T) {
		if got := listIntersectBinding(notString, notScalar); !types.IsError(got) {
			t.Errorf("listIntersectBinding(_, scalar) = %v, want a types.Err", got)
		}
	})
	t.Run("listDiff_a", func(t *testing.T) {
		if got := listDiffBinding(notScalar, notString); !types.IsError(got) {
			t.Errorf("listDiffBinding(scalar, _) = %v, want a types.Err", got)
		}
	})
	t.Run("listDiff_b", func(t *testing.T) {
		if got := listDiffBinding(notString, notScalar); !types.IsError(got) {
			t.Errorf("listDiffBinding(_, scalar) = %v, want a types.Err", got)
		}
	})
	t.Run("dedupeByKey_list", func(t *testing.T) {
		if got := dedupeByKeyBinding(notString, types.String("k")); !types.IsError(got) {
			t.Errorf("dedupeByKeyBinding(list-of-ints, _) = %v, want a types.Err", got)
		}
	})
	t.Run("dedupeByKey_key", func(t *testing.T) {
		if got := dedupeByKeyBinding(types.NewDynamicList(types.DefaultTypeAdapter, []map[string]any{}), notString); !types.IsError(got) {
			t.Errorf("dedupeByKeyBinding(_, list) = %v, want a types.Err", got)
		}
	})
	t.Run("compareSemVer_a", func(t *testing.T) {
		if got := compareSemVerBinding(notString, types.String("1.0.0")); !types.IsError(got) {
			t.Errorf("compareSemVerBinding(list, _) = %v, want a types.Err", got)
		}
	})
	t.Run("compareSemVer_b", func(t *testing.T) {
		if got := compareSemVerBinding(types.String("1.0.0"), notString); !types.IsError(got) {
			t.Errorf("compareSemVerBinding(_, list) = %v, want a types.Err", got)
		}
	})

	t.Run("filterListByKV_wrongArity", func(t *testing.T) {
		if got := filterListByKVBinding(types.String("a")); !types.IsError(got) {
			t.Errorf("filterListByKVBinding(one arg) = %v, want a types.Err", got)
		}
	})
	t.Run("filterListByKV_list", func(t *testing.T) {
		if got := filterListByKVBinding(notScalar, types.String("k"), types.String("v")); !types.IsError(got) {
			t.Errorf("filterListByKVBinding(scalar, _, _) = %v, want a types.Err", got)
		}
	})
	t.Run("filterListByKV_key", func(t *testing.T) {
		emptyList := types.NewDynamicList(types.DefaultTypeAdapter, []map[string]any{})
		if got := filterListByKVBinding(emptyList, notString, types.String("v")); !types.IsError(got) {
			t.Errorf("filterListByKVBinding(_, list, _) = %v, want a types.Err", got)
		}
	})
	t.Run("excludeListByKV_wrongArity", func(t *testing.T) {
		if got := excludeListByKVBinding(types.String("a"), types.String("b"), types.String("c"), types.String("d")); !types.IsError(got) {
			t.Errorf("excludeListByKVBinding(four args) = %v, want a types.Err", got)
		}
	})
	t.Run("excludeListByKV_list", func(t *testing.T) {
		if got := excludeListByKVBinding(notScalar, types.String("k"), types.String("v")); !types.IsError(got) {
			t.Errorf("excludeListByKVBinding(scalar, _, _) = %v, want a types.Err", got)
		}
	})
	t.Run("excludeListByKV_key", func(t *testing.T) {
		emptyList := types.NewDynamicList(types.DefaultTypeAdapter, []map[string]any{})
		if got := excludeListByKVBinding(emptyList, notString, types.String("v")); !types.IsError(got) {
			t.Errorf("excludeListByKVBinding(_, list, _) = %v, want a types.Err", got)
		}
	})
}

// TestPhase55Bindings_RejectUnconvertibleArguments mirrors
// TestPhase54Bindings_RejectUnconvertibleArguments for this phase's own
// 25 bindings. Every Phase 55 parameter is string, int or
// map[string]any (unlike Phase 54, none is "any"), so every conversion-
// failure branch here has a real, constructible failing input: notString
// (a CEL list) fails StringType, IntType and MapType conversion alike
// (common/types/list.go's own ConvertToType supports only ListType and
// TypeType), so it works as the universal failing value for every string
// and int parameter below; notScalar (a bare CEL int) is used for the
// two map-typed parameters (isBusinessHour's schedule, isMaintenanceWindow's
// window), the same role it plays in TestPhase54Bindings_RejectUnconvertibleArguments.
func TestPhase55Bindings_RejectUnconvertibleArguments(t *testing.T) {
	notString := types.NewDynamicList(types.DefaultTypeAdapter, []int{1, 2, 3})
	notScalar := types.Int(5)
	validStr := types.String("2024-01-01T00:00:00Z")

	unaryInt := map[string]func(ref.Val) ref.Val{
		"epochToISO8601":   epochToISO8601Binding,
		"fileTimeToEpoch":  fileTimeToEpochBinding,
		"epochToFileTime":  epochToFileTimeBinding,
		"isLeapYear":       isLeapYearBinding,
		"humanizeDuration": humanizeDurationBinding,
	}
	for name, fn := range unaryInt {
		t.Run(name, func(t *testing.T) {
			if got := fn(notString); !types.IsError(got) {
				t.Errorf("%sBinding(list) = %v, want a types.Err", name, got)
			}
		})
	}

	unaryString := map[string]func(ref.Val) ref.Val{
		"iso8601ToEpoch": iso8601ToEpochBinding,
		"roundToHour":    roundToHourBinding,
		"startOfDay":     startOfDayBinding,
		"startOfWeek":    startOfWeekBinding,
		"startOfMonth":   startOfMonthBinding,
		"dayOfWeek":      dayOfWeekBinding,
	}
	for name, fn := range unaryString {
		t.Run(name, func(t *testing.T) {
			if got := fn(notString); !types.IsError(got) {
				t.Errorf("%sBinding(list) = %v, want a types.Err", name, got)
			}
		})
	}

	binaryStringString := map[string]func(ref.Val, ref.Val) ref.Val{
		"shiftTimezone":      shiftTimezoneBinding,
		"isPast":             isPastBinding,
		"isFuture":           isFutureBinding,
		"uptimeFromBootTime": uptimeFromBootTimeBinding,
		"cronNextRun":        cronNextRunBinding,
		"cronPreviousRun":    cronPreviousRunBinding,
	}
	for name, fn := range binaryStringString {
		t.Run(name+"_arg0", func(t *testing.T) {
			if got := fn(notString, validStr); !types.IsError(got) {
				t.Errorf("%sBinding(list, _) = %v, want a types.Err", name, got)
			}
		})
		t.Run(name+"_arg1", func(t *testing.T) {
			if got := fn(validStr, notString); !types.IsError(got) {
				t.Errorf("%sBinding(_, list) = %v, want a types.Err", name, got)
			}
		})
	}

	binaryStringInt := map[string]func(ref.Val, ref.Val) ref.Val{
		"addSeconds":         addSecondsBinding,
		"bootTimeFromUptime": bootTimeFromUptimeBinding,
	}
	for name, fn := range binaryStringInt {
		t.Run(name+"_arg0", func(t *testing.T) {
			if got := fn(notString, types.Int(0)); !types.IsError(got) {
				t.Errorf("%sBinding(list, _) = %v, want a types.Err", name, got)
			}
		})
		t.Run(name+"_arg1", func(t *testing.T) {
			if got := fn(validStr, notString); !types.IsError(got) {
				t.Errorf("%sBinding(_, list) = %v, want a types.Err", name, got)
			}
		})
	}

	binaryMapString := map[string]func(ref.Val, ref.Val) ref.Val{
		"isBusinessHour":      isBusinessHourBinding,
		"isMaintenanceWindow": isMaintenanceWindowBinding,
	}
	for name, fn := range binaryMapString {
		t.Run(name+"_map", func(t *testing.T) {
			if got := fn(notScalar, validStr); !types.IsError(got) {
				t.Errorf("%sBinding(scalar, _) = %v, want a types.Err", name, got)
			}
		})
		t.Run(name+"_iso", func(t *testing.T) {
			validMap := types.NewDynamicMap(types.DefaultTypeAdapter, map[string]any{})
			if got := fn(validMap, notString); !types.IsError(got) {
				t.Errorf("%sBinding(_, list) = %v, want a types.Err", name, got)
			}
		})
	}

	ternaryStringStringInt := map[string]func(...ref.Val) ref.Val{
		"deltaSeconds":     deltaSecondsBinding,
		"deltaDays":        deltaDaysBinding,
		"isOlderThan":      isOlderThanBinding,
		"isExpiringWithin": isExpiringWithinBinding,
	}
	for name, fn := range ternaryStringStringInt {
		t.Run(name+"_wrongArity", func(t *testing.T) {
			if got := fn(validStr, validStr); !types.IsError(got) {
				t.Errorf("%sBinding(two args) = %v, want a types.Err", name, got)
			}
		})
		t.Run(name+"_arg0", func(t *testing.T) {
			if got := fn(notString, validStr, types.Int(0)); !types.IsError(got) {
				t.Errorf("%sBinding(list, _, _) = %v, want a types.Err", name, got)
			}
		})
		t.Run(name+"_arg1", func(t *testing.T) {
			if got := fn(validStr, notString, types.Int(0)); !types.IsError(got) {
				t.Errorf("%sBinding(_, list, _) = %v, want a types.Err", name, got)
			}
		})
		t.Run(name+"_arg2", func(t *testing.T) {
			if got := fn(validStr, validStr, notString); !types.IsError(got) {
				t.Errorf("%sBinding(_, _, list) = %v, want a types.Err", name, got)
			}
		})
	}
}
