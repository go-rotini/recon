package recon

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"testing"
)

// FuzzCodecDecode runs every bundled codec on the input. Invariants:
// no panic; a success returns a non-nil map that [NewValue] accepts;
// a failure that reports a position reports line 1 or later.
func FuzzCodecDecode(f *testing.F) {
	for _, s := range []string{
		"", "{}", `{"a": 1}`, `{"a": [1, 2.5, "x", null, true]}`, `{"a": 1e400}`,
		`{"a": 18446744073709551615, "b": 123456789012345678901234567890}`,
		"{\"a\": 1, \"a\": 2}", "// c\n{\"a\": 1,}", "a: 1\nb: [x, y]\n", "a: 1\n---\nb: 2\n",
		"x: &a {k: v}\ny: *a\n", "a = 1\n[t]\nb = 2024-01-02T03:04:05Z\n", "A=1\nB=\"two\"\n",
		"{\"a\": \"\xff\"}", "\xef\xbb\xbf{}",
	} {
		f.Add([]byte(s))
	}
	codecs := []Codec{JSON, JSONC, YAML, TOML, Dotenv}
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, c := range codecs {
			m, err := c.Decode(data)
			if err != nil {
				if pe, ok := errors.AsType[PositionError](err); ok {
					if line, _ := pe.Position(); line < 1 {
						t.Fatalf("%s: position line %d for %v", c.Name(), line, err)
					}
				}
				continue
			}
			if m == nil {
				t.Fatalf("%s: nil map without an error", c.Name())
			}
			_ = NewValue(m)
		}
	})
}

// FuzzJSONAgainstStdlib checks the [JSON] codec against encoding/json:
// every document encoding/json rejects is rejected; a document it
// accepts is accepted unless recon's stricter rules (an object root, no
// repeated key, valid UTF-8) apply; and both read the same values,
// numbers compared as float64, with a null document read as an empty map.
func FuzzJSONAgainstStdlib(f *testing.F) {
	for _, s := range []string{
		`{}`, `{"a": 1}`, `{"a": {"b": [1, -2, 3.5e2, "s", false, null]}}`,
		`{"n": 9007199254740993, "u": 18446744073709551615, "x": 1e400}`,
		`{"n": 123456789012345678901234567890}`, `{"0": 10000000000000000, "0000": 1000000000000000000000}`,
		`{"a": 1, "a": 2}`, `[1]`, `{"a": 1} x`, `null`, " null\n", "{\"a\": \"\xff\"}", `{"a": "\ud800"}`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var want any
		stdErr := json.Unmarshal(stripBOM(data), &want)
		got, err := JSON.Decode(data)
		switch {
		case stdErr != nil && err == nil:
			if !isJSONSpace(stripBOM(data)) {
				t.Fatalf("recon accepts what encoding/json rejects (%v): %q", stdErr, data)
			}
		case stdErr == nil && err != nil:
			if _, isObj := want.(map[string]any); isObj && !errors.Is(err, errJSONDuplicateKey) && !errors.Is(err, errInvalidUTF8) {
				t.Fatalf("recon rejects what encoding/json accepts: %v: %q", err, data)
			}
		case stdErr == nil && err == nil:
			if want == nil { // a null document decodes as an empty map
				if len(got) != 0 {
					t.Fatalf("recon decodes a null document as %#v, want an empty map", got)
				}
				return
			}
			if !jsonSame(got, want) {
				t.Fatalf("values differ: recon %#v, encoding/json %#v", got, want)
			}
		}
	})
}

// jsonSame compares a recon JSON value with encoding/json's.
func jsonSame(got, want any) bool {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok || len(g) != len(w) {
			return false
		}
		for k, wv := range w {
			if gv, ok := g[k]; !ok || !jsonSame(gv, wv) {
				return false
			}
		}
		return true
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return false
		}
		for i := range w {
			if !jsonSame(g[i], w[i]) {
				return false
			}
		}
		return true
	case float64:
		var f float64
		switch g := got.(type) {
		case int64:
			f = float64(g)
		case uint64:
			f = float64(g)
		case float64:
			f = g
		case json.Number:
			p, err := strconv.ParseFloat(string(g), 64)
			if err != nil && !errors.Is(err, strconv.ErrRange) {
				return false
			}
			f = p
		case string:
			p, err := strconv.ParseFloat(g, 64)
			if err != nil && !errors.Is(err, strconv.ErrRange) {
				return false
			}
			f = p
		default:
			return false
		}
		return f == w || math.IsNaN(f) && math.IsNaN(w)
	default:
		return got == want
	}
}
