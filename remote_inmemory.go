package recon

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"
)

// MemoryBackend is the reference [RemoteBackend] implementation
// recon ships for tests and for callers prototyping with no real
// remote store handy. It is NOT intended for production use —
// production backends (etcd, consul, vault, …) live in separate
// go-rotini adapter modules.
//
// MemoryBackend implements [BackendWatcher] so [RemoteSource] sees a
// push-style notification path; mutating via [Put] / [Delete] fires
// the subscription on every active Watch.
//
// Safe for concurrent use.
type MemoryBackend struct {
	mu     sync.RWMutex
	data   map[string]string
	subs   []chan struct{}
	closed bool
}

// NewInMemoryBackend constructs an empty [MemoryBackend]. Seed
// content with [MemoryBackend.Put] / [MemoryBackend.PutAll].
func NewInMemoryBackend() *MemoryBackend {
	return &MemoryBackend{data: map[string]string{}}
}

// Put sets key to value. A pre-existing key is overwritten;
// subscribers receive a notification. Calling Put on a closed
// backend is a silent no-op.
func (m *MemoryBackend) Put(key, value string) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.data[key] = value
	subs := append([]chan struct{}(nil), m.subs...)
	m.mu.Unlock()
	notifyAll(subs)
}

// PutAll seeds many keys at once. Fires exactly one notification on
// each active subscription, regardless of how many keys are
// added — useful for test fixtures that want to drive a single
// reload event.
func (m *MemoryBackend) PutAll(kv map[string]string) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	maps.Copy(m.data, kv)
	subs := append([]chan struct{}(nil), m.subs...)
	m.mu.Unlock()
	notifyAll(subs)
}

// Delete removes key. Subscribers receive a notification when key
// existed. A delete of a missing key is a no-op (no notification).
func (m *MemoryBackend) Delete(key string) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	if _, ok := m.data[key]; !ok {
		m.mu.Unlock()
		return
	}
	delete(m.data, key)
	subs := append([]chan struct{}(nil), m.subs...)
	m.mu.Unlock()
	notifyAll(subs)
}

// List implements [RemoteBackend]. Returns every key under prefix,
// in sorted order for deterministic output.
func (m *MemoryBackend) List(_ context.Context, prefix string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.data))
	for k := range m.data {
		if prefix == "" || strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out, nil
}

// Get implements [RemoteBackend].
func (m *MemoryBackend) Get(_ context.Context, key string) (string, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.data[key]
	return v, ok, nil
}

// Watch implements [BackendWatcher]. Each call returns a fresh
// channel; the channel closes when ctx cancels or the backend is
// closed.
func (m *MemoryBackend) Watch(ctx context.Context) (<-chan struct{}, error) {
	ch := make(chan struct{}, 1)
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		close(ch)
		return ch, nil
	}
	m.subs = append(m.subs, ch)
	m.mu.Unlock()

	go func() {
		<-ctx.Done()
		m.mu.Lock()
		for i, c := range m.subs {
			if c == ch {
				m.subs = append(m.subs[:i], m.subs[i+1:]...)
				close(c)
				m.mu.Unlock()
				return
			}
		}
		m.mu.Unlock()
	}()
	return ch, nil
}

// Close releases the backend's subscriptions. Idempotent.
func (m *MemoryBackend) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	for _, c := range m.subs {
		close(c)
	}
	m.subs = nil
	return nil
}

// Snapshot returns a copy of the backend's current data — used by
// tests that want to verify state without going through the
// RemoteBackend interface.
func (m *MemoryBackend) Snapshot() map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]string, len(m.data))
	maps.Copy(out, m.data)
	return out
}

// notifyAll non-blockingly delivers a tick to every channel. Used
// by [Put] / [PutAll] / [Delete]; the lock has already been
// released so subscribers can take the lock on their own refresh
// path without deadlocking.
func notifyAll(subs []chan struct{}) {
	for _, c := range subs {
		select {
		case c <- struct{}{}:
		default:
			// Subscriber already has a pending notification —
			// coalesce.
		}
	}
}
