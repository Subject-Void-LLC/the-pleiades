// Package facts: taking a baseline in two steps on the Crawl CLI's store
// (Phase 41d). PlanBaseline makes the baseline and every refusal storing it
// can make, writing nothing; StoreBaseline writes it under the device's
// lock. A command taking baselines of several devices plans every one first,
// so a refusal of any leaves every device as it was.
package facts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// PlannedBaseline is a baseline PlanBaseline made and StoreBaseline writes:
// the baseline, and the bytes it is sealed from.
type PlannedBaseline struct {
	// Baseline is what will be stored.
	Baseline Baseline
	// plain is Baseline's JSON, already held to its bound.
	plain []byte
}

// TakeBaseline takes and stores a baseline of device, under the device's
// lock, and returns it: PlanBaseline, then StoreBaseline.
func (s *FileStore) TakeBaseline(ctx context.Context, device string, req BaselineRequest) (Baseline, error) {
	p, err := s.PlanBaseline(ctx, device, req)
	if err != nil {
		return Baseline{}, err
	}
	if err := s.StoreBaseline(ctx, p); err != nil {
		return Baseline{}, err
	}
	return p.Baseline, nil
}

// PlanBaseline makes the baseline of device that req asks for, writing
// nothing, and makes every refusal storing it can make but one: another
// taker storing the same name between this and StoreBaseline, which only the
// write, under the device's lock, can settle. It is refused when the store
// has no keys (ErrNoKeys), the request is invalid, the device or the source
// was never gathered (ErrNeverGathered), a key pattern covers no stable fact,
// no stable key is selected, none of the selected keys could be compared (a
// golden baseline whose every value carries a digest keyed for the source),
// the baseline is too large, or the device already has a baseline of the
// name (ErrBaselineExists). A caller taking baselines of several devices
// plans every one before storing any, so a refusal leaves none written.
func (s *FileStore) PlanBaseline(ctx context.Context, device string, req BaselineRequest) (PlannedBaseline, error) {
	if err := s.keys.Ready(); err != nil {
		return PlannedBaseline{}, err
	}
	if err := checkBaselineName(req.Name); err != nil {
		return PlannedBaseline{}, err
	}
	if err := checkBaselineText("reason", req.Reason, MaxBaselineReasonBytes); err != nil {
		return PlannedBaseline{}, err
	}
	if err := checkBaselineText("taker", req.By, MaxBaselineByBytes); err != nil {
		return PlannedBaseline{}, err
	}
	target, err := s.Current(ctx, device)
	if err != nil {
		return PlannedBaseline{}, err
	}
	if len(target) == 0 {
		return PlannedBaseline{}, fmt.Errorf("%w: %s", ErrNeverGathered, device)
	}
	golden := req.From != "" && req.From != device
	source := target
	if golden {
		if source, err = s.Current(ctx, req.From); err != nil {
			return PlannedBaseline{}, err
		}
		if len(source) == 0 {
			return PlannedBaseline{}, fmt.Errorf("%w %s", ErrSourceNeverGathered, req.From)
		}
	}
	pairs, volatile, err := BaselinePairs(s.keys, device, golden, source, req.Keys)
	if err != nil {
		return PlannedBaseline{}, err
	}
	if err := checkPairs(pairs); err != nil {
		return PlannedBaseline{}, err
	}
	b := Baseline{Name: req.Name, Device: device, TakenAt: StoredTime(req.At), By: req.By, Reason: req.Reason, KeysID: s.keys.ID(), Keys: req.Keys, Pairs: pairs, Volatile: volatile}
	if golden {
		b.From, b.FromName = req.From, req.FromName
	}
	plain, err := json.Marshal(b)
	if err != nil {
		return PlannedBaseline{}, fmt.Errorf("facts: %w", err)
	}
	if len(plain) > maxBaselinePlainBytes {
		return PlannedBaseline{}, fmt.Errorf("%w: the baseline is %d bytes, more than %d", ErrInvalid, len(plain), maxBaselinePlainBytes)
	}
	// The name read here, outside the lock, so a plan is refused before any
	// device is written; StoreBaseline reads it again under the lock.
	if _, err := s.Baseline(ctx, device, req.Name); err == nil {
		return PlannedBaseline{}, fmt.Errorf("%w: %q", ErrBaselineExists, req.Name)
	} else if !errors.Is(err, ErrNoBaseline) {
		return PlannedBaseline{}, err
	}
	return PlannedBaseline{Baseline: b, plain: plain}, nil
}

// checkPairs refuses a baseline's pairs that hold no key, more keys than a
// baseline holds, or no key that could ever be compared: a baseline whose
// every pair is not comparable would read in sync whatever changed.
func checkPairs(pairs []BaselinePair) error {
	notComparable := 0
	for _, p := range pairs {
		if p.NotComparable {
			notComparable++
		}
	}
	switch {
	case len(pairs) == 0:
		return fmt.Errorf("%w: no stable fact matches the keys asked for", ErrInvalid)
	case len(pairs) > MaxBaselinePairs:
		return fmt.Errorf("%w: %d keys, more than a baseline holds (%d)", ErrInvalid, len(pairs), MaxBaselinePairs)
	case notComparable == len(pairs):
		return fmt.Errorf("%w: none of the %d fact(s) selected can be compared: each is not comparable, holding a digest keyed for the source host alone, so the baseline would read in sync whatever changed", ErrInvalid, notComparable)
	}
	return nil
}

// StoreBaseline writes a baseline PlanBaseline made, under its device's
// lock, refusing it when the device has a baseline of the name by then
// (ErrBaselineExists) and leaving every line as it was.
func (s *FileStore) StoreBaseline(_ context.Context, p PlannedBaseline) error {
	if err := s.keys.Ready(); err != nil {
		return err
	}
	b, device := p.Baseline, p.Baseline.Device
	if p.plain == nil {
		return fmt.Errorf("%w: a baseline not made by PlanBaseline", ErrInvalid)
	}
	return s.locked(device, func(r *os.Root) error {
		existing, complete, err := readBaselines(r, s.keys, device)
		if err != nil {
			return err
		}
		for _, e := range existing {
			if e.Name == b.Name {
				return fmt.Errorf("%w: %q", ErrBaselineExists, b.Name)
			}
		}
		raw, err := s.keys.SealLine(device, RoleBaseline, complete, p.plain)
		if err != nil {
			return fmt.Errorf("facts: %w", err)
		}
		return appendTo(r, baselinesFile(device), complete, append(raw, '\n'))
	})
}
