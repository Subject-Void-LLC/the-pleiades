// Package restconflive runs pkg/restconf against a real RESTCONF server in a
// container: rousette over sysrepo, in the notconf image the NETCONF tests
// already run, which serves NETCONF and RESTCONF over one datastore. It is
// apart from pkg/restconf so that package's own tests need no Docker and run
// on every platform's container-free legs.
package restconflive
