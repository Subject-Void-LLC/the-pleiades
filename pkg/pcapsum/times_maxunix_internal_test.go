// Holding maxUnix, a constant so it compares with uint64 and int64 alike, to
// the date it stands for.
package pcapsum

import (
	"testing"
	"time"
)

// TestMaxUnix_IsTheLastWritableSecond holds maxUnix to the last second
// before year 10000, which RFC 3339 cannot write.
func TestMaxUnix_IsTheLastWritableSecond(t *testing.T) {
	if want := time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC).Unix() - 1; maxUnix != want {
		t.Fatalf("maxUnix = %d, want %d", int64(maxUnix), want)
	}
	if got := time.Unix(maxUnix, 0).UTC().Year(); got != 9999 {
		t.Fatalf("maxUnix falls in year %d, want 9999", got)
	}
}
