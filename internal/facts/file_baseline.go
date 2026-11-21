// Package facts: the Crawl CLI's baselines (Phase 41d).
//
// <device>.baselines.jsonl is append-only: one line per baseline, each one
// whole baseline sealed under the store's line key (SealLine, role
// RoleBaseline), bound to the device, the role and the line's byte offset
// like every other line the store writes, so a line copied to another
// device's file or moved to another place does not open. A baseline is never
// edited: taking one under a name the device already has is refused, and
// leaves every line as it was. A line a crash left unfinished is ignored by
// readers and cut off by the next writer, as the other files' are.
package facts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

// Errors a baseline's store reports.
var (
	// ErrBaselineExists marks a baseline name the device already has.
	ErrBaselineExists = errors.New("facts: the device already has a baseline of that name, and a baseline is never replaced")
	// ErrNoBaseline marks a device with no baseline, or none of the name asked.
	ErrNoBaseline = errors.New("facts: no such baseline")
	// ErrNeverGathered marks a device whose facts were never stored, so
	// nothing can be taken or compared.
	ErrNeverGathered = errors.New("facts: the device's facts were never gathered")
	// ErrSourceNeverGathered is the ErrNeverGathered of a golden baseline's
	// source.
	ErrSourceNeverGathered = fmt.Errorf("%w: the source", ErrNeverGathered)
)

// maxBaselineLineBytes bounds one baseline line read back.
var maxBaselineLineBytes = SealedLineBytes(maxBaselinePlainBytes)

// BaselineRequest is what taking a baseline asks for.
type BaselineRequest struct {
	// Name names the baseline (letters, digits, '.', '_' and '-').
	Name string
	// Reason says why it is taken.
	Reason string
	// By is who takes it.
	By string
	// From is the device, by its ID, to take it from (a golden host); empty
	// or the device itself takes the device's own facts.
	From string
	// FromName is From's inventory name, recorded beside it.
	FromName string
	// Keys selects the keys it covers; empty is every stable key.
	Keys KeySelector
	// At is when it is taken.
	At time.Time
}

// baselinesFile is the name of device's baselines file.
func baselinesFile(device string) string { return device + ".baselines.jsonl" }

// TakeBaseline takes and stores a baseline of device, under the device's
// lock, and returns it. It is refused before anything is written when the
// store has no keys (ErrNoKeys), the request is invalid, the device or the
// source was never gathered (ErrNeverGathered), no stable key is selected, or
// the device already has a baseline of the name (ErrBaselineExists).
func (s *FileStore) TakeBaseline(ctx context.Context, device string, req BaselineRequest) (Baseline, error) {
	if err := s.keys.Ready(); err != nil {
		return Baseline{}, err
	}
	if err := checkBaselineName(req.Name); err != nil {
		return Baseline{}, err
	}
	if err := checkBaselineText("reason", req.Reason, MaxBaselineReasonBytes); err != nil {
		return Baseline{}, err
	}
	if err := checkBaselineText("taker", req.By, MaxBaselineByBytes); err != nil {
		return Baseline{}, err
	}
	target, err := s.Current(ctx, device)
	if err != nil {
		return Baseline{}, err
	}
	if len(target) == 0 {
		return Baseline{}, fmt.Errorf("%w: %s", ErrNeverGathered, device)
	}
	golden := req.From != "" && req.From != device
	source := target
	if golden {
		if source, err = s.Current(ctx, req.From); err != nil {
			return Baseline{}, err
		}
		if len(source) == 0 {
			return Baseline{}, fmt.Errorf("%w %s", ErrSourceNeverGathered, req.From)
		}
	}
	pairs, err := BaselinePairs(s.keys, device, golden, source, req.Keys)
	if err != nil {
		return Baseline{}, err
	}
	switch {
	case len(pairs) == 0:
		return Baseline{}, fmt.Errorf("%w: no stable fact of the source matches the keys asked for", ErrInvalid)
	case len(pairs) > MaxBaselinePairs:
		return Baseline{}, fmt.Errorf("%w: %d keys, more than a baseline holds (%d)", ErrInvalid, len(pairs), MaxBaselinePairs)
	}
	b := Baseline{Name: req.Name, Device: device, TakenAt: StoredTime(req.At), By: req.By, Reason: req.Reason, KeysID: s.keys.ID(), Keys: req.Keys, Pairs: pairs}
	if golden {
		b.From, b.FromName = req.From, req.FromName
	}
	plain, err := json.Marshal(b)
	if err != nil {
		return Baseline{}, fmt.Errorf("facts: %w", err)
	}
	if len(plain) > maxBaselinePlainBytes {
		return Baseline{}, fmt.Errorf("%w: the baseline is %d bytes, more than %d", ErrInvalid, len(plain), maxBaselinePlainBytes)
	}
	err = s.locked(device, func(r *os.Root) error {
		existing, complete, err := readBaselines(r, s.keys, device)
		if err != nil {
			return err
		}
		for _, e := range existing {
			if e.Name == b.Name {
				return fmt.Errorf("%w: %q", ErrBaselineExists, b.Name)
			}
		}
		raw, err := s.keys.SealLine(device, RoleBaseline, complete, plain)
		if err != nil {
			return fmt.Errorf("facts: %w", err)
		}
		return appendTo(r, baselinesFile(device), complete, append(raw, '\n'))
	})
	if err != nil {
		return Baseline{}, err
	}
	return b, nil
}

