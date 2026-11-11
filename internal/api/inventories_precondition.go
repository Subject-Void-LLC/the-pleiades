// This file is the inventory's entity tag and the If-Match precondition a
// PATCH is held to.
//
// An inventory's ETag is its version and a digest of what it holds, quoted:
// "0.<digest>" when created, the version one more after each committed update
// (inventory.Set.Version). GET, POST and PATCH send it, so a client reads the
// inventory, changes it and sends the ETag back as If-Match. A PATCH naming
// groups or devices replaces that list whole, so a client that read the
// inventory before another update removed a device would put the device back,
// which on a shared inventory re-grants it to every team it is shared with.
// Such a PATCH therefore needs If-Match (428 without one), and any PATCH that
// sends one is refused when it is not the current ETag (412). A PATCH naming
// neither list folds over the stored inventory under the row's lock and
// cannot revert anything, so it needs none.
//
// The digest is there for a write the version does not count. A build from
// before the version column keeps serving the migrated schema during a
// rolling upgrade or after a rollback (internal/ent/migrate/compat.go), and
// its writes leave the version where it was; they still change what the
// inventory holds, and so its ETag. A write that changes nothing is still
// counted by the version on this build.
//
// The comparison runs inside the store's fold, under the lock, so it is made
// against the inventory the write will replace, and a missing inventory is a
// 404 before any precondition is judged. Neither refusal repeats the header:
// its text is fixed.
package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/inventory"
)

// errInventoryPreconditionRequired is the fold's refusal of a PATCH that
// names groups or devices and sends no If-Match, answered 428.
var errInventoryPreconditionRequired = errors.New("api: an inventory membership PATCH needs If-Match")

// inventoryPreconditionRequiredText is the 428 answer.
const inventoryPreconditionRequiredText = "a PATCH that names groups or devices needs an If-Match header " +
	"holding the inventory's ETag: read the inventory and send the ETag it answered with"

// inventoryPreconditionFailedText is the 412 answer. It is the same whether
// the tag was stale, malformed or "*", and never repeats what was sent.
const inventoryPreconditionFailedText = "If-Match does not hold the inventory's current ETag: " +
	"it has changed since it was read, so read it again and resend the change"

// inventoryETag is the strong entity tag of an inventory as stored: its
// version, a dot, and the first 16 bytes of a SHA-256 over its name,
// description and sorted member ids, in hex. The digest says nothing a
// reader of the inventory cannot already read.
func inventoryETag(set inventory.Set) string {
	groups, devices := slices.Clone(set.GroupIDs), slices.Clone(set.DeviceIDs)
	slices.Sort(groups)
	slices.Sort(devices)
	// Each part is written with its length first, so no two inventories'
	// parts run together into the same bytes.
	h := sha256.New()
	for _, part := range []string{set.Name, set.Description, joinInts(groups), joinInts(devices)} {
		h.Write([]byte(strconv.Itoa(len(part)) + ":" + part + ";"))
	}
	return `"` + strconv.FormatUint(set.Version, 10) + "." + hex.EncodeToString(h.Sum(nil)[:16]) + `"`
}

// joinInts renders ids as comma-separated decimal.
func joinInts(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.Itoa(id)
	}
	return strings.Join(parts, ",")
}

// ifMatch is what a request's If-Match header says.
type ifMatch struct {
	// present reports that the request carried an If-Match header at all.
	present bool

	// tag is the header's one value, trimmed, when it carried exactly one
	// header line. It matches only when it is the current ETag exactly, which
	// is RFC 9110's strong comparison: a list, a weak tag and "*" never equal
	// any tag this API sends. "*" would match any inventory that exists,
	// which is no protection against a lost update.
	tag string
}

// readIfMatch reads r's If-Match header.
func readIfMatch(r *http.Request) ifMatch {
	values := r.Header.Values("If-Match")
	switch len(values) {
	case 0:
		return ifMatch{}
	case 1:
		return ifMatch{present: true, tag: strings.TrimSpace(values[0])}
	default:
		// Two header lines are a list, which this API never sends.
		return ifMatch{present: true}
	}
}

// check holds the stored inventory to the precondition: a header that is
// present must be its current ETag, and a PATCH that names a member list must
// send one. It returns inventory.ErrVersionStale or
// errInventoryPreconditionRequired, or nil.
func (m ifMatch) check(stored inventory.Set, namesMembers bool) error {
	switch {
	case m.present && (m.tag == "" || m.tag != inventoryETag(stored)):
		return inventory.ErrVersionStale
	case !m.present && namesMembers:
		return errInventoryPreconditionRequired
	}
	return nil
}
