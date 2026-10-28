// How win.firewall.rule reaches a Windows host: one constant script over
// WinRM, its values as ASCII JSON on standard input, as win.schtask does.
package firewall

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/devicetls"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/farside"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/inventory"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/winrmexec"
)

// The parameters every call has.
const (
	paramName  = "name"
	paramState = "state"
)

// maxAnswer bounds the script's answer (a rule is well under a KiB).
const maxAnswer = 1 << 20

// run is how the script runs; a test replaces it.
var run = func(ctx context.Context, target winrmexec.Target, auth winrmexec.Auth, script, stdin string, opts winrmexec.Options) (winrmexec.Result, error) {
	return winrmexec.RunWithStdin(ctx, target, auth, winrmexec.ShellPowerShell, script, stdin, opts)
}

// session is the device's WinRM target and this run's credential.
func session(rc sdk.RunbookContext, device inventory.InventoryItem) (winrmexec.Target, winrmexec.Auth, winrmexec.Options, error) {
	dev, ok := device.(capability.WinRMCapable)
	if !ok {
		return winrmexec.Target{}, winrmexec.Auth{}, winrmexec.Options{}, errors.New("the device is not reached over WinRM")
	}
	auth, err := winrmexec.AuthFromSecrets(rc.InjectSecrets())
	if err != nil {
		return winrmexec.Target{}, winrmexec.Auth{}, winrmexec.Options{}, err
	}
	return winrmexec.Target{Host: dev.WinRMHost(), Port: dev.WinRMPort()}, auth, winrmexec.WithDeviceTLS(winrmexec.Options{}, devicetls.For(device)), nil
}

// asciiJSON is JSON with every character past ASCII written as a \u escape,
// so the script reads it the same whatever code page the device's console
// decodes standard input with.
func asciiJSON(in []byte) []byte {
	var b strings.Builder
	for _, r := range string(in) {
		switch {
		case r < 0x80:
			b.WriteRune(r)
		case r > 0xFFFF:
			hi, lo := utf16.EncodeRune(r)
			fmt.Fprintf(&b, `\u%04x\u%04x`, hi, lo)
		default:
			fmt.Fprintf(&b, `\u%04x`, r)
		}
	}
	return []byte(b.String())
}

// bounded is PowerShell's error text fit for an error: on one line,
// quoted and bounded through pkg/farside, so a long answer or a control
// byte cannot reach the error raw.
func bounded(s string) string {
	return farside.Output(s)
}

// toAny is a string list as a return value.
func toAny(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}
