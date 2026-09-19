//go:build windows

// Package loader: the refusal on Windows.
package loader

import (
	"context"
	"strings"
	"testing"
)

// TestLoad_WindowsRefusesNamingTheReasonAndWSL is the Windows half of the
// platform decision: every entry point refuses, and the refusal says why
// and points at WSL 2, where the whole feature works today.
func TestLoad_WindowsRefusesNamingTheReasonAndWSL(t *testing.T) {
	_, loadErr := Load(context.Background(), `C:\collections`, Options{})
	_, readErr := ReadApprovals(`C:\collections`)
	_, _, inspectErr := Inspect(context.Background(), `C:\collections`, "note", Options{})
	for name, err := range map[string]error{"Load": loadErr, "ReadApprovals": readErr, "Inspect": inspectErr} {
		if err == nil {
			t.Fatalf("%s did not refuse on Windows", name)
		}
		for _, want := range []string{"not supported on windows", "confined", "WSL 2"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s's refusal %q does not say %q", name, err, want)
			}
		}
	}
}
