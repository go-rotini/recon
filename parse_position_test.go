package recon

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

const bom = "\xef\xbb\xbf"

// brokenDocs holds one malformed document per bundled codec, with the
// position its parser reports.
var brokenDocs = []struct {
	file      string
	body      string
	line, col int
}{
	{"bad.yaml", "a: 1\nb: [1, 2\n", 2, 4},
	{"bad.json", "{\n  \"a\": 1,\n  \"b\" 2\n}\n", 3, 7},
	{"bad.jsonc", "{\n  // note\n  \"a\": ,\n}\n", 3, 8},
	{"bad.toml", "a = 1\nb = = 2\n", 2, 5},
	{"bad.env", "A=1\nB='unterminated\n", 2, 3},
}

func TestNewFileSource_ParseErrorPosition(t *testing.T) {
	for _, tc := range brokenDocs {
		t.Run(tc.file, func(t *testing.T) {
			path := writeTempFile(t, tc.file, tc.body)
			_, err := NewFileSource(path)
			pe, ok := errors.AsType[*ParseError](err)
			if !ok {
				t.Fatalf("err = %v (%T), want *ParseError", err, err)
			}
			if pe.Path != path || pe.Source != tc.file {
				t.Errorf("Path, Source = %q, %q; want %q, %q", pe.Path, pe.Source, path, tc.file)
			}
			if pe.Position != (Position{Line: tc.line, Column: tc.col}) {
				t.Errorf("Position = %v, want %d:%d", pe.Position, tc.line, tc.col)
			}
			if pe.Msg == "" || strings.Contains(pe.Msg, "line ") {
				t.Errorf("Msg = %q, want the parser's message without its position", pe.Msg)
			}
			if pe.Unwrap() == nil {
				t.Error("ParseError should unwrap to the codec error")
			}
			want := path + ":" + pe.Position.String() + ": " + pe.Msg
			if !strings.Contains(pe.Error(), want) {
				t.Errorf("Error() = %q, want it to contain %q", pe.Error(), want)
			}
			line, col, ok := ParsePosition(err, nil)
			if !ok || line != tc.line || col != tc.col {
				t.Errorf("ParsePosition(*ParseError) = %d, %d, %v", line, col, ok)
			}
		})
	}
}

func TestParsePosition_CodecErrors(t *testing.T) {
	for _, tc := range brokenDocs {
		t.Run(tc.file, func(t *testing.T) {
			c, ok := DefaultCodecs().ByExtension(tc.file[strings.LastIndexByte(tc.file, '.'):])
			if !ok {
				t.Fatalf("no codec for %s", tc.file)
			}
			_, err := c.Decode([]byte(tc.body))
			if err == nil {
				t.Fatal("Decode succeeded on a malformed document")
			}
			line, col, ok := ParsePosition(err, []byte(tc.body))
			if !ok || line != tc.line || col != tc.col {
				t.Errorf("ParsePosition = %d, %d, %v; want %d, %d, true", line, col, ok, tc.line, tc.col)
			}
		})
	}
}

func TestParsePosition_NoPosition(t *testing.T) {
	_, err := JSON.Decode([]byte(`[1, 2]`))
	if !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("err = %v, want ErrUnsupportedFormat", err)
	}
	if line, col, ok := ParsePosition(err, []byte(`[1, 2]`)); ok {
		t.Errorf("ParsePosition = %d, %d, true; want ok=false for a wrong-type root", line, col)
	}
	if _, _, ok := ParsePosition(errors.New("plain"), nil); ok {
		t.Error("ParsePosition on a plain error should report ok=false")
	}

	path := writeTempFile(t, "list.json", `[1, 2]`)
	_, err = NewFileSource(path)
	pe, isParse := errors.AsType[*ParseError](err)
	if !isParse {
		t.Fatalf("err = %v, want *ParseError", err)
	}
	if pe.Position != (Position{}) || pe.Path != path || pe.Msg == "" {
		t.Errorf("ParseError = %+v", pe)
	}
}

func TestParsePosition_JSONOffsetIgnoresBOM(t *testing.T) {
	doc := []byte(bom + "{\"a\" 1}")
	_, err := JSON.Decode(doc)
	line, col, ok := ParsePosition(err, doc)
	if !ok || line != 1 || col != 6 {
		t.Errorf("ParsePosition = %d, %d, %v; want 1, 6, true", line, col, ok)
	}
}

func TestFileSource_ReloadParseErrorPosition(t *testing.T) {
	path := writeTempFile(t, "app.toml", "a = 1\n")
	src, err := NewFileSource(path)
	if err != nil {
		t.Fatalf("NewFileSource: %v", err)
	}
	if err := writeFile(path, []byte("a = 1\nb = = 2\n")); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	err = src.(*FileSource).Reload()
	pe, ok := errors.AsType[*ParseError](err)
	if !ok {
		t.Fatalf("Reload err = %v, want *ParseError", err)
	}
	if pe.Path != path || pe.Position.Line != 2 || pe.Msg == "" {
		t.Errorf("ParseError = %+v", pe)
	}
}

