package filters_test

import (
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/filters"
)

func FuzzInterfaceShortForm(f *testing.F) {
	seeds := []string{"GigabitEthernet0/1", "Gi0/1", "Port-channel1", "garbage", "", "Vlan100", "0/1", "Gi", "Gi0/0/0/0/0/0/0"}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, name string) {
		filters.InterfaceShortForm(name)
		filters.InterfaceLongForm(name)
	})
}
