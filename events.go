package recon

import "time"

// Event is delivered on the channel returned by [Registry.Events].
// One Event corresponds to one reload attempt — successful or failed.
//
// On success Err is nil and Changed lists the paths whose resolved value
// differs from the previous snapshot. On failure Err is set, Changed is
// empty, and the previous snapshot is retained.
type Event struct {
	// Time is the wall-clock time the reload attempt completed.
	Time time.Time

	// Source is the name of the source whose change triggered the
	// reload. Empty for manual reloads via [Registry.Reload].
	Source string

	// Changed lists paths whose resolved value differs from the previous
	// snapshot, sorted by canonical path string. Empty on failure.
	Changed []Path

	// Err is non-nil when the reload failed. Readers continue to observe
	// the previous snapshot until the next successful reload.
	Err error

	// Warnings carries non-fatal notices (deprecations, dropped events)
	// that should not invalidate the snapshot.
	Warnings []DeprecationWarning
}
