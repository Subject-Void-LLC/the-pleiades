// What a method holds for a person's decision (human in the loop), and the
// run's way to put it before one.
package sdk

import (
	"context"
	"errors"
)

// ReviewStatus is where a review stands.
type ReviewStatus string

// The review statuses. Only a person moves a review out of pending.
const (
	ReviewPending  ReviewStatus = "pending"
	ReviewApproved ReviewStatus = "approved"
	ReviewDenied   ReviewStatus = "denied"
)

// Review is a result held for a person's decision: which request (Key,
// the SHA-256 of the request as asked, so asking the same thing again finds
// it), what was answered, why it was held, and what was decided.
type Review struct {
	Key      string   `json:"key"`
	Method   string   `json:"method"`
	Provider string   `json:"provider"`
	Model    string   `json:"model"`
	Reasons  []string `json:"reasons"`
	// Answer is the result held. AnswerSHA256 is its digest, and
	// DecidedSHA256 the digest of the answer the person was shown: an
	// approval holds for those bytes and no others.
	Answer        string         `json:"answer"`
	AnswerSHA256  string         `json:"answer_sha256"`
	Usage         map[string]any `json:"usage,omitempty"`
	Status        ReviewStatus   `json:"status"`
	HeldAt        string         `json:"held_at"`
	DecidedBy     string         `json:"decided_by,omitempty"`
	DecidedAt     string         `json:"decided_at,omitempty"`
	DecidedSHA256 string         `json:"decided_sha256,omitempty"`
	Comment       string         `json:"comment,omitempty"`
}

// Reviewer is a run's way to hold a result for a person. The Crawl tier
// implements it over the project (ai/review) and the terminal; a context
// that does not implement it has no person to ask, and a method must then
// fail closed rather than release what it would have held. The Walk tier
// does not implement it until the Controller can pause a run for an
// approval (Phase 27, internal/engine's pause.go).
type Reviewer interface {
	// Decided returns the review of key, false when there is none. It
	// only reads, so a check may call it.
	Decided(key string) (Review, bool, error)
	// Hold puts r before a person: one at the terminal is asked now; with
	// nobody there it is recorded pending, for `pleiades ai review`. It
	// returns the review as it then stands.
	Hold(ctx context.Context, r Review) (Review, error)
}

// ErrHeld is what every error for a result held for a person wraps, so a
// run can tell a held task from a failed one.
var ErrHeld = errors.New("held for a person's decision")

// HeldError is a result held for review: the review's key, and what to do.
type HeldError struct {
	Key    string
	Reason string
}

func (e *HeldError) Error() string { return "held for review: " + e.Reason }

// Is makes errors.Is(err, ErrHeld) true for a HeldError.
func (e *HeldError) Is(target error) bool { return target == ErrHeld }
