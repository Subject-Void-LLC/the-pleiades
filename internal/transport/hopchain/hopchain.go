// Package hopchain translates transport.Target's Route into the
// pkg/remoteexec-level hop chain pkg/remoteexec.Runner.DialThroughHops
// needs, so internal/transport/serialtcp and internal/transport/telnet
// share ONE translation instead of each hand-rolling a near-identical
// copy of internal/transport/ssh's own unexported hopsFrom. That
// existing copy stays exactly as it is: it predates this package, its
// own tests already cover it, and touching already-shipped, already-
// tested code for a DRY nicety with no behavior change is not this
// phase's job.
package hopchain

import (
	"fmt"

	"github.com/Subject-Void-LLC/the-pleiades/internal/transport"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/remoteexec"
)

// Convert translates route into the remoteexec-level hop chain,
// converting each hop's already-resolved credential.Credential into
// exactly one remoteexec.Auth: the identical conversion
// internal/transport/ssh's own hopsFrom applies, just shared. A hop
// whose credential cannot produce a usable authentication method is a
// hard error before any network I/O, naming that hop's own device
// rather than reporting a generic authentication failure once dialing
// has already started.
//
// An empty route returns a nil hop slice, exactly
// pkg/remoteexec.Runner.DialThroughHops' own "nil means a direct
// connection" contract, so a Target with no Route costs nothing extra.
func Convert(route []transport.Hop) ([]remoteexec.Hop, error) {
	if len(route) == 0 {
		return nil, nil
	}

	hops := make([]remoteexec.Hop, len(route))
	for i, hop := range route {
		auth, err := remoteexec.AuthFrom(hop.Credential.Username, hop.Credential.Password, hop.Credential.PrivateKeyPEM, hop.Credential.Passphrase)
		if err != nil {
			return nil, fmt.Errorf("hop %q: %w", hop.DeviceName, err)
		}
		hops[i] = remoteexec.Hop{
			Target: remoteexec.Target{Host: hop.Host, Port: hop.Port},
			Auth:   auth,
		}
	}
	return hops, nil
}
