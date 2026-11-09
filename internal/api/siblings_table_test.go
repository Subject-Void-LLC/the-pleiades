// Package api_test: the fixed segments the API's own route table registers
// beside a parameter.
package api_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/api"
	"github.com/Subject-Void-LLC/the-pleiades/internal/apispec"
)

// TestStaticSiblings_OfTheAPIsOwnRouteTable reads apispec's route table, the
// one NewRouter registers, and pins what it finds: "preview" beside
// /schedules/{id}, and nothing beside a device's {name}.
func TestStaticSiblings_OfTheAPIsOwnRouteTable(t *testing.T) {
	var patterns []string
	for _, e := range apispec.Endpoints {
		patterns = append(patterns, api.APIVersionPrefix+e.Pattern)
	}
	statics := api.StaticSiblingsForTest(patterns)
	if !statics[api.APIVersionPrefix+"/schedules/{id}"]["id"]["preview"] {
		t.Errorf("staticSiblings did not find /schedules/preview beside /schedules/{id}: %v", statics[api.APIVersionPrefix+"/schedules/{id}"])
	}
	for _, p := range []string{"/inventory/devices/{name}", "/inventory/devices/{name}/facts/{key}", "/inventory/devices/{name}/capabilities/{capability}"} {
		if got := statics[api.APIVersionPrefix+p]; len(got) != 0 {
			t.Errorf("%s has fixed siblings %v, so a device or a key of that name would be shadowed", p, got)
		}
	}
}
