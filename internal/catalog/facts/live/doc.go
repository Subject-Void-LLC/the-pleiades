// Package live runs facts.gather's probes against real systems in
// containers (an rpm database in Rocky Linux, OpenRC in Alpine), apart from
// internal/catalog/facts so that package's own tests need no Docker and run
// on every platform's container-free legs.
package live