// Baselines is every baseline of device, oldest first. A store with no keys
// reads nothing (ErrNoKeys).
func (s *FileStore) Baselines(_ context.Context, device string) ([]Baseline, error) {
	if err := s.keys.Ready(); err != nil {
		return nil, err
	}
	r, err := s.root(device)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	out, _, err := readBaselines(r, s.keys, device)
	return out, err
}

// Baseline is device's baseline named name, or its newest, the active one,
// when name is empty: ErrNoBaseline when there is none.
func (s *FileStore) Baseline(ctx context.Context, device, name string) (Baseline, error) {
	all, err := s.Baselines(ctx, device)
	if err != nil {
		return Baseline{}, err
	}
	if name == "" && len(all) > 0 {
		return all[len(all)-1], nil
	}
	for _, b := range all {
		if b.Name == name {
			return b, nil
		}
	}
	if name == "" {
		return Baseline{}, fmt.Errorf("%w: %s has none", ErrNoBaseline, device)
	}
	return Baseline{}, fmt.Errorf("%w: %s has none named %q", ErrNoBaseline, device, name)
}

// DriftOf is device's drift against its baseline named name (its newest when
// empty) at time at: a report with no_baseline when it has none.
func (s *FileStore) DriftOf(ctx context.Context, device, name string, at time.Time) (DriftReport, error) {
	b, err := s.Baseline(ctx, device, name)
	if errors.Is(err, ErrNoBaseline) {
		reason := "no baseline was taken of it"
		if name != "" {
			reason = fmt.Sprintf("it has no baseline named %q", name)
		}
		return NoBaseline(device, reason), nil
	}
	if err != nil {
		return DriftReport{}, err
	}
	current, err := s.Current(ctx, device)
	if err != nil {
		return DriftReport{}, err
	}
	return Drift(b, s.keys.ID(), current, at), nil
}

// readBaselines reads device's baselines and how many bytes of the file are
// whole lines. Each whole line must open under keys as this device's
// baseline line at its place and decode as a baseline: one that does not is
// an error, never skipped.
func readBaselines(r *os.Root, keys *Keys, device string) ([]Baseline, int64, error) {
	var out []Baseline
	complete, err := readJSONL(r, baselinesFile(device), maxBaselineLineBytes, func(n int, offset int64, sealed []byte) error {
		b, err := DecodeBaselineLine(keys, device, offset, sealed)
		if err != nil {
			return fmt.Errorf("facts: %s line %d: %w", baselinesFile(device), n, err)
		}
		out = append(out, b)
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return out, complete, nil
}

// errNotABaseline marks a line that opens but is not a baseline.
var errNotABaseline = errors.New("not a stored baseline")

// DecodeBaselineLine opens one line of device's baselines file at offset
// (with or without its newline) and decodes the baseline it holds: one bound
// to another device, role or offset, or under another key, is ErrSealed, and
// one that opens but is not a baseline of this device is refused.
func DecodeBaselineLine(keys *Keys, device string, offset int64, sealed []byte) (Baseline, error) {
	raw, err := openStored(keys, device, RoleBaseline, offset, sealed)
	if err != nil {
		return Baseline{}, err
	}
	var b Baseline
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&b); err != nil {
		return Baseline{}, errNotABaseline
	}
	if checkBaselineName(b.Name) != nil || b.Device != device || b.KeysID == "" {
		return Baseline{}, errNotABaseline
	}
	for _, p := range b.Pairs {
		if !wellFormedKey(p.Key) || p.Hash == "" && !p.NotComparable {
			return Baseline{}, errNotABaseline
		}
	}
	return b, nil
}
