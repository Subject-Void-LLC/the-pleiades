// container.info, list and logs: what the engine says of its containers,
// read only, so each one's check is the method itself.
package container

import (
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/containerengine"
)

const (
	paramAll   = "all"
	paramMatch = "match"
	paramTail  = "tail"
	paramSince = "since"

	statContainers = "containers"
	statExists     = "exists"
	statStdout     = "stdout"
	statStderr     = "stderr"

	// maxListed bounds how many containers one list reports.
	maxListed = 1000
	// defaultTail and maxTail bound how many log lines one read returns.
	defaultTail = 100
	maxTail     = 10000
	// maxLogBytes bounds the log text one read keeps, refused past it.
	maxLogBytes = 1 << 20
)

// readOnly is the reversibility every reading method declares.
var readOnly = collection.Reversibility{ReadOnly: true, Notes: "It reads what the engine reports, and changes nothing."}

// registerRead registers a method that only reads: a check of it is the
// task itself.
func registerRead(name string, fn collection.Method) {
	collection.MustRegister(collection.Descriptor{
		Name:     name,
		Manifest: containerengine.MethodManifest(readOnly, readDoc(name)),
		Invoke:   fn,
		Check:    fn,
	})
}