func TestFileSourceFS_ParseErrorPosition(t *testing.T) {
	dir := t.TempDir()
	if err := writeFile(dir+"/c.yaml", []byte("a: 1\nb: [1, 2\n")); err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	_, err := NewFileSourceFS("embedded", os.DirFS(dir), "c.yaml")
	pe, ok := errors.AsType[*ParseError](err)
	if !ok {
		t.Fatalf("err = %v, want *ParseError", err)
	}
	if pe.Path != "c.yaml" || pe.Position.Line == 0 {
		t.Errorf("ParseError = %+v", pe)
	}
}

func TestParseError_FormatErrorUsesPosition(t *testing.T) {
	pe := &ParseError{Source: "c.yaml", Path: "/etc/c.yaml", Position: Position{Line: 2, Column: 4}, Cause: errors.New("yaml: line 2, column 4: bad indent"), Msg: "bad indent"}
	if got := pe.Error(); got != "recon: parse /etc/c.yaml:2:4: bad indent" {
		t.Errorf("Error() = %q", got)
	}
	if got := formatOneError(nil, pe); got != "parse /etc/c.yaml:2:4: bad indent" {
		t.Errorf("formatOneError = %q", got)
	}
}

func TestFileSource_StripsBOM(t *testing.T) {
	docs := map[string]string{
		"c.yaml":  "a: one\n",
		"c.json":  `{"a": "one"}`,
		"c.jsonc": "{\n  // c\n  \"a\": \"one\",\n}",
		"c.toml":  "a = \"one\"\n",
		"c.env":   "a=one\n",
	}
	for file, body := range docs {
		t.Run(file, func(t *testing.T) {
			path := writeTempFile(t, file, bom+body)
			src, err := NewFileSource(path)
			if err != nil {
				t.Fatalf("NewFileSource: %v", err)
			}
			assertKeyA(t, src, "one")

			if err := writeFile(path, []byte(bom+strings.Replace(body, "one", "two", 1))); err != nil {
				t.Fatalf("writeFile: %v", err)
			}
			if err := src.(*FileSource).Reload(); err != nil {
				t.Fatalf("Reload: %v", err)
			}
			assertKeyA(t, src, "two")
		})
	}
}

func TestJSONCodec_StripsBOM(t *testing.T) {
	m, err := JSON.Decode([]byte(bom + `{"a": 1}`))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if m["a"] != int64(1) {
		t.Errorf("a = %v, want 1", m["a"])
	}
	if m, err := JSON.Decode([]byte(bom)); err != nil || len(m) != 0 {
		t.Errorf("Decode(BOM only) = %v, %v; want empty map", m, err)
	}
	if _, err := JSON.Decode([]byte(bom + bom + `{}`)); err == nil {
		t.Error("only one byte-order mark should be stripped")
	}
}

func assertKeyA(t *testing.T, src Source, want string) {
	t.Helper()
	v, ok, err := src.Get(Path{"a"})
	if err != nil || !ok {
		t.Fatalf("Get(a) = %v, %v, %v", v, ok, err)
	}
	if s, _ := v.AsString(); s != want {
		t.Errorf("a = %q, want %q", s, want)
	}
}

func TestParsePosition_JSONEndOfInput(t *testing.T) {
	doc := []byte("{\"a\":\n")
	_, err := JSON.Decode(doc)
	if line, col, ok := ParsePosition(err, doc); !ok || line != 2 || col != 1 {
		t.Errorf("ParsePosition = %d, %d, %v; want 2, 1, true (where more input was expected)", line, col, ok)
	}
}

func TestParsePosition_JSONTypeError(t *testing.T) {
	doc := []byte("{\n  \"a\": \"x\"\n}")
	var v struct {
		A int `json:"a"`
	}
	err := json.Unmarshal(doc, &v)
	if line, col, ok := ParsePosition(err, doc); !ok || line != 2 || col != 11 {
		t.Errorf("ParsePosition = %d, %d, %v; want 2, 11, true", line, col, ok)
	}
}

func TestParseMessage(t *testing.T) {
	for _, tc := range brokenDocs {
		t.Run(tc.file, func(t *testing.T) {
			c, _ := DefaultCodecs().ByExtension(tc.file[strings.LastIndexByte(tc.file, '.'):])
			_, err := c.Decode([]byte(tc.body))
			msg := ParseMessage(err)
			if msg == "" || strings.Contains(msg, "line ") || strings.HasPrefix(msg, "recon:") {
				t.Errorf("ParseMessage = %q, want the bare parser message", msg)
			}
			if got := ParseMessage(newParseError("s", "", []byte(tc.body), err)); got != msg {
				t.Errorf("ParseMessage(*ParseError) = %q, want %q", got, msg)
			}
		})
	}
	if got := ParseMessage(errors.New("plain")); got != "plain" {
		t.Errorf("ParseMessage(plain) = %q", got)
	}
	if got := ParseMessage(nil); got != "" {
		t.Errorf("ParseMessage(nil) = %q, want empty", got)
	}
}
