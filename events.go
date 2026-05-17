package recon

import "time"

// Event is delivered on the channel returned by [Registry.Events] (Phase 8+).
// One Event corresponds to one reload attempt — successful or failed.
//
// Successful reloads have Err == nil and Changed populated with the paths
// whose resolved value differs from the previous snapshot. Failed reloads
// have Err != nil; the previous snapshot is retained and the Changed slice
// is empty. Non-fatal Warnings (typically [DeprecationWarning] values
// emitted when a deprecated key was read) ride along on every Event so the
// receiving handler can route them out-of-band from the error path.
type Event struct {
	// Time is the wall-clock time the reload attempt completed.
	Time time.Time

	// Source is the name of the source whose change triggered the reload,
	// when known. Empty for manual reloads (Registry.Reload).
	Source string

	// Changed lists the paths whose resolved value differs from the
	// previous snapshot. Sorted by canonical path string for deterministic
	// ordering. Empty on a failed reload.
	Changed []Path

	// Err is non-nil on a failed reload. The previous snapshot is retained
	// when Err != nil — every Get / Bind continues to observe the pre-
	// failure state until the next successful reload.
	Err error

	// Warnings carries non-fatal events (deprecations, dropped events,
	// source-side notices that do not invalidate the snapshot). Consumers
	// route these to a logger or to a rotini-style OnProgramWarning hook.
	Warnings []DeprecationWarning
}
