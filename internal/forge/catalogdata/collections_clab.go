// This file holds the virt.clab.* Collection: labs, nodes, link impairment,
// images and the server itself on a containerlab API server, one verb per
// method. Its reference text is pkg/clabapi's, the same text the registered
// methods carry, so the two cannot disagree.
package catalogdata

import (
	"github.com/Subject-Void-LLC/the-pleiades/internal/forge/collectionscaffold"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/clabapi"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

// clabCollections is one entry per documented virt.clab method.
var clabCollections = func() []collectionscaffold.Config {
	specs := clabapi.Specs()
	out := make([]collectionscaffold.Config, 0, len(specs))
	for _, s := range specs {
		out = append(out, collectionscaffold.Config{
			Name:          s.Name,
			Capabilities:  []capability.Name{capability.NameClabAPI},
			Transports:    []string{capability.TransportHTTPS},
			Site:          collection.SiteController,
			Device:        collection.DeviceRequired,
			EngineVersion: ">=0.3.0",
			Doc:           s.Doc,
		})
	}
	return out
}()
