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
