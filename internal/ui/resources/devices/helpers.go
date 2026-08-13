package devices

import (
	"strconv"
	"strings"

	pkginventory "github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/google/uuid"
)

// deviceTyped is the structural check for an item that reports its own
// classification. pkg/inventory.InventoryItem deliberately does not carry
// it -- classification is the factory's business -- so this is the same
// structural-matching idiom pkg/capability uses rather than a nominal
// method added to the domain interface.
type deviceTyped interface{ DeviceType() string }

func deviceType(item pkginventory.InventoryItem) string {
	if t, ok := item.(deviceTyped); ok {
		return t.DeviceType()
	}
	return ""
}

func joinTags(tags []pkginventory.Tag) string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		out = append(out, string(t))
	}
	return strings.Join(out, ", ")
}

func itoa(v uint64) string { return strconv.FormatUint(v, 10) }

// newDeviceID mints the identifier a created device is stored under,
// matching the schema's own default: a UUIDv7, so identifiers sort in
// creation order and the keyset paging the list relies on is chronological.
func newDeviceID() pkginventory.DeviceID {
	if id, err := uuid.NewV7(); err == nil {
		return pkginventory.DeviceID(id.String())
	}
	return pkginventory.DeviceID(uuid.New().String())
}
