package recon

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"

	"github.com/go-rotini/toml"
)

type tomlCodec struct{}

// TOML is the [Codec] for TOML documents, part of [DefaultCodecs].
// TOML integers are 64-bit signed, so Encode writes an integer outside
// that range (an unsigned integer above [math.MaxInt64], or a
// [json.Number]) as its decimal text, a string; it binds back into a
// numeric field unchanged.
var TOML Codec = tomlCodec{}

func (tomlCodec) Name() string         { return FormatTOML }
func (tomlCodec) Extensions() []string { return []string{".toml"} }

func (tomlCodec) Decode(data []byte) (map[string]any, error) {
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var v any
	if err := toml.Unmarshal(data, &v); err != nil {
		return nil, tomlDecodeError(err)
	}
	m, ok := normalizeMap(v)
	if !ok {
		return nil, fmt.Errorf("%w: toml root must be a table, got %T",
			ErrUnsupportedFormat, v)
	}
	return m, nil
}

func (tomlCodec) Encode(v map[string]any) ([]byte, error) {
	if nv, changed := tomlBigUints(v); changed {
		v, _ = nv.(map[string]any)
	}
	b, err := toml.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("recon: toml encode: %w", err)
	}
	return b, nil
}

// tomlDecodeError gives every parser error that knows its position a
// line and column.
func tomlDecodeError(err error) error {
	if e, ok := errors.AsType[*toml.SyntaxError](err); ok {
		return &codecError{format: FormatTOML, err: err, line: e.Pos.Line, col: e.Pos.Column, msg: e.Message}
	}
	var pos toml.Position
	if e, ok := errors.AsType[*toml.DuplicateKeyError](err); ok {
		pos = e.Pos
	} else if e, ok := errors.AsType[*toml.OverflowError](err); ok {
		pos = e.Pos
	} else if e, ok := errors.AsType[*toml.InlineTableError](err); ok {
		pos = e.Pos
	} else if e, ok := errors.AsType[*toml.ConflictError](err); ok {
		pos = e.Pos
	}
	if pos.Line > 0 {
		return atPosition(FormatTOML, err, pos.Line, pos.Column)
	}
	return fmt.Errorf("recon: toml decode: %w", err)
}

// tomlBigUints returns v with every unsigned integer above
// [math.MaxInt64] and every [json.Number] inside maps and lists
// replaced by its decimal text,
// and whether anything was replaced. Maps and lists are copied only
// where something is.
func tomlBigUints(v any) (any, bool) {
	switch t := v.(type) {
	case uint64:
		if t > math.MaxInt64 {
			return strconv.FormatUint(t, 10), true
		}
	case uint:
		if uint64(t) > math.MaxInt64 {
			return strconv.FormatUint(uint64(t), 10), true
		}
	case json.Number:
		return t.String(), true
	case map[string]any:
		var out map[string]any
		for k, e := range t {
			if ne, changed := tomlBigUints(e); changed {
				if out == nil {
					out = maps.Clone(t)
				}
				out[k] = ne
			}
		}
		return out, out != nil
	case []any:
		var out []any
		for i, e := range t {
			if ne, changed := tomlBigUints(e); changed {
				if out == nil {
					out = slices.Clone(t)
				}
				out[i] = ne
			}
		}
		return out, out != nil
	}
	return v, false
}
