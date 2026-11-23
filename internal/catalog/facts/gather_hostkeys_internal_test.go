// Tests that a Windows host reached over WinRM, which has no SSH host key,
// says so when the host key facts were all a task asked for, and only then.
package facts

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection/facttest"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// winStatsContext is winTestContext keeping stats too, for the warnings.
type winStatsContext struct {
	facts map[string]any
	stats map[string]any
}

// InjectSecrets returns the test credential.
func (c *winStatsContext) InjectSecrets() map[string]string {
	return map[string]string{"username": "gate", "password": "secret"}
}

// SetStat keeps a stat.
func (c *winStatsContext) SetStat(k string, v any) error { c.stats[k] = v; return nil }

// EmitFact keeps a fact, after the audit sees it.
func (c *winStatsContext) EmitFact(k string, v any) error {
	facttest.Observe(k, v)
	c.facts[k] = v
	return nil
}

// TestGather_OverWinRMAHostKeyOnlyFilterSaysWhyItGathersNothing: a filter of
// only ssh.* against a WinRM device sends no script, records nothing and
// warns that WinRM has no SSH host key; the default gather says nothing of
// host keys.
func TestGather_OverWinRMAHostKeyOnlyFilterSaysWhyItGathersNothing(t *testing.T) {
	asked := answerWindows(t, map[string]winrmexec.Result{
		gatherSubsetMin:      {Stdout: `{"host.nodename":"win-check","os.kernel.name":"windows"}`},
		gatherSubsetHardware: {Stdout: `{"hardware.memory_mb":2047}`},
	})
	rc := &winStatsContext{facts: map[string]any{}, stats: map[string]any{}}
	if _, err := Gather(context.Background(), rc, newWinTestHost(), map[string]any{gatherParamFilter: []any{"ssh.*"}}); err != nil {
		t.Fatalf("Gather: %v", err)
	}
	warnings, _ := rc.stats[sdk.StatWarnings].([]string)
	if len(*asked) != 0 || len(rc.facts) != 0 || len(warnings) != 1 || !strings.Contains(warnings[0], "WinRM, which has no SSH host key") {
		t.Errorf("asked %v, facts %v, warnings %q; want no script, no fact and one warning naming WinRM", *asked, rc.facts, warnings)
	}

	rc = &winStatsContext{facts: map[string]any{}, stats: map[string]any{}}
	if _, err := Gather(context.Background(), rc, newWinTestHost(), nil); err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if w, _ := rc.stats[sdk.StatWarnings].([]string); len(w) != 0 {
		t.Errorf("the default gather over WinRM warned %q, want nothing about host keys", w)
	}
}
