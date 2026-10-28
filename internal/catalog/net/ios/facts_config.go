// net.ios.facts: the config subset, the running configuration with every
// secret masked before any of it leaves this method.
package ios

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/netcfg"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// showRunningConfig is the command the config subset reads.
const showRunningConfig = "show running-config"

// gatherConfig reads the running configuration into g, masked through
// pkg/netcfg (MaskChecked), and adds each secret it masked that is long
// enough to the run's masking set, so the same value is masked wherever else
// the run prints it. It reports whether the subset was gathered.
//
// A device that refuses the command (an account in user mode) fails the
// subset when it was named, and otherwise leaves it out with a warning. Text
// that does not end at the configuration's "end" line, or that masking
// cannot vouch for, fails it either way: none of it is returned.
func gatherConfig(ctx context.Context, rc sdk.RunbookContext, session iosSession, g *gathered, named bool) (bool, error) {
	out, err := show(ctx, session, showRunningConfig)
	var refused *errRefused
	if errors.As(err, &refused) && !named {
		g.warnings = append(g.warnings, refused.Error()+"; the config subset is left out: show running-config needs privileged mode, so store the device's enable secret (pleiades add-credential --enable-password-stdin)")
		return false, nil
	}
	if err != nil {
		return false, err
	}
	masked, found, err := netcfg.MaskChecked(out)
	// Whatever masking found is masked by value from here on, even when the
	// subset is refused: the error says nothing of it, but a later task may
	// print the same text.
	sdk.RegisterSecret(rc, netcfg.Learnable(found)...)
	if err != nil {
		return false, fmt.Errorf("%s: the configuration is not returned, since masking cannot vouch for it: %w", showRunningConfig, err)
	}
	if !endsAtEnd(masked) {
		return false, fmt.Errorf("%s: the output does not end at the configuration's \"end\" line, so it may be cut short and is not returned", showRunningConfig)
	}
	g.values[factConfig] = strings.TrimSpace(masked) + "\n"
	return true, nil
}

// endsAtEnd reports whether config's last line that holds anything is
// "end", the line IOS closes every whole configuration with. A read cut
// short (a session that closed, a pager that was never turned off) does not
// end there.
func endsAtEnd(config string) bool {
	lines := strings.Split(strings.TrimRight(config, " \t\n"), "\n")
	return strings.TrimSpace(lines[len(lines)-1]) == "end"
}
