// Command external_collection is a complete, working external Collection:
// a program built outside the pleiades binary that The Pleiades runs as a
// child process, once per task, to provide a Collection method of its own.
//
// It provides one method, example.note.write, which makes sure a file on
// the target holds exactly the text a task asks for. It is small on
// purpose, and it is not a toy: it reaches a real device over SSH with the
// credential The Pleiades hands it, it reads before it writes so a converged
// run reports no change, and it answers check mode honestly. It is the
// program the external Collection Release Gate builds and runs, and the
// worked example in docs/11-extending-pleiades.md.
//
// It imports only pkg/ packages, which is the whole constraint an
// external Collection lives under: a program outside this repository can
// import pkg/ and nothing else. Build it, drop the binary into the
// directory PLEIADES_COLLECTIONS_DIR names, and example.note.write is
// usable from any runbook. See README.md beside this file.
package main

import "github.com/Subject-Void-LLC/the-pleiades/pkg/external"

// main serves this program's one method. external.Main answers the two
// commands The Pleiades runs it with ("describe" and "invoke") and exits; the
// program has no other behavior.
func main() {
	external.Main(noteWriteDescriptor())
}
