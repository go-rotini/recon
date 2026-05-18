package recon

import "context"

// Source is the contract every config-data source implements. The
// registry composes one or more Sources in precedence order and asks
// each in turn to look up a key. A Source is consulted only after the
// registry's own explicit / pinned / aliased layers resolve.
type Source interface {
	// Name identifies the source in [Event] and [Describe] output.
	// Names must be unique within a Registry.
	Name() string

	// Get returns the value at path. The returned [Value] preserves
	// the wire type from the underlying format; typed coercion happens
	// at the registry call site.
	//
	// (Value, false, nil) means "not present"; (Value, true, nil)
	// means "set" (an empty string is a present value);
	// (Value, _, err) reports a source-internal error.
	Get(path Path) (Value, bool, error)

	// Keys enumerates every path this source can answer. May be
	// expensive; the registry caches the result inside snapshots. The
	// returned slice must not be mutated by callers — sources may
	// alias internal storage.
	Keys() []Path

	// Close releases any resources held by the source. Idempotent;
	// sources that hold no resources may return nil.
	Close() error
}

// Watcher is an optional [Source] capability. Sources implementing
// Watcher participate in live reload: the registry subscribes once at
// construction and fans every emitted [SourceChange] into a single
// reload pipeline.
//
// Implementations must honor ctx cancellation by closing the returned
// channel and returning from any internal goroutine.
type Watcher interface {
	Watch(ctx context.Context) (<-chan SourceChange, error)
}

// SourceChange is what a [Watcher] emits when source content may have
// changed. An empty Keys slice signals "re-read everything"; a non-nil
// Err signals an unrecoverable refresh failure.
type SourceChange struct {
	Keys []Path
	Err  error
}
