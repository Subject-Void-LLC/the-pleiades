package topology

import (
	"testing"
	"time"
)

// TestRetentionWouldDiscard covers the one-way door's decision directly.
//
// The refusal it drives cannot be reached from a container test: the
// shortest retention any legal budget derives is several hours, so a
// freshly published message can never be "too old". Separating the
// decision from the round trip is what makes it verifiable at all.
func TestRetentionWouldDiscard(t *testing.T) {
	const day = 24 * time.Hour

	tests := []struct {
		name    string
		live    time.Duration
		desired time.Duration
		msgs    uint64
		oldest  time.Duration
		want    bool
	}{
		{"raising retention never discards", day, 7 * day, 1000, 5 * day, false},
		{"identical retention never discards", day, day, 1000, 5 * day, false},
		{"an empty stream has nothing to lose", 7 * day, time.Hour, 0, 0, false},
		{"a young stream keeps everything it holds", 7 * day, day, 1000, time.Hour, false},
		{"exactly at the new limit is kept", 7 * day, day, 1000, day, false},
		{"older than the new limit is discarded", 7 * day, day, 1000, day + time.Second, true},
		{"much older is discarded", 7 * day, time.Hour, 41823, 5 * day, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := retentionWouldDiscard(tt.live, tt.desired, tt.msgs, tt.oldest); got != tt.want {
				t.Fatalf("retentionWouldDiscard(live=%v, desired=%v, msgs=%d, oldest=%v) = %v, want %v",
					tt.live, tt.desired, tt.msgs, tt.oldest, got, tt.want)
			}
		})
	}
}
