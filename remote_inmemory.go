package recon

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"
)

// MemoryBackend is an in-memory [RemoteBackend] for tests and local
// prototyping.
//
// It implements [BackendLister] and [BackendWatcher]: [MemoryBackend.Put],
// [MemoryBackend.PutAll] and [MemoryBackend.Delete] notify every active
// subscription. After [MemoryBackend.Close], writes are ignored. Safe
// for concurrent use.
type MemoryBackend struct {
	mu     sync.RWMutex
	data   map[string]string
	subs   []chan struct{}
	closed bool
}

// NewInMemoryBackend returns an empty [MemoryBackend].
func NewInMemoryBackend() *MemoryBackend {
	return &MemoryBackend{data: map[string]string{}}
}

// Put sets key to value and notifies subscribers.
func (m *MemoryBackend) Put(key, value string) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.data[key] = value
	// Notify while holding the lock: Close and a watch cancel close the
	// channels under it, and the sends never block.
	notifyAll(m.subs)
	m.mu.Unlock()
}

// PutAll seeds many keys with a single notification fanout.
func (m *MemoryBackend) PutAll(kv map[string]string) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	maps.Copy(m.data, kv)
	// Notify while holding the lock: Close and a watch cancel close the
	// channels under it, and the sends never block.
	notifyAll(m.subs)
	m.mu.Unlock()
}

// Delete removes key. Notifies subscribers only when key existed.
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
	// Notify while holding the lock: Close and a watch cancel close the
	// channels under it, and the sends never block.
	notifyAll(m.subs)
	m.mu.Unlock()
}

// List implements [RemoteBackend]. Returns matching keys in sorted
// order.
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
// channel that closes when ctx cancels or the backend closes.
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

// Close releases subscriptions. Idempotent.
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

// Snapshot returns a copy of the current data, intended for tests.
func (m *MemoryBackend) Snapshot() map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]string, len(m.data))
	maps.Copy(out, m.data)
	return out
}

// notifyAll delivers a non-blocking tick to every channel; an already
// pending notification coalesces.
func notifyAll(subs []chan struct{}) {
	for _, c := range subs {
		select {
		case c <- struct{}{}:
		default:
		}
	}
}

// ListValues implements [BackendLister]: every key under prefix with
// its value.
func (m *MemoryBackend) ListValues(_ context.Context, prefix string) (map[string]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]string, len(m.data))
	for k, v := range m.data {
		if prefix == "" || strings.HasPrefix(k, prefix) {
			out[k] = v
		}
	}
	return out, nil
}
