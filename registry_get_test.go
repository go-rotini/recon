package recon

import (
	"errors"
	"testing"
	"time"
)

func TestRegistry_GetPath(t *testing.T) {
	r := newRegistry(t)
	if err := r.Set("server.port", 8080); err != nil {
		t.Fatalf("Set: %v", err)
	}
	v, ok, err := r.GetPath(ParsePath("server.port"))
	if err != nil {
		t.Fatalf("GetPath: %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if got, _ := v.AsInt64(); got != 8080 {
		t.Fatalf("got %d, want 8080", got)
	}
}

func TestRegistry_GetPath_MissingKey(t *testing.T) {
	r := newRegistry(t)
	_, ok, err := r.GetPath(ParsePath("missing.key"))
	if err != nil {
		t.Fatalf("GetPath: %v", err)
	}
	if ok {
		t.Fatal("ok = true, want false for missing key")
	}
}

func TestRegistry_GetPath_OnSubView(t *testing.T) {
	// GetPath on a sub-view goes through fullPath; exercises that
	// helper alongside the path-typed accessor.
	r := newRegistry(t)
	if err := r.Set("server.port", 8080); err != nil {
		t.Fatalf("Set: %v", err)
	}
	sub := r.Sub("server")
	v, ok, err := sub.GetPath(ParsePath("port"))
	if err != nil {
		t.Fatalf("GetPath: %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true on sub-view")
	}
	if got, _ := v.AsInt64(); got != 8080 {
		t.Fatalf("got %d, want 8080", got)
	}
}

func TestRegistry_GetTime_Native(t *testing.T) {
	r := newRegistry(t)
	when := time.Date(2026, 5, 17, 12, 34, 56, 0, time.UTC)
	if err := r.Set("event.at", when); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok, err := r.GetTime("event.at")
	if err != nil {
		t.Fatalf("GetTime: %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if !got.Equal(when) {
		t.Fatalf("got %v, want %v", got, when)
	}
}

func TestRegistry_GetTime_FromRFC3339String(t *testing.T) {
	r := newRegistry(t)
	if err := r.Set("event.at", "2026-05-17T12:34:56Z"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok, err := r.GetTime("event.at")
	if err != nil {
		t.Fatalf("GetTime: %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if got.Year() != 2026 {
		t.Fatalf("got year %d, want 2026", got.Year())
	}
}

func TestRegistry_GetTime_Missing(t *testing.T) {
	r := newRegistry(t)
	_, ok, err := r.GetTime("missing")
	if err != nil {
		t.Fatalf("GetTime: %v", err)
	}
	if ok {
		t.Fatal("ok = true, want false")
	}
}

func TestRegistry_GetTime_WrongKind(t *testing.T) {
	r := newRegistry(t)
	if err := r.Set("event.at", true); err != nil {
		t.Fatalf("Set: %v", err)
	}
	_, _, err := r.GetTime("event.at")
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("err = %v, want wrapping ErrTypeMismatch", err)
	}
}

func TestRegistry_GetStringMap(t *testing.T) {
	r := newRegistry(t)
	if err := r.Set("labels", map[string]any{"env": "prod", "tier": "api"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok, err := r.GetStringMap("labels")
	if err != nil {
		t.Fatalf("GetStringMap: %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if got["env"] != "prod" || got["tier"] != "api" {
		t.Fatalf("got %v, want env=prod tier=api", got)
	}
}

func TestRegistry_GetStringMap_WrongKind(t *testing.T) {
	r := newRegistry(t)
	if err := r.Set("labels", "not-a-map"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	_, _, err := r.GetStringMap("labels")
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("err = %v, want wrapping ErrTypeMismatch", err)
	}
}

func TestRegistry_GetStringMap_NonStringElement(t *testing.T) {
	r := newRegistry(t)
	// Mix a non-string-projectable element (a nested map) into the
	// MapKind value. Element-side AsString will fail and surface as
	// the entry error.
	if err := r.Set("labels", map[string]any{
		"k": map[string]any{"nested": "value"},
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	_, _, err := r.GetStringMap("labels")
	if err == nil {
		t.Fatal("expected error for non-string element")
	}
}

func TestRegistry_GetStringMap_Missing(t *testing.T) {
	r := newRegistry(t)
	_, ok, err := r.GetStringMap("missing")
	if err != nil {
		t.Fatalf("GetStringMap: %v", err)
	}
	if ok {
		t.Fatal("ok = true, want false")
	}
}

func TestRegistry_GetAny(t *testing.T) {
	r := newRegistry(t)
	if err := r.Set("port", 8080); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok, err := r.GetAny("port")
	if err != nil {
		t.Fatalf("GetAny: %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true")
	}
	// NewValue widens int to int64.
	if got != int64(8080) {
		t.Fatalf("got %v (%T), want int64(8080)", got, got)
	}
}

func TestRegistry_GetAny_Missing(t *testing.T) {
	r := newRegistry(t)
	_, ok, err := r.GetAny("missing")
	if err != nil {
		t.Fatalf("GetAny: %v", err)
	}
	if ok {
		t.Fatal("ok = true, want false")
	}
}

func TestRegistry_GetString_WrongKind(t *testing.T) {
	r := newRegistry(t)
	if err := r.Set("port", 8080); err != nil {
		t.Fatalf("Set: %v", err)
	}
	_, _, err := r.GetString("port")
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("err = %v, want wrapping ErrTypeMismatch (int != string)", err)
	}
}

func TestRegistry_GetInt_WrongKind(t *testing.T) {
	r := newRegistry(t)
	if err := r.Set("port", "not-a-number"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	_, _, err := r.GetInt("port")
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("err = %v, want wrapping ErrTypeMismatch", err)
	}
}

func TestRegistry_GetFloat_WrongKind(t *testing.T) {
	r := newRegistry(t)
	if err := r.Set("ratio", "not-a-number"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	_, _, err := r.GetFloat("ratio")
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("err = %v, want wrapping ErrTypeMismatch", err)
	}
}

func TestRegistry_GetBool_WrongKind(t *testing.T) {
	r := newRegistry(t)
	if err := r.Set("flag", "not-a-bool"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	_, _, err := r.GetBool("flag")
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("err = %v, want wrapping ErrTypeMismatch", err)
	}
}

func TestRegistry_GetDuration_FromString(t *testing.T) {
	r := newRegistry(t)
	if err := r.Set("timeout", "250ms"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok, err := r.GetDuration("timeout")
	if err != nil {
		t.Fatalf("GetDuration: %v", err)
	}
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if got != 250*time.Millisecond {
		t.Fatalf("got %v, want 250ms", got)
	}
}

func TestRegistry_GetDuration_WrongKind(t *testing.T) {
	r := newRegistry(t)
	if err := r.Set("timeout", true); err != nil {
		t.Fatalf("Set: %v", err)
	}
	_, _, err := r.GetDuration("timeout")
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("err = %v, want wrapping ErrTypeMismatch", err)
	}
}

func TestRegistry_GetStringSlice_WrongKind(t *testing.T) {
	r := newRegistry(t)
	if err := r.Set("tags", true); err != nil {
		t.Fatalf("Set: %v", err)
	}
	_, _, err := r.GetStringSlice("tags")
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("err = %v, want wrapping ErrTypeMismatch", err)
	}
}

func TestRegistry_GetStringSlice_NonStringElement(t *testing.T) {
	r := newRegistry(t)
	if err := r.Set("tags", []any{int64(1), int64(2)}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	_, _, err := r.GetStringSlice("tags")
	if err == nil {
		t.Fatal("expected error for non-string element")
	}
}

func TestRegistry_Get_ClosedRegistry(t *testing.T) {
	r := newRegistry(t)
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	_, _, err := r.Get("anything")
	if !errors.Is(err, ErrRegistryClosed) {
		t.Fatalf("err = %v, want ErrRegistryClosed", err)
	}
}

func TestRegistry_IsSet_ClosedRegistry(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("port", 8080)
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if r.IsSet("port") {
		t.Fatal("IsSet on closed registry should return false")
	}
}

func TestRegistry_AllKeys_ClosedRegistry(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("port", 8080)
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := r.AllKeys(); got != nil {
		t.Fatalf("AllKeys on closed registry: got %v, want nil", got)
	}
}

func TestGenericGet_AllSupportedTypes(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("s", "hello")
	_ = r.Set("b", true)
	_ = r.Set("i", 42)
	_ = r.Set("i64", int64(42))
	_ = r.Set("f", 3.14)
	_ = r.Set("t", time.Date(2026, 5, 17, 0, 0, 0, 0, time.UTC))
	_ = r.Set("d", 250*time.Millisecond)
	_ = r.Set("slice", []any{"a", "b", "c"})

	if v, _, _ := Get[string](r, "s"); v != "hello" {
		t.Errorf("Get[string]: %q, want hello", v)
	}
	if v, _, _ := Get[bool](r, "b"); !v {
		t.Errorf("Get[bool]: got false, want true")
	}
	if v, _, _ := Get[int](r, "i"); v != 42 {
		t.Errorf("Get[int]: %d, want 42", v)
	}
	if v, _, _ := Get[int64](r, "i64"); v != 42 {
		t.Errorf("Get[int64]: %d, want 42", v)
	}
	if v, _, _ := Get[float64](r, "f"); v != 3.14 {
		t.Errorf("Get[float64]: %v, want 3.14", v)
	}
	if v, _, _ := Get[time.Time](r, "t"); v.Year() != 2026 {
		t.Errorf("Get[time.Time]: %v, want 2026", v.Year())
	}
	if v, _, _ := Get[time.Duration](r, "d"); v != 250*time.Millisecond {
		t.Errorf("Get[time.Duration]: %v, want 250ms", v)
	}
	if v, _, _ := Get[[]string](r, "slice"); len(v) != 3 || v[0] != "a" {
		t.Errorf("Get[[]string]: %v, want [a b c]", v)
	}
	// Value pass-through.
	if v, _, _ := Get[Value](r, "s"); v.Kind() != StringKind {
		t.Errorf("Get[Value]: kind %v, want StringKind", v.Kind())
	}
}

func TestGenericGet_UnsupportedType(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("x", "y")
	type Unsupported struct{ S string }
	_, _, err := Get[Unsupported](r, "x")
	if !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("err = %v, want wrapping ErrTypeMismatch", err)
	}
}

func TestGenericGet_MissingKey(t *testing.T) {
	r := newRegistry(t)
	v, ok, err := Get[int](r, "missing")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Fatal("ok = true, want false")
	}
	if v != 0 {
		t.Fatalf("got %d, want zero", v)
	}
}

func TestMustGet_MissingKeyPanics(t *testing.T) {
	r := newRegistry(t)
	defer func() {
		if recover() == nil {
			t.Fatal("MustGet on missing key did not panic")
		}
	}()
	MustGet[int](r, "missing")
}

func TestMustGet_CoercionErrorPanics(t *testing.T) {
	r := newRegistry(t)
	_ = r.Set("port", true)
	defer func() {
		if recover() == nil {
			t.Fatal("MustGet with coercion error did not panic")
		}
	}()
	MustGet[int](r, "port")
}

func TestGenericGet_StringSliceWithNonStringElement(t *testing.T) {
	r := newRegistry(t)
	// Mixed slice — element coercion fails on the nested map.
	_ = r.Set("xs", []any{map[string]any{"nested": "value"}})
	_, _, err := Get[[]string](r, "xs")
	if err == nil {
		t.Fatal("expected error for non-string element")
	}
}
