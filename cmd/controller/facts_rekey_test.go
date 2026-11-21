// Tests of a change of the facts key as main wires it: the previous key read
// from PLEIADES_FACTS_DIGEST_KEY_PREVIOUS and refused by name when it cannot
// be one, and the pass at start reporting whether the previous key is still
// needed.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/internal/facts/entstore"
)

// previousDigestSecret is a 32-byte digest secret other than digestSecret.
var previousDigestSecret = []byte("the previous 32-byte facts key!!")

func TestLoadFactAtRestKeys_ReadsThePreviousKeyOrRefusesItByName(t *testing.T) {
	encode := base64.StdEncoding.EncodeToString
	t.Setenv(factDigestKeyEnv, encode(digestSecret))
	t.Setenv(factDigestKeyPreviousEnv, "")
	current, previous, err := loadFactAtRestKeys(quietLogger())
	if err != nil || current == nil || previous != nil {
		t.Fatalf("no previous key: %v %v %v, want the current keys alone", current, previous, err)
	}

	for name, value := range map[string]string{
		"not base64":         "abc",
		"too short":          encode([]byte("sixteen bytes!!!")),
		"the current secret": encode(digestSecret),
	} {
		t.Setenv(factDigestKeyPreviousEnv, value)
		if _, _, err := loadFactAtRestKeys(quietLogger()); err == nil || !strings.Contains(err.Error(), factDigestKeyPreviousEnv) {
			t.Errorf("%s: %v, want a refusal naming %s", name, err, factDigestKeyPreviousEnv)
		}
	}

	t.Setenv(factDigestKeyPreviousEnv, encode(previousDigestSecret))
	current, previous, err = loadFactAtRestKeys(quietLogger())
	if err != nil || current == nil || previous == nil {
		t.Fatalf("a valid previous key: %v", err)
	}
	wantCurrent, _ := entstore.AtRestKeys(digestSecret)
	wantPrevious, _ := entstore.AtRestKeys(previousDigestSecret)
	if current.ID() != wantCurrent.ID() || previous.ID() != wantPrevious.ID() || previous.ID() == current.ID() {
		t.Errorf("keys %s and %s, want %s and %s", current.ID(), previous.ID(), wantCurrent.ID(), wantPrevious.ID())
	}

	// A previous key with no current one is a misconfiguration, not a
	// controller that stores nothing.
	t.Setenv(factDigestKeyEnv, "")
	if _, _, err := loadFactAtRestKeys(quietLogger()); err == nil || !strings.Contains(err.Error(), factDigestKeyPreviousEnv) {
		t.Errorf("a previous key alone: %v, want a refusal naming both", err)
	}
	t.Setenv(factDigestKeyPreviousEnv, "")
	if current, previous, err := loadFactAtRestKeys(quietLogger()); current != nil || previous != nil || err != nil {
		t.Errorf("no key at all: %v %v %v, want none and no error", current, previous, err)
	}
}

// countingRekeyer is a factsRekeyer that reports count and err.
type countingRekeyer struct {
	count entstore.RekeyCount
	err   error
}

// RekeyAll implements factsRekeyer.
func (r countingRekeyer) RekeyAll(context.Context) (entstore.RekeyCount, error) {
	return r.count, r.err
}

func TestRunFactsRekey_SaysWhetherThePreviousKeyIsStillNeeded(t *testing.T) {
	for _, tc := range []struct {
		name      string
		rekeyer   countingRekeyer
		want      []string
		forbidden string
	}{
		{"complete", countingRekeyer{count: entstore.RekeyCount{Devices: 3, Facts: 21}},
			[]string{"devices=3", "facts=21", "no stored fact needs " + factDigestKeyPreviousEnv + " any more", "once every replica runs with the new"}, "keep " + factDigestKeyPreviousEnv},
		{"a device left", countingRekeyer{count: entstore.RekeyCount{Devices: 2, Refused: 1, RefusedDevices: []string{"dev-9"}, Remaining: 1}},
			[]string{"refused=1", "dev-9", "keep " + factDigestKeyPreviousEnv + " set"}, "rotation complete"},
		{"a pass that failed", countingRekeyer{count: entstore.RekeyCount{Devices: 1}, err: errors.New("database gone")},
			[]string{"level=ERROR", "database gone", "keep " + factDigestKeyPreviousEnv + " set"}, "rotation complete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			runFactsRekey(context.Background(), tc.rekeyer, slog.New(slog.NewTextHandler(&logs, nil)))
			out := logs.String()
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("the log does not say %q:\n%s", want, out)
				}
			}
			if strings.Contains(out, tc.forbidden) {
				t.Errorf("the log says %q:\n%s", tc.forbidden, out)
			}
		})
	}
}
