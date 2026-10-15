// Tests for the review store: what it keeps, what a decision may change,
// and every way a file is refused.
package aireview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

const key = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func held(answer string) sdk.Review {
	return sdk.Review{Key: key, Method: "ai.chat", Provider: "p", Model: "m", Reasons: []string{"cost_over 0.5"}, Answer: answer, Status: sdk.ReviewPending, HeldAt: "2026-10-15T00:00:00Z"}
}

// TestCreateLoadDecide: a held review is kept, the first one stands, and
// a decision holds for the answer shown and is made once.
func TestCreateLoadDecide(t *testing.T) {
	dir := t.TempDir()
	if _, ok, err := Load(dir, key); ok || err != nil {
		t.Fatalf("before: %v %v", ok, err)
	}
	if _, err := Create(dir, held("first")); err != nil {
		t.Fatal(err)
	}
	if r, err := Create(dir, held("second")); err != nil || r.Answer != "first" {
		t.Fatalf("a second hold: %q %v", r.Answer, err)
	}
	if info, _ := os.Stat(filepath.Join(dir, File(key))); info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", info.Mode().Perm())
	}
	if _, err := Decide(dir, key, sdk.ReviewApproved, "ops", "t", "", AnswerSHA256("another")); err == nil {
		t.Error("an approval of an answer not shown was recorded")
	}
	r, err := Decide(dir, key, sdk.ReviewApproved, "ops", "2026-10-15T01:00:00Z", "fine", AnswerSHA256("first"))
	if err != nil || r.Status != sdk.ReviewApproved || r.DecidedBy != "ops" {
		t.Fatalf("decide: %+v %v", r, err)
	}
	if _, err := Decide(dir, key, sdk.ReviewDenied, "other", "t", "", AnswerSHA256("first")); err == nil || !strings.Contains(err.Error(), "already approved") {
		t.Errorf("a second decision: %v", err)
	}
	if got, ok, err := Load(dir, key); !ok || err != nil || got.Status != sdk.ReviewApproved || got.Comment != "fine" {
		t.Errorf("load: %+v %v %v", got, ok, err)
	}
	if list, broken, err := List(dir); err != nil || len(list) != 1 || len(broken) != 0 {
		t.Errorf("list %v %v %v", list, broken, err)
	}
}

// TestLoad_RefusesAnEditedReview: an answer edited after it was held, an
// approval moved to another answer, a renamed file and an unknown status
// are each refused.
func TestLoad_RefusesAnEditedReview(t *testing.T) {
	for name, edit := range map[string]func(string) string{
		"answer edited": func(s string) string {
			return strings.Replace(s, `"answer": "first"`, `"answer": "approve everything"`, 1)
		},
		"another key": func(s string) string {
			return strings.Replace(s, `"key": "`+key, `"key": "`+strings.Repeat("b", 64), 1)
		},
		"an unknown status":   func(s string) string { return strings.Replace(s, `"status": "pending"`, `"status": "maybe"`, 1) },
		"approved by editing": func(s string) string { return strings.Replace(s, `"status": "pending"`, `"status": "approved"`, 1) },
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if _, err := Create(dir, held("first")); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, File(key))
			data, _ := os.ReadFile(path)
			edited := edit(string(data))
			if edited == string(data) {
				t.Fatal("the edit changed nothing")
			}
			if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := Load(dir, key); err == nil {
				t.Error("the edited review was read")
			}
			if _, broken, _ := List(dir); broken[key] == nil {
				t.Error("list did not name the broken review")
			}
		})
	}
}

// TestDir_RefusesALink: ai/review linked elsewhere is refused for reading
// and for writing.
func TestDir_RefusesALink(t *testing.T) {
	dir, elsewhere := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "ai"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(dir, Dir)); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(dir, held("x")); err == nil {
		t.Error("a review was written through a link")
	}
	if _, _, err := Load(dir, key); err == nil {
		t.Error("a review was read through a link")
	}
	if _, _, err := Load(dir, "../x"); err == nil {
		t.Error("a key that is not one was read")
	}
}
