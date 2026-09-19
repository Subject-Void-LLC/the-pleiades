// Package block: an internal test of the check answer's refusal, which no
// call through file.block.set or file.block.remove can reach today.
package block

import (
	"strings"
	"testing"
)

// answerContext records whatever a check answer records.
type answerContext struct{ stats map[string]any }

func (c *answerContext) InjectSecrets() map[string]string { return nil }
func (c *answerContext) SetStat(key string, value any) error {
	c.stats[key] = value
	return nil
}
func (c *answerContext) EmitFact(key string, value any) error { return c.SetStat(key, value) }

// TestBlockCheckAnswer_RefusesLinesThatDoNotReadBack pins blockCheckAnswer's
// guard: lines whose read-back would find the begin marker twice are
// refused with blockFind's own reason, and nothing is recorded, so a check
// never reports a region the method itself could not find again. The
// control is the same call with one pair of markers, which records both
// the diff and the stats.
func TestBlockCheckAnswer_RefusesLinesThatDoNotReadBack(t *testing.T) {
	m, err := blockMarkerRequest(map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	begin, end := m.beginLine, m.endLine

	control := &answerContext{stats: map[string]any{}}
	if err := blockCheckAnswer(control, "/etc/f", blockObservation{}, []string{begin, "body", end}, true, m); err != nil {
		t.Fatalf("one pair of markers was refused: %v", err)
	}
	if len(control.stats) == 0 {
		t.Fatal("the control recorded nothing, so the refusal below proves nothing")
	}

	rc := &answerContext{stats: map[string]any{}}
	err = blockCheckAnswer(rc, "/etc/f", blockObservation{}, []string{begin, "body", begin, end}, true, m)
	if err == nil || !strings.Contains(err.Error(), "copies of the begin marker") {
		t.Errorf("err = %v, want blockFind's refusal of two begin markers", err)
	}
	if len(rc.stats) != 0 {
		t.Errorf("a refused answer recorded %v", rc.stats)
	}
}
