package recon

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/big"
	"reflect"
	"slices"
	"strings"

	"github.com/go-rotini/yaml"
)

// yamlCodec wraps go-rotini/yaml and normalizes the decoded map so
// the registry never sees a yaml.Node.
type yamlCodec struct{}

// YAML is the [Codec] for YAML 1.2.2 documents (and the KYAML strict
// subset), part of [DefaultCodecs]. Integers decode as [JSON]'s do: as
// int64, as uint64 above the int64 range, and as a [json.Number] (in
// decimal) outside both ranges; a float beyond the float64 range is a
// positioned error, as in JSON.
var YAML Codec = yamlCodec{}

func (yamlCodec) Name() string         { return FormatYAML }
func (yamlCodec) Extensions() []string { return []string{".yaml", ".yml"} }

// errYAMLSecondDocument reports a stream with more than one document
// holding a value.
var errYAMLSecondDocument = errors.New("found a second document; a configuration file holds one")

// Decode parses data as one YAML document. The root must be a mapping;
// sequences or scalars at the top level are rejected. A document with
// no value (empty, comments only, "~" or a bare "---") decodes as an
// empty map. A key repeated in one mapping is an error, as is a second
// document holding a value; empty documents around the one that holds
// the configuration are ignored.
func (yamlCodec) Decode(data []byte) (map[string]any, error) {
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	dec := yaml.NewDecoder(bytes.NewReader(data), yaml.WithDisallowDuplicateKey())
	var v any
	found := false
	for i := 0; ; i++ {
		var doc any
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, yamlDecodeError(err)
		}
		if doc == nil {
			continue
		}
		if found {
			line, col := yamlDocumentPosition(data, i)
			return nil, &codecError{format: FormatYAML, err: errYAMLSecondDocument, line: line, col: col, msg: errYAMLSecondDocument.Error()}
		}
		v, found = doc, true
	}
	if !found {
		return map[string]any{}, nil
	}
	m, ok := normalizeMap(v)
	if !ok {
		return nil, fmt.Errorf("%w: yaml root must be a mapping, got %T",
			ErrUnsupportedFormat, v)
	}
	var overflow yamlOverflow
	yamlNumbers(m, &overflow)
	if overflow.texts != nil {
		return nil, yamlOverflowError(data, overflow.texts)
	}
	return m, nil
}

// yamlDocumentPosition is where the i-th document of data starts, or
// 0, 0 when the parser does not say.
func yamlDocumentPosition(data []byte, i int) (line, col int) {
	f, err := yaml.Parse(data)
	if err != nil || i >= len(f.Docs) {
		return 0, 0
	}
	return f.Docs[i].Pos.Line, f.Docs[i].Pos.Column
}

func (yamlCodec) Encode(v map[string]any) ([]byte, error) {
	b, err := yaml.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("recon: yaml encode: %w", err)
	}
	return b, nil
}

// yamlDecodeError gives every parser error that knows its position a
// line and column.
func yamlDecodeError(err error) error {
	if e, ok := errors.AsType[*yaml.SyntaxError](err); ok {
		return &codecError{format: FormatYAML, err: err, line: e.Pos.Line, col: e.Pos.Column, msg: e.Message}
	}
	if e, ok := errors.AsType[*yaml.DuplicateKeyError](err); ok && e.Pos.Line > 0 {
		return atPosition(FormatYAML, err, e.Pos.Line, e.Pos.Column)
	}
	if e, ok := errors.AsType[*yaml.CycleError](err); ok && e.Pos.Line > 0 {
		return atPosition(FormatYAML, err, e.Pos.Line, e.Pos.Column)
	}
	if e, ok := errors.AsType[*yaml.KYAMLError](err); ok && len(e.Errors) > 0 && e.Errors[0].Pos.Line > 0 {
		v := e.Errors[0]
		return &codecError{format: FormatYAML, err: err, line: v.Pos.Line, col: v.Pos.Column, msg: v.Message}
	}
	if e, ok := errors.AsType[*yaml.TypeError](err); ok && len(e.Errors) == 1 {
		var line, col int
		if _, serr := fmt.Sscanf(e.Errors[0], "line %d, column %d:", &line, &col); serr == nil && line > 0 {
			return &codecError{format: FormatYAML, err: err, line: line, col: col, msg: stripPositionPrefix(e.Errors[0], FormatYAML)}
		}
	}
	// Any other error that leads with its line, such as an alias-expansion
	// limit, keeps that line.
	var line int
	if _, serr := fmt.Sscanf(err.Error(), "yaml: line %d:", &line); serr == nil && line > 0 {
		return atPosition(FormatYAML, err, line, 0)
	}
	return fmt.Errorf("recon: yaml decode: %w", err)
}

// yamlPkgPath is the import path of the yaml package.
const yamlPkgPath = "github.com/go-rotini/yaml"

// yamlOverflow collects the text of each float beyond the float64
// range that [yamlNumbers] finds.
type yamlOverflow struct{ texts map[string]struct{} }

// yamlNumbers replaces, in place, each integer the yaml package hands
// back as its own Number text (one outside the int64 and uint64
// ranges) by a [json.Number] in decimal. The text of a float beyond the
// float64 range is added to overflow.
func yamlNumbers(v any, overflow *yamlOverflow) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			x[k] = yamlNumbers(e, overflow)
		}
		return x
	case []any:
		for i, e := range x {
			x[i] = yamlNumbers(e, overflow)
		}
		return x
	}
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || rv.Kind() != reflect.String || rv.Type().Name() != "Number" || rv.Type().PkgPath() != yamlPkgPath {
		return v
	}
	s := rv.String()
	body := strings.TrimLeft(s, "+-")
	base := 10
	if len(body) > 1 && body[0] == '0' && (body[1] == 'x' || body[1] == 'o') {
		base = 0
	}
	if z, ok := new(big.Int).SetString(strings.TrimPrefix(s, "+"), base); ok {
		return json.Number(z.String())
	}
	if overflow.texts == nil {
		overflow.texts = map[string]struct{}{}
	}
	overflow.texts[s] = struct{}{}
	return s
}

// yamlOverflowError reports the first plain scalar in data whose text
// is in overflow, a float beyond the float64 range, as [JSON] does.
func yamlOverflowError(data []byte, overflow map[string]struct{}) error {
	var at *yaml.Node
	if f, err := yaml.Parse(data); err == nil {
		for _, doc := range f.Docs {
			yaml.Walk(doc, func(n *yaml.Node) bool {
				if at != nil {
					return false
				}
				if _, hit := overflow[n.Value]; hit && n.Kind == yaml.ScalarNode && n.Style == yaml.PlainStyle {
					at = n
				}
				return true
			})
		}
	}
	if at == nil {
		return fmt.Errorf("recon: yaml decode: %w", errNumberRange(slices.Min(slices.Collect(maps.Keys(overflow)))))
	}
	err := errNumberRange(at.Value)
	return &codecError{format: FormatYAML, err: err, line: at.Pos.Line, col: at.Pos.Column, msg: err.Error()}
}
