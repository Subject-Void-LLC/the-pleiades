package inventory_test

import (
	"testing"

	"github.com/SubjectVoidLLC/the-pleiades/pkg/inventory"
)

func TestPropertiesString(t *testing.T) {
	cases := []struct {
		name   string
		raw    map[string]inventory.PropertyValue
		key    string
		want   string
		wantOK bool
	}{
		{"present", map[string]inventory.PropertyValue{"host": "10.0.0.1"}, "host", "10.0.0.1", true},
		{"missing key", map[string]inventory.PropertyValue{}, "host", "", false},
		{"wrong type", map[string]inventory.PropertyValue{"host": 123}, "host", "", false},
		{"nil map", nil, "host", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := inventory.NewProperties(tc.raw)
			got, ok := p.String(tc.key)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("String(%q) = (%q, %v), want (%q, %v)", tc.key, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestPropertiesInt(t *testing.T) {
	cases := []struct {
		name   string
		raw    map[string]inventory.PropertyValue
		key    string
		want   int
		wantOK bool
	}{
		{"go int", map[string]inventory.PropertyValue{"port": 22}, "port", 22, true},
		{"json float64", map[string]inventory.PropertyValue{"port": float64(22)}, "port", 22, true},
		{"missing key", map[string]inventory.PropertyValue{}, "port", 0, false},
		{"wrong type", map[string]inventory.PropertyValue{"port": "not-an-int"}, "port", 0, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := inventory.NewProperties(tc.raw)
			got, ok := p.Int(tc.key)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("Int(%q) = (%d, %v), want (%d, %v)", tc.key, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestPropertiesBool(t *testing.T) {
	cases := []struct {
		name   string
		raw    map[string]inventory.PropertyValue
		key    string
		want   bool
		wantOK bool
	}{
		{"present true", map[string]inventory.PropertyValue{"netconf": true}, "netconf", true, true},
		{"present false", map[string]inventory.PropertyValue{"netconf": false}, "netconf", false, true},
		{"missing key", map[string]inventory.PropertyValue{}, "netconf", false, false},
		{"wrong type", map[string]inventory.PropertyValue{"netconf": "yes"}, "netconf", false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := inventory.NewProperties(tc.raw)
			got, ok := p.Bool(tc.key)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("Bool(%q) = (%v, %v), want (%v, %v)", tc.key, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestPropertiesLen(t *testing.T) {
	p := inventory.NewProperties(map[string]inventory.PropertyValue{"a": 1, "b": 2})
	if p.Len() != 2 {
		t.Errorf("expected Len 2, got %d", p.Len())
	}
	if inventory.NewProperties(nil).Len() != 0 {
		t.Error("expected Len 0 for a nil map")
	}
}
