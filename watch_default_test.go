package recon

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNew_InstallsNoDefaultWatcher(t *testing.T) {
	path := writeTempFile(t, "app.yaml", "k: v\n")
	src, err := NewFileSource(path)
	if err != nil {
		t.Fatalf("NewFileSource: %v", err)
	}
	r, err := New(WithSource(src))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	if r.state.opts.watcher != nil {
		t.Errorf("default watcher = %T, want none", r.state.opts.watcher)
	}
	fs, ok := src.(*FileSource)
	if !ok {
		t.Fatalf("source is %T", src)
	}
	if fs.watcherFactory() != nil {
		t.Error("file source got a watcher without WithWatcher")
	}
	if got, _, _ := r.GetString("k"); got != "v" {
		t.Errorf("k = %q, want v", got)
	}
}

func TestWithoutWatch(t *testing.T) {
	r, err := New(WithoutWatch(), WithPoll(time.Millisecond),
		WithSource(NewMapSource("m", map[string]any{"k": "v"})))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if r.state.watch != nil {
		t.Error("watch engine started under WithoutWatch")
	}
	if r.Events() != nil {
		t.Error("Events should be nil under WithoutWatch")
	}
	if err := r.Set("k", "w"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := r.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got, _, _ := r.GetString("k"); got != "w" {
		t.Errorf("k = %q, want w", got)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestNewLive_RefusesUnwatchedRegistry(t *testing.T) {
	t.Run("WithoutWatch", func(t *testing.T) {
		r, err := New(WithoutWatch())
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		t.Cleanup(func() { _ = r.Close() })
		if _, err := NewLive[liveConfig](r); !errors.Is(err, ErrNotWatched) {
			t.Fatalf("NewLive err = %v, want ErrNotWatched", err)
		}
	})
	t.Run("file source without a watcher", func(t *testing.T) {
		src, err := NewFileSource(writeTempFile(t, "app.yaml", "k: v\n"))
		if err != nil {
			t.Fatalf("NewFileSource: %v", err)
		}
		r, err := New(WithSource(src))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		t.Cleanup(func() { _ = r.Close() })
		_, err = NewLive[liveConfig](r)
		if !errors.Is(err, ErrNotWatched) {
			t.Fatalf("NewLive err = %v, want ErrNotWatched", err)
		}
		if !strings.Contains(err.Error(), "recon.WithWatcher(fswatch.New())") {
			t.Errorf("error should name the fix: %v", err)
		}
	})
	t.Run("file source with a watcher", func(t *testing.T) {
		src, err := NewFileSource(writeTempFile(t, "app.yaml", "k: v\n"))
		if err != nil {
			t.Fatalf("NewFileSource: %v", err)
		}
		r, err := New(WithWatcher(NewPollWatcher(time.Hour)), WithSource(src))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		t.Cleanup(func() { _ = r.Close() })
		live, err := NewLive[liveConfig](r)
		if err != nil {
			t.Fatalf("NewLive: %v", err)
		}
		_ = live.Close()
	})
}

func TestWithWatcher_KeepsPerSourceWatcher(t *testing.T) {
	registryWide := NewPollWatcher(time.Hour)
	perSource := NewPollWatcher(time.Minute)
	attach := map[string]func(r *Registry, s Source) error{
		"New":          nil,
		"AddSource":    func(r *Registry, s Source) error { return r.AddSource(s) },
		"InsertSource": func(r *Registry, s Source) error { return r.InsertSource(0, s) },
	}
	for name, add := range attach {
		t.Run(name, func(t *testing.T) {
			own, err := NewFileSource(writeTempFile(t, "own.yaml", "a: 1\n"), WithFileWatcher(perSource))
			if err != nil {
				t.Fatalf("NewFileSource: %v", err)
			}
			plain, err := NewFileSource(writeTempFile(t, "plain.yaml", "b: 2\n"))
			if err != nil {
				t.Fatalf("NewFileSource: %v", err)
			}
			opts := []Option{WithWatcher(registryWide)}
			if add == nil {
				opts = append(opts, WithSources(own, plain))
			}
			r, err := New(opts...)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			t.Cleanup(func() { _ = r.Close() })
			if add != nil {
				for _, s := range []Source{own, plain} {
					if err := add(r, s); err != nil {
						t.Fatalf("%s: %v", name, err)
					}
				}
			}
			if got := own.(*FileSource).watcherFactory(); got != perSource {
				t.Errorf("source with WithFileWatcher uses %p, want its own %p", got, perSource)
			}
			if got := plain.(*FileSource).watcherFactory(); got != registryWide {
				t.Errorf("source without a watcher uses %p, want the registry-wide %p", got, registryWide)
			}
		})
	}
}
