package filters_test

import (
	"reflect"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

func TestDropEmptyValues(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want map[string]any
	}{
		{
			name: "drops_nil_empty_string_empty_list_empty_map",
			in: map[string]any{
				"a": "kept",
				"b": "",
				"c": nil,
				"d": []any{},
				"e": map[string]any{},
				"f": []any{1},
				"g": 0,
				"h": false,
			},
			want: map[string]any{"a": "kept", "f": []any{1}, "g": 0, "h": false},
		},
		{name: "empty_map", in: map[string]any{}, want: map[string]any{}},
		{name: "nil_map", in: nil, want: map[string]any{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filters.DropEmptyValues(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("DropEmptyValues(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestFilterListByKV(t *testing.T) {
	list := []map[string]any{
		{"name": "a", "role": "web"},
		{"name": "b", "role": "db"},
		{"name": "c", "role": "web"},
		{"name": "d"},
	}
	got := filters.FilterListByKV(list, "role", "web")
	want := []map[string]any{
		{"name": "a", "role": "web"},
		{"name": "c", "role": "web"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FilterListByKV = %v, want %v", got, want)
	}

	if got := filters.FilterListByKV(list, "role", "nonexistent"); len(got) != 0 {
		t.Errorf("FilterListByKV with no matches = %v, want empty", got)
	}

	// A device fact decoded through JSON (float64) must still match a
	// runbook literal typed as a CEL int (int64), the whole reason
	// valuesEqual exists.
	numeric := []map[string]any{{"port": float64(22)}, {"port": float64(80)}}
	if got := filters.FilterListByKV(numeric, "port", int64(22)); len(got) != 1 {
		t.Errorf("FilterListByKV numeric cross-type match = %v, want 1 element", got)
	}
}

func TestExcludeListByKV(t *testing.T) {
	list := []map[string]any{
		{"name": "a", "role": "web"},
		{"name": "b", "role": "db"},
		{"name": "c", "role": "web"},
		{"name": "d"},
	}
	got := filters.ExcludeListByKV(list, "role", "web")
	want := []map[string]any{
		{"name": "b", "role": "db"},
		{"name": "d"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExcludeListByKV = %v, want %v", got, want)
	}

	// FilterListByKV and ExcludeListByKV on the same key/value must
	// partition list with no overlap and no gap.
	filtered := filters.FilterListByKV(list, "role", "web")
	excluded := filters.ExcludeListByKV(list, "role", "web")
	if len(filtered)+len(excluded) != len(list) {
		t.Errorf("FilterListByKV(%d) + ExcludeListByKV(%d) != len(list)(%d)", len(filtered), len(excluded), len(list))
	}
}

func TestListContains(t *testing.T) {
	cases := []struct {
		name  string
		list  []any
		value any
		want  bool
	}{
		{"string_present", []any{"a", "b", "c"}, "b", true},
		{"string_absent", []any{"a", "b", "c"}, "z", false},
		{"empty_list", []any{}, "a", false},
		{"numeric_cross_type", []any{float64(1), float64(2)}, int64(2), true},
		{"plain_int_list_elements", []any{1, 2, 3}, int64(2), true},
		{"nil_value", []any{"a", nil}, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := filters.ListContains(tc.list, tc.value); got != tc.want {
				t.Errorf("ListContains(%v, %v) = %v, want %v", tc.list, tc.value, got, tc.want)
			}
		})
	}
}

func TestHasMandatoryTags(t *testing.T) {
	m := map[string]any{"env": "prod", "owner": "team-a"}

	got := filters.HasMandatoryTags(m, []string{"env", "owner", "cost-center"})
	want := []string{"cost-center"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("HasMandatoryTags missing = %v, want %v", got, want)
	}

	if got := filters.HasMandatoryTags(m, []string{"env", "owner"}); len(got) != 0 {
		t.Errorf("HasMandatoryTags all present = %v, want empty", got)
	}

	// Present but empty still counts as present: HasMandatoryTags checks
	// key existence, not DropEmptyValues' notion of emptiness.
	withEmpty := map[string]any{"env": ""}
	if got := filters.HasMandatoryTags(withEmpty, []string{"env"}); len(got) != 0 {
		t.Errorf("HasMandatoryTags with present-but-empty value = %v, want empty (present)", got)
	}
}

func TestListIntersect(t *testing.T) {
	cases := []struct {
		name string
		a, b []any
		want []any
	}{
		{"basic", []any{"a", "b", "c"}, []any{"b", "c", "d"}, []any{"b", "c"}},
		{"no_overlap", []any{"a"}, []any{"b"}, nil},
		{"preserves_multiplicity", []any{"a", "a", "b"}, []any{"a"}, []any{"a", "a"}},
		{"empty_a", []any{}, []any{"a"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filters.ListIntersect(tc.a, tc.b)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ListIntersect(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestListDiff(t *testing.T) {
	cases := []struct {
		name string
		a, b []any
		want []any
	}{
		{"basic", []any{"a", "b", "c"}, []any{"b"}, []any{"a", "c"}},
		{"full_overlap", []any{"a", "b"}, []any{"a", "b"}, nil},
		{"preserves_multiplicity", []any{"a", "a", "b"}, []any{"b"}, []any{"a", "a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filters.ListDiff(tc.a, tc.b)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ListDiff(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}

	// ListIntersect(a, b) and ListDiff(a, b) must exactly partition a.
	a := []any{"a", "b", "c", "a"}
	b := []any{"b", "c"}
	inter := filters.ListIntersect(a, b)
	diff := filters.ListDiff(a, b)
	if len(inter)+len(diff) != len(a) {
		t.Errorf("ListIntersect(%d) + ListDiff(%d) != len(a)(%d)", len(inter), len(diff), len(a))
	}
}

func TestDedupeByKey(t *testing.T) {
	list := []map[string]any{
		{"id": "1", "v": "first"},
		{"id": "2", "v": "second"},
		{"id": "1", "v": "duplicate"},
		{"note": "no id field"},
	}
	got := filters.DedupeByKey(list, "id")
	want := []map[string]any{
		{"id": "1", "v": "first"},
		{"id": "2", "v": "second"},
		{"note": "no id field"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DedupeByKey = %v, want %v", got, want)
	}

	// Multiple maps missing the key are never deduplicated against each
	// other: there is no value to compare.
	noKey := []map[string]any{{"other": "a"}, {"other": "b"}}
	if got := filters.DedupeByKey(noKey, "id"); len(got) != 2 {
		t.Errorf("DedupeByKey with no key present = %v, want both kept", got)
	}
}
