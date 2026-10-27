// Tests for secret references in a dialog's parameters.
package clidialog_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/clidialog"
)

// plainRC is a run with no named secrets.
type plainRC struct{}

func (plainRC) InjectSecrets() map[string]string { return nil }
func (plainRC) SetStat(string, any) error        { return nil }
func (plainRC) EmitFact(string, any) error       { return nil }

// TestRead_AReferenceBelongsInAnswerOnly: a reference in match is refused,
// naming answer, rather than compiled as a pattern and never filled in.
func TestRead_AReferenceBelongsInAnswerOnly(t *testing.T) {
	_, err := clidialog.Read(context.Background(), plainRC{}, "net.ios.command", map[string]any{
		"prompts": []any{map[string]any{"match": "{{ secret.ca.passphrase }}", "answer": "yes"}},
	})
	if err == nil || !strings.Contains(err.Error(), "belongs in answer") {
		t.Fatalf("err = %v", err)
	}
}
