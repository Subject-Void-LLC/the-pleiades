// Package aireview keeps the reviews an ai method held for a person's
// decision, in the project's ai/review directory, one JSON file per
// request key. The engine creates them (a held answer) and the CLI decides
// them (`pleiades ai review approve|deny`); a runbook task can do neither,
// since its writes reach only ai/usage and ai/bench (sdk.AIProject).
//
// Every read holds a file to itself: its name is its key, its answer
// hashes to the digest it records, and an approval holds for the answer it
// was made on. A file that fails any of these is refused, never trusted.
package aireview

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// Dir is where reviews are kept, relative to the project.
const Dir = "ai/review"

// maxReviewBytes bounds a review file: an answer of up to 16 MiB, escaped.
const maxReviewBytes = 40 << 20

// keyForm is a request key: a SHA-256 in lower-case hex.
var keyForm = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ValidKey reports whether key is a request key.
func ValidKey(key string) bool { return keyForm.MatchString(key) }

// AnswerSHA256 is an answer's digest, in hex.
func AnswerSHA256(answer string) string {
	sum := sha256.Sum256([]byte(answer))
	return hex.EncodeToString(sum[:])
}

// openDir opens the project's review directory, refusing a link standing
// in for it or for ai, and creating both when create is set.
func openDir(project string, create bool) (*os.Root, error) {
	root, err := os.OpenRoot(project)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	for _, d := range []string{"ai", Dir} {
		info, err := root.Lstat(d)
		switch {
		case errors.Is(err, fs.ErrNotExist) && create:
			if err := root.Mkdir(d, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
				return nil, err
			}
		case errors.Is(err, fs.ErrNotExist):
			return nil, err
		case err != nil:
			return nil, err
		case !info.IsDir():
			return nil, fmt.Errorf("%s is not a directory (a link is refused)", d)
		}
	}
	return root.OpenRoot(Dir)
}

// Load reads the review of key, false when there is none.
func Load(project, key string) (sdk.Review, bool, error) {
	if !ValidKey(key) {
		return sdk.Review{}, false, fmt.Errorf("%q is not a review key", key)
	}
	dir, err := openDir(project, false)
	if errors.Is(err, fs.ErrNotExist) {
		return sdk.Review{}, false, nil
	}
	if err != nil {
		return sdk.Review{}, false, err
	}
	defer func() { _ = dir.Close() }()
	f, err := dir.Open(key + ".json")
	if errors.Is(err, fs.ErrNotExist) {
		return sdk.Review{}, false, nil
	}
	if err != nil {
		return sdk.Review{}, false, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxReviewBytes+1))
	if err != nil {
		return sdk.Review{}, false, err
	}
	if len(data) > maxReviewBytes {
		return sdk.Review{}, false, fmt.Errorf("review %s is over %d bytes", key, maxReviewBytes)
	}
	var r sdk.Review
	if err := json.Unmarshal(data, &r); err != nil {
		return sdk.Review{}, false, fmt.Errorf("review %s: %w", key, err)
	}
	if err := check(key, r); err != nil {
		return sdk.Review{}, false, err
	}
	return r, true, nil
}

// check holds a review to itself.
func check(key string, r sdk.Review) error {
	switch {
	case r.Key != key:
		return fmt.Errorf("review %s holds key %s: it was renamed or edited", key, r.Key)
	case AnswerSHA256(r.Answer) != r.AnswerSHA256:
		return fmt.Errorf("review %s's answer no longer matches its digest: it was edited after it was held", key)
	case r.Status != sdk.ReviewPending && r.Status != sdk.ReviewApproved && r.Status != sdk.ReviewDenied:
		return fmt.Errorf("review %s has status %q", key, r.Status)
	case r.Status == sdk.ReviewApproved && r.DecidedSHA256 != r.AnswerSHA256:
		return fmt.Errorf("review %s was approved for another answer than the one it holds", key)
	}
	return nil
}

// Create records r, refusing a second review of the same key: the first
// one stands, and is returned.
func Create(project string, r sdk.Review) (sdk.Review, error) {
	if !ValidKey(r.Key) {
		return sdk.Review{}, fmt.Errorf("%q is not a review key", r.Key)
	}
	r.AnswerSHA256 = AnswerSHA256(r.Answer)
	if err := check(r.Key, r); err != nil {
		return sdk.Review{}, err
	}
	dir, err := openDir(project, true)
	if err != nil {
		return sdk.Review{}, err
	}
	defer func() { _ = dir.Close() }()
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return sdk.Review{}, err
	}
	f, err := dir.OpenFile(r.Key+".json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		existing, _, err := Load(project, r.Key)
		return existing, err
	}
	if err != nil {
		return sdk.Review{}, err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		return sdk.Review{}, err
	}
	return r, f.Close()
}

// Decide records a person's decision on a pending review: status approved
// or denied, by whom, when, with a comment, for the answer whose digest
// shown is (the one the person read). A review already decided, or one
// whose answer is not the one shown, is refused.
func Decide(project, key string, status sdk.ReviewStatus, by, at, comment, shown string) (sdk.Review, error) {
	if status != sdk.ReviewApproved && status != sdk.ReviewDenied {
		return sdk.Review{}, fmt.Errorf("a decision is approved or denied, not %q", status)
	}
	r, ok, err := Load(project, key)
	switch {
	case err != nil:
		return sdk.Review{}, err
	case !ok:
		return sdk.Review{}, fmt.Errorf("there is no review %s", key)
	case r.Status != sdk.ReviewPending:
		return sdk.Review{}, fmt.Errorf("review %s was already %s by %s at %s", key, r.Status, r.DecidedBy, r.DecidedAt)
	case shown != r.AnswerSHA256:
		return sdk.Review{}, fmt.Errorf("review %s's answer is not the one shown", key)
	}
	r.Status, r.DecidedBy, r.DecidedAt, r.Comment, r.DecidedSHA256 = status, by, at, comment, shown
	return r, replace(project, r)
}

// Record writes a review decided where it was held (at the terminal), so
// a later run asking the same thing reuses the decision.
func Record(project string, r sdk.Review) error {
	r.AnswerSHA256 = AnswerSHA256(r.Answer)
	if err := check(r.Key, r); err != nil {
		return err
	}
	if _, err := Create(project, sdk.Review{Key: r.Key, Method: r.Method, Provider: r.Provider, Model: r.Model, Reasons: r.Reasons, Answer: r.Answer, Usage: r.Usage, Status: sdk.ReviewPending, HeldAt: r.HeldAt}); err != nil {
		return err
	}
	return replace(project, r)
}

// replace writes r over its file, whole, through a rename in the
// directory, so a reader sees the old review or the new one.
func replace(project string, r sdk.Review) error {
	dir, err := openDir(project, true)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp := "." + r.Key + ".tmp"
	f, err := dir.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return dir.Rename(tmp, r.Key+".json")
}

// List reads every review, newest held first, refusing none: a file that
// fails its own checks is listed by key with its error.
func List(project string) ([]sdk.Review, map[string]error, error) {
	dir, err := openDir(project, false)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = dir.Close() }()
	entries, err := fs.ReadDir(dir.FS(), ".")
	if err != nil {
		return nil, nil, err
	}
	var out []sdk.Review
	broken := map[string]error{}
	for _, e := range entries {
		key, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !ValidKey(key) || !e.Type().IsRegular() {
			continue
		}
		r, _, err := Load(project, key)
		if err != nil {
			broken[key] = err
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].HeldAt > out[j].HeldAt })
	return out, broken, nil
}

// File is a review's path relative to the project.
func File(key string) string { return path.Join(Dir, key+".json") }
