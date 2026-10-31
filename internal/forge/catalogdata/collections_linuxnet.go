// This file holds the net.linux.* Collection: a Linux host's bridges,
// addresses and routes through iproute2's ip, as the connecting account or, with
// become, as root. Its reference text is pkg/linuxnet's, the same text the
// registered methods carry, so the two cannot disagree.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/linuxnet"
)

// linuxCollections is one entry per documented net.linux method.
var linuxCollections = func() []collectionscaffold.Config {
	specs := linuxnet.Specs()
	out := make([]collectionscaffold.Config, 0, len(specs))
	for _, s := range specs {
		out = append(out, collectionscaffold.Config{
			Name:          s.Name,
			Capabilities:  []capability.Name{capability.NameLinux, capability.NameCommandExec},
			Transports:    []string{capability.TransportSSH},
			EngineVersion: ">=0.3.0",
			Doc:           s.Doc,
		})
	}
	return out
}()
