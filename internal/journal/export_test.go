// Package journal: the unexported seams its external tests reach, and
// nothing else.
package journal

// AllForJobWithin and OnDevicesSinceWithin are the rollback reads with
// their bounds as parameters, so a test can reach them without writing a
// hundred thousand rows.
var (
	AllForJobWithin      = (*EntStore).allForJob
	OnDevicesSinceWithin = (*EntStore).onDevicesSince
)
