package recon

import (
	"context"
	"io"
)

// Source is the contract every config-data source implements. The registry
// composes one or more Sources in precedence order and asks each in turn to
// look up a key.
//
// Sources are constructed with their own configuration (file path, env-var
// prefix, in-memory map, remote backend, etc.) and registered with a
// Registry via [WithSource] or [Registry.AddSource]. A Source is consulted
// only after the registry's own explicit / pinned / aliased layers resolve
// — see [Registry] for the full resolution order.
type Source interface {
	// Name identifies the source in [Event] / [Describe] output. Names must
	// be unique within a single Registry; AddSource returns
	// [ErrSourceConflict] on collision.
	Name() string

	// Get returns the value at path, if present. The returned [Value] preserves
	// the wire type from the underlying format — the registry performs typed
	// coercion at the call site, not here.
	//
	// A returned (Value, false, nil) means "the key is not in this source";
	// (Value, true, nil) means "set" (an empty-string value is still a present
	// value); (Value, _, err) reports a source-internal error and propagates
	// per the configured [ErrorBehavior].
	Get(path Path) (Value, bool, error)

	// Keys enumerates every path this source can answer. Used by
	// [Registry.AllKeys] and [Registry.Describe] for introspection. The
	// implementation MAY be expensive — the registry calls it sparingly and
	// caches the result inside snapshots.
	Keys() []Path

	// Close releases any resources held by the source (open files, watcher
	// subscriptions, network connections). Close is idempotent; calling it
	// more than once is not an error. Sources that hold no resources may
	// return nil.
	Close() error
}

// Watcher is an optional [Source] capability. Sources that implement Watcher
// participate in live reload — the registry subscribes once at construction
// and the engine fans every Watcher's [SourceChange] events into a single
// reload pipeline.
//
// Implementations MUST honor ctx cancellation: when ctx is canceled, Watch
// closes the returned channel cleanly and returns from any internal
// goroutine.
type Watcher interface {
	Watch(ctx context.Context) (<-chan SourceChange, error)
}

// Writer is an optional [Source] capability. Sources that implement Writer
// can be the target of [Registry.Save] for round-tripping the current
// resolved view back out to bytes (typically: a file source persisting the
// snapshot back to disk).
type Writer interface {
	Save(w io.Writer, opts ...SaveOption) error
}

// SourceChange is what a [Watcher] emits when a source's content may have
// changed. Keys lists the paths whose value the source believes have changed;
// an empty Keys slice signals "I don't know what changed — re-read
// everything." Err is non-nil when the source itself failed to refresh
// (e.g., the watched file was deleted and the watcher cannot recover).
//
// Callers should treat SourceChange as advisory: the registry re-reads the
// affected source(s) and computes the actual changed-keys delta against the
// previous snapshot before emitting an [Event] on the public channel.
type SourceChange struct {
	Keys []Path
	Err  error
}
