package recon

import (
	"errors"
	"slices"
	"testing"
)

func TestConfigs_RegisterAndGet(t *testing.T) {
	c := NewConfigs()
	db, _ := New()
	srv, _ := New()
	if err := c.Register("database", db); err != nil {
		t.Fatalf("Register database: %v", err)
	}
	if err := c.Register("server", srv); err != nil {
		t.Fatalf("Register server: %v", err)
	}

	got, ok := c.Get("database")
	if !ok || got != db {
		t.Fatalf("Get(database) ok=%v got=%p (want %p)", ok, got, db)
	}
	if _, ok := c.Get("nope"); ok {
		t.Fatal("Get(nope) ok=true")
	}

	if names := c.Names(); !slices.Equal(names, []string{"database", "server"}) {
		t.Fatalf("Names()=%v", names)
	}

	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestConfigs_Register_DuplicateName(t *testing.T) {
	c := NewConfigs()
	t.Cleanup(func() { _ = c.Close() })
	r1, _ := New()
	r2, _ := New()
	if err := c.Register("a", r1); err != nil {
		t.Fatal(err)
	}
	err := c.Register("a", r2)
	if !errors.Is(err, ErrSourceConflict) {
		t.Fatalf("err=%v, want ErrSourceConflict", err)
	}
}

func TestConfigs_Register_EmptyName(t *testing.T) {
	c := NewConfigs()
	t.Cleanup(func() { _ = c.Close() })
	r, _ := New()
	err := c.Register("", r)
	if !errors.Is(err, ErrSourceConflict) {
		t.Fatalf("err=%v, want ErrSourceConflict", err)
	}
}

func TestConfigs_Register_Nil(t *testing.T) {
	c := NewConfigs()
	t.Cleanup(func() { _ = c.Close() })
	err := c.Register("x", nil)
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("err=%v, want ErrInvalidPath", err)
	}
}

func TestConfigs_MustGet(t *testing.T) {
	c := NewConfigs()
	t.Cleanup(func() { _ = c.Close() })
	r, _ := New()
	_ = c.Register("a", r)

	if got := c.MustGet("a"); got != r {
		t.Fatalf("MustGet(a) returned wrong registry")
	}

	defer func() {
		rec := recover()
		if rec == nil {
			t.Fatal("MustGet(missing) did not panic")
		}
		err, ok := rec.(error)
		if !ok || !errors.Is(err, ErrSourceConflict) {
			t.Fatalf("panic=%v, want wrap of ErrSourceConflict", rec)
		}
	}()
	c.MustGet("missing")
}

func TestConfigs_Remove(t *testing.T) {
	c := NewConfigs()
	t.Cleanup(func() { _ = c.Close() })
	r, _ := New()
	_ = c.Register("a", r)

	if err := c.Remove("a"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, ok := c.Get("a"); ok {
		t.Fatal("Get(a) still ok after Remove")
	}
	// Idempotent.
	if err := c.Remove("a"); err != nil {
		t.Fatalf("second Remove: %v", err)
	}
	// The removed registry was closed.
	if err := r.Set("k", "v"); !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("Set after Remove err=%v, want ErrRegistryClosed", err)
	}
}

func TestConfigs_Close_Idempotent(t *testing.T) {
	c := NewConfigs()
	r, _ := New()
	_ = c.Register("a", r)
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
