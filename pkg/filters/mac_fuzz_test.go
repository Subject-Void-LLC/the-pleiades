package filters_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

func FuzzMACToCiscoFormat(f *testing.F) {
	seeds := []string{"00:00:5e:00:53:01", "00-00-5e-00-53-01", "0000.5e00.5301", "garbage", "", "02:00:5e:10:00:00:00:01"}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, mac string) {
		filters.MACToCiscoFormat(mac)
		filters.MACToColonFormat(mac)
		filters.MACToWindowsFormat(mac)
		filters.MACOUI(mac)
	})
}
