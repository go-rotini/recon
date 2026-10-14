package recon

import (
	"errors"
	"testing"
)

func TestNewWithCodecsWithoutWatchMatchesWithoutWatch(t *testing.T) {
	src := func() Source { return NewMapSource("m", map[string]any{"a": 1, "b": map[string]any{"c": "x"}}) }
	want, err := NewWithCodecs(nil, WithoutWatch(), WithSource(src()))
	if err != nil {
		t.Fatal(err)
	}
	got, err := NewWithCodecsWithoutWatch(nil, WithSource(src()))
	if err != nil {
		t.Fatal(err)
	}
	if got.Events() != nil || want.Events() != nil {
		t.Fatal("a one-shot registry has no events channel")
	}
	if !errors.Is(got.checkWatched(), ErrNotWatched) || got.checkWatched().Error() != want.checkWatched().Error() {
		t.Fatalf("checkWatched: %v vs %v", got.checkWatched(), want.checkWatched())
	}
	if g, w := got.AllKeys(), want.AllKeys(); len(g) != len(w) {
		t.Fatalf("keys %v vs %v", g, w)
	}
	// A source added later is not subscribed: there is no engine to subscribe it.
	if err := got.AddSource(NewMapSource("late", map[string]any{"d": true})); err != nil {
		t.Fatal(err)
	}
	if err := got.Close(); err != nil {
		t.Fatal(err)
	}
	if err := want.Close(); err != nil {
		t.Fatal(err)
	}
}
