package recon

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/go-rotini/yaml"
)

// yamlCodec wraps go-rotini/yaml and normalizes the decoded map so
// the registry never sees a yaml.Node.
type yamlCodec struct{}

// YAML is the [Codec] for YAML 1.2.2 documents (and the KYAML strict
// subset), part of [DefaultCodecs]. Integers decode as [JSON]'s do: as
// int64, or as uint64 above the int64 range.
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
