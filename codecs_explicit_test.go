package recon

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-rotini/yaml"
)

func codecNames(cs *Codecs) []string {
	names := cs.Names()
	slices.Sort(names)
	return names
}

func TestNewWithCodecs_UsesOnlyPassedCodecs(t *testing.T) {
	r, err := NewWithCodecs(NewCodecs(YAML), WithoutWatch())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	if got := codecNames(r.state.opts.codecs); !slices.Equal(got, []string{FormatYAML}) {
		t.Fatalf("codecs = %v, want [yaml]", got)
	}
	if _, err := r.SaveString(WithSaveFormat(FormatTOML)); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("Save as toml: err = %v, want ErrUnsupportedFormat", err)
	}
}

func TestNewWithCodecs_NilHoldsNone(t *testing.T) {
	r, err := NewWithCodecs(nil, WithoutWatch(),
		WithSource(NewMapSource("m", map[string]any{"doc": `{"a": 1}`})))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	if got := r.state.opts.codecs.Names(); len(got) != 0 {
		t.Fatalf("codecs = %v, want none", got)
	}
	var target struct {
		Doc map[string]any `recon:"doc,format=json"`
	}
	err = r.Bind(&target)
	if !errors.Is(err, ErrUnsupportedFormat) || !strings.Contains(err.Error(), `"json"`) {
		t.Fatalf("Bind: err = %v, want ErrUnsupportedFormat naming json", err)
	}
}

func TestNewWithCodecs_CodecOptions(t *testing.T) {
	tests := []struct {
		name string
		opts []Option
		want []string
	}{
		{"WithCodec adds", []Option{WithCodec(JSON)}, []string{FormatJSON, FormatYAML}},
		{"WithoutCodec removes", []Option{WithoutCodec(FormatYAML)}, []string{}},
		{"WithCodecs replaces", []Option{WithCodecs(NewCodecs(TOML))}, []string{FormatTOML}},
		{"WithCodecs nil then WithCodec", []Option{WithCodecs(nil), WithCodec(JSON)}, []string{FormatJSON}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, err := NewWithCodecs(NewCodecs(YAML), append([]Option{WithoutWatch()}, tc.opts...)...)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = r.Close() })
			if got := codecNames(r.state.opts.codecs); !slices.Equal(got, tc.want) {
				t.Fatalf("codecs = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNew_CodecOptionsStartFromDefaults(t *testing.T) {
	custom := stubCodec{name: "custom"}
	tests := []struct {
		name string
		opts []Option
		want []string
	}{
		{"none", nil, []string{FormatDotenv, FormatJSON, FormatJSONC, FormatTOML, FormatYAML}},
		{"WithCodec", []Option{WithCodec(custom)}, []string{"custom", FormatDotenv, FormatJSON, FormatJSONC, FormatTOML, FormatYAML}},
		{"WithoutCodec", []Option{WithoutCodec(FormatTOML), WithoutCodec(FormatDotenv)}, []string{FormatJSON, FormatJSONC, FormatYAML}},
		{"WithCodec then WithCodecs", []Option{WithCodec(custom), WithCodecs(NewCodecs(JSON))}, []string{FormatJSON}},
		{"WithCodecs then WithCodec", []Option{WithCodecs(NewCodecs(JSON)), WithCodec(custom)}, []string{"custom", FormatJSON}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newRegistry(t, tc.opts...)
			if got := codecNames(r.state.opts.codecs); !slices.Equal(got, tc.want) {
				t.Fatalf("codecs = %v, want %v", got, tc.want)
			}
		})
	}
}

type stubCodec struct{ name string }

func (c stubCodec) Name() string                        { return c.name }
func (stubCodec) Extensions() []string                  { return nil }
func (stubCodec) Decode([]byte) (map[string]any, error) { return map[string]any{}, nil }
func (stubCodec) Encode(map[string]any) ([]byte, error) { return nil, nil }

func TestNewFileSourceWithCodecs(t *testing.T) {
	yamlOnly := NewCodecs(YAML)
	yamlPath := writeTempFile(t, "app.yaml", "port: 8080\n")
	tomlPath := writeTempFile(t, "app.toml", "port = 8080\n")

	src, err := NewFileSourceWithCodecs(yamlOnly, yamlPath)
	if err != nil {
		t.Fatalf("yaml file: %v", err)
	}
	if v, ok, err := src.Get(ParsePath("port")); err != nil || !ok || v.String() != "8080" {
		t.Fatalf("port = %v, %v, %v", v, ok, err)
	}

	_, err = NewFileSourceWithCodecs(yamlOnly, tomlPath)
	if !errors.Is(err, ErrUnsupportedFormat) || !strings.Contains(err.Error(), `".toml"`) {
		t.Fatalf("toml file without TOML: err = %v, want ErrUnsupportedFormat naming .toml", err)
	}
	_, err = NewFileSourceWithCodecs(yamlOnly, yamlPath, WithFileFormat(FormatTOML))
	if !errors.Is(err, ErrUnsupportedFormat) || !strings.Contains(err.Error(), `"toml"`) {
		t.Fatalf("toml format without TOML: err = %v, want ErrUnsupportedFormat naming toml", err)
	}
	if _, err := NewFileSourceWithCodecs(nil, yamlPath); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("nil codecs: err = %v, want ErrUnsupportedFormat", err)
	}
	if _, err := NewFileSourceWithCodecs(nil, tomlPath, WithFileCodec(TOML)); err != nil {
		t.Fatalf("nil codecs with WithFileCodec: %v", err)
	}
}

