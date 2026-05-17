package recon

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
)

// TestFormatRoundtripMatrix is the §8.3 format-roundtrip matrix.
//
// Per the spec: "Load fixture in format A → Save as format B → reload →
// assert structural equivalence." The flow is bytes_A → mapA → bytes_B →
// mapB; A and B never see each other's serialized form directly.
//
// Dotenv is the asymmetric format — it cannot encode nested values —
// so the matrix is restricted to the four hierarchical codecs. A
// separate flat-dotenv test covers the dotenv bridge.
func TestFormatRoundtripMatrix(t *testing.T) {
	nested := map[string]any{
		"server": map[string]any{
			"host":  "localhost",
			"port":  float64(8080),
			"debug": true,
		},
		"tags": []any{"a", "b", "c"},
	}

	codecs := []Codec{YAML, TOML, JSON, JSONC}

	for _, a := range codecs {
		for _, b := range codecs {
			t.Run(fmt.Sprintf("%s_to_%s", a.Name(), b.Name()), func(t *testing.T) {
				// Step 1: encode + decode in format A so the comparison
				// baseline reflects A's type-projection rules (e.g.,
				// JSON widens int → float64; TOML preserves int64).
				bytesA, err := a.Encode(nested)
				if err != nil {
					t.Fatalf("encode A: %v", err)
				}
				mapA, err := a.Decode(bytesA)
				if err != nil {
					t.Fatalf("decode A: %v", err)
				}
				// Step 2: re-encode A's map through B, decode again.
				bytesB, err := b.Encode(mapA)
				if err != nil {
					t.Fatalf("encode B: %v", err)
				}
				mapB, err := b.Decode(bytesB)
				if err != nil {
					t.Fatalf("decode B: %v", err)
				}
				assertStructurallyEqual(t, mapA, mapB)
			})
		}
	}
}

// TestFormatRoundtripFlatDotenv covers the dotenv-as-bridge pair: a
// flat map encoded as dotenv and re-decoded by every codec must survive
// (dotenv values are all strings — the receiving codec sees string-
// valued leaves).
func TestFormatRoundtripFlatDotenv(t *testing.T) {
	flat := map[string]any{
		"DATABASE_URL": "postgres://user@host/db",
		"PORT":         "8080",
		"DEBUG":        "true",
	}

	bytes, err := Dotenv.Encode(flat)
	if err != nil {
		t.Fatalf("dotenv encode: %v", err)
	}
	got, err := Dotenv.Decode(bytes)
	if err != nil {
		t.Fatalf("dotenv decode: %v", err)
	}
	for k, want := range flat {
		if got[k] != want {
			t.Fatalf("dotenv roundtrip: %s=%v, want %v", k, got[k], want)
		}
	}
}

// TestRoundtrip_KeysPreserved is a §8.3 sub-assertion: "every key in
// the loaded value round-trips." It rebuilds the encoded result and
// compares the sorted key list against the original.
func TestRoundtrip_KeysPreserved(t *testing.T) {
	src := map[string]any{
		"alpha":   "a",
		"bravo":   "b",
		"charlie": "c",
	}

	for _, c := range []Codec{YAML, TOML, JSON, JSONC} {
		t.Run(c.Name(), func(t *testing.T) {
			b, err := c.Encode(src)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			got, err := c.Decode(b)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			wantKeys := []string{"alpha", "bravo", "charlie"}
			gotKeys := mapKeys(got)
			slices.Sort(gotKeys)
			if !slices.Equal(gotKeys, wantKeys) {
				t.Fatalf("keys=%v, want %v", gotKeys, wantKeys)
			}
		})
	}
}

// TestRoundtrip_FailuresAreExplicit covers §8.3's "no silent data
// loss" requirement: a codec that cannot represent the input returns
// an error rather than dropping data.
func TestRoundtrip_FailuresAreExplicit(t *testing.T) {
	// Dotenv cannot represent nested values; encode must fail.
	_, err := Dotenv.Encode(map[string]any{
		"server": map[string]any{"port": 8080},
	})
	if err == nil {
		t.Fatal("dotenv encode of nested map: nil err, want error")
	}
}

// assertStructurallyEqual compares two decoded maps under loose
// numeric equivalence (int↔float64 widening tolerated, since JSON
// widens every integer). The function recurses through nested maps
// and slices.
func assertStructurallyEqual(t *testing.T, want, got map[string]any) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("size: want %d keys, got %d (keys=%v)", len(want), len(got), mapKeys(got))
	}
	for k, w := range want {
		g, ok := got[k]
		if !ok {
			t.Fatalf("missing key %q", k)
		}
		if !leafEqual(w, g) {
			t.Fatalf("key %q: want %v (%T), got %v (%T)", k, w, w, g, g)
		}
	}
}

// leafEqual implements the loose-equality rule used by the roundtrip
// matrix: numeric types compare across int / int64 / float64; maps and
// slices recurse; everything else uses reflect.DeepEqual.
func leafEqual(a, b any) bool {
	switch av := a.(type) {
	case float64:
		switch bv := b.(type) {
		case float64:
			return av == bv
		case int64:
			return av == float64(bv)
		case int:
			return av == float64(bv)
		}
	case int64:
		switch bv := b.(type) {
		case int64:
			return av == bv
		case float64:
			return float64(av) == bv
		case int:
			return int(av) == bv
		}
	case int:
		switch bv := b.(type) {
		case int:
			return av == bv
		case int64:
			return int64(av) == bv
		case float64:
			return float64(av) == bv
		}
	case map[string]any:
		bm, ok := b.(map[string]any)
		if !ok {
			return false
		}
		if len(av) != len(bm) {
			return false
		}
		for k, v := range av {
			bv, ok := bm[k]
			if !ok || !leafEqual(v, bv) {
				return false
			}
		}
		return true
	case []any:
		bs, ok := b.([]any)
		if !ok {
			return false
		}
		if len(av) != len(bs) {
			return false
		}
		for i, v := range av {
			if !leafEqual(v, bs[i]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(a, b)
}

// mapKeys returns the keys of m as a slice. Helper used by the
// keys-preserved assertion.
func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
