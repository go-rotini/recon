package recon

// Format-named convenience constructors for [FileSource]. Each is a
// one-line wrapper around [NewFileSource] that pins the codec via
// [WithFileCodec]. Useful in cases where the file path's extension
// is missing or misleading (a `.txt` file holding YAML), or when
// the caller prefers a more discoverable name than
// "NewFileSource + WithFileCodec(recon.YAML)".
//
// Additional [FileOption] values are applied after the codec is
// pinned and may override it via [WithFileCodec] / [WithFileFormat].

// NewYAMLSource returns a [FileSource] pre-wired with the [YAML]
// codec.
func NewYAMLSource(path string, opts ...FileOption) (Source, error) {
	return NewFileSource(path, append([]FileOption{WithFileCodec(YAML)}, opts...)...)
}

// NewTOMLSource returns a [FileSource] pre-wired with the [TOML]
// codec.
func NewTOMLSource(path string, opts ...FileOption) (Source, error) {
	return NewFileSource(path, append([]FileOption{WithFileCodec(TOML)}, opts...)...)
}

// NewJSONSource returns a [FileSource] pre-wired with the [JSON]
// codec.
func NewJSONSource(path string, opts ...FileOption) (Source, error) {
	return NewFileSource(path, append([]FileOption{WithFileCodec(JSON)}, opts...)...)
}

// NewJSONCSource returns a [FileSource] pre-wired with the [JSONC]
// codec. Accepts both `.jsonc` and `.json5` files.
func NewJSONCSource(path string, opts ...FileOption) (Source, error) {
	return NewFileSource(path, append([]FileOption{WithFileCodec(JSONC)}, opts...)...)
}

// NewDotenvSource returns a [FileSource] pre-wired with the [Dotenv]
// codec. The result holds a flat keyspace — `.env` files have no
// nesting.
func NewDotenvSource(path string, opts ...FileOption) (Source, error) {
	return NewFileSource(path, append([]FileOption{WithFileCodec(Dotenv)}, opts...)...)
}