func TestNewFileSourceFSWithCodecs(t *testing.T) {
	fsys := fstest.MapFS{
		"app.yaml": {Data: []byte("port: 8080\n")},
		"app.toml": {Data: []byte("port = 8080\n")},
	}
	if _, err := NewFileSourceFSWithCodecs(NewCodecs(YAML), "embedded", fsys, "app.yaml"); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	_, err := NewFileSourceFSWithCodecs(NewCodecs(YAML), "embedded", fsys, "app.toml")
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("toml without TOML: err = %v, want ErrUnsupportedFormat", err)
	}
}

func TestNewStdinSourceWithCodecs_UnknownFormat(t *testing.T) {
	_, err := NewStdinSourceWithCodecs(NewCodecs(JSON), FormatYAML)
	if !errors.Is(err, ErrUnsupportedFormat) || !strings.Contains(err.Error(), `"yaml"`) {
		t.Fatalf("err = %v, want ErrUnsupportedFormat naming yaml", err)
	}
	if _, err := NewStdinSourceWithCodecs(nil, FormatJSON); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("nil codecs: err = %v, want ErrUnsupportedFormat", err)
	}
}

func TestRawValue_DecodeWithCodecs(t *testing.T) {
	rv := RawValue{Format: FormatJSON, Data: []byte(`{"a": "b"}`)}
	var got map[string]any
	if err := rv.DecodeWithCodecs(NewCodecs(JSON), &got); err != nil || got["a"] != "b" {
		t.Fatalf("got %v, err %v", got, err)
	}
	if err := rv.DecodeWithCodecs(NewCodecs(YAML), &got); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("json without JSON: err = %v, want ErrUnsupportedFormat", err)
	}
	if err := rv.DecodeWithCodecs(nil, &got); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("nil codecs: err = %v, want ErrUnsupportedFormat", err)
	}
}

func TestBundledCodecErrorsArePositionErrors(t *testing.T) {
	tests := []struct {
		codec     Codec
		body      string
		line, col int
	}{
		{YAML, "a: 1\nb: [\n", 0, 0},
		{TOML, "a = 1\nb = \n", 0, 0},
		{JSONC, "{\n  \"a\": ,\n}", 0, 0},
		{JSON, "{\n  \"a\": ,\n}", 2, 8},
		{Dotenv, "A=1\nB=\"open\n", 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.codec.Name(), func(t *testing.T) {
			_, err := tc.codec.Decode([]byte(tc.body))
			pe, ok := errors.AsType[PositionError](err)
			if !ok {
				t.Fatalf("%T is not a PositionError: %v", err, err)
			}
			line, col := pe.Position()
			if line == 0 {
				t.Fatalf("no position in %v", err)
			}
			if tc.line != 0 && (line != tc.line || col != tc.col) {
				t.Fatalf("position = %d:%d, want %d:%d", line, col, tc.line, tc.col)
			}
			if !strings.HasPrefix(err.Error(), "recon: "+tc.codec.Name()+" decode: ") {
				t.Fatalf("error text %q lost its prefix", err)
			}
			if pe.Message() == "" || strings.Contains(pe.Message(), "line ") {
				t.Fatalf("message %q should describe the failure without its location", pe.Message())
			}
			gl, gc, gok := ParsePosition(err, []byte(tc.body))
			if !gok || gl != line || gc != col {
				t.Fatalf("ParsePosition = %d:%d %v, want %d:%d", gl, gc, gok, line, col)
			}
			if got := ParseMessage(err); got != pe.Message() {
				t.Fatalf("ParseMessage = %q, want %q", got, pe.Message())
			}
		})
	}
}

// positionedError is a custom codec's decode error reporting a position.
type positionedError struct{}

func (positionedError) Error() string             { return "custom: line 3, column 4: bad token" }
func (positionedError) Position() (line, col int) { return 3, 4 }
func (positionedError) Message() string           { return "bad token" }

type positionedCodec struct{ stubCodec }

func (positionedCodec) Decode([]byte) (map[string]any, error) { return nil, positionedError{} }

func TestCustomCodecPositionErrorFillsParseError(t *testing.T) {
	_, err := NewBufferSource("buf", "custom", []byte("x"),
		WithBufferCodec(positionedCodec{stubCodec{name: "custom"}}))
	pe, ok := errors.AsType[*ParseError](err)
	if !ok {
		t.Fatalf("err = %v, want *ParseError", err)
	}
	if pe.Position != (Position{Line: 3, Column: 4}) || pe.Msg != "bad token" {
		t.Fatalf("ParseError = %+v", pe)
	}
}

// rawYAMLCodec returns the yaml parser's error unwrapped, as a custom
// codec built on go-rotini/yaml might.
type rawYAMLCodec struct{ stubCodec }

func (rawYAMLCodec) Decode(data []byte) (map[string]any, error) {
	var v any
	return nil, yaml.Unmarshal(data, &v)
}

func TestNewFileSource_KeepsPositionsFromParserErrorTypes(t *testing.T) {
	path := writeTempFile(t, "app.yaml", "a: 1\nb: [\n")
	_, err := NewFileSource(path, WithFileCodec(rawYAMLCodec{stubCodec{name: "raw"}}))
	pe, ok := errors.AsType[*ParseError](err)
	if !ok || pe.Position.Line == 0 {
		t.Fatalf("err = %v, want a *ParseError with a position", err)
	}
}
