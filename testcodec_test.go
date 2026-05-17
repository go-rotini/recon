package recon

import (
	"encoding/json"
	"fmt"
)

// jsonTestCodec is a small JSON-backed codec used by the Phase 3 tests.
// Phase 4 will ship bundled codecs and remove this; until then it gives
// the BufferSource tests something concrete to decode.
type jsonTestCodec struct{}

func (jsonTestCodec) Name() string         { return "json" }
func (jsonTestCodec) Extensions() []string { return []string{".json"} }

func (jsonTestCodec) Decode(data []byte) (map[string]any, error) {
	var out map[string]any
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("jsonTestCodec.Decode: %w", err)
	}
	if out == nil {
		return map[string]any{}, nil
	}
	return out, nil
}

func (jsonTestCodec) Encode(v map[string]any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("jsonTestCodec.Encode: %w", err)
	}
	return b, nil
}
