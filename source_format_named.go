package recon

// Format-named convenience constructors for [FileSource]. Each
// wraps [NewFileSource] with the codec pinned via [WithFileCodec].
// Useful when the file extension is missing or misleading.

// NewYAMLSource is [NewFileSource] with the [YAML] codec pinned.
func NewYAMLSource(path string, opts ...FileOption) (Source, error) {
	return NewFileSource(path, append([]FileOption{WithFileCodec(YAML)}, opts...)...)
}

// NewTOMLSource is [NewFileSource] with the [TOML] codec pinned.
func NewTOMLSource(path string, opts ...FileOption) (Source, error) {
	return NewFileSource(path, append([]FileOption{WithFileCodec(TOML)}, opts...)...)
}

// NewJSONSource is [NewFileSource] with the [JSON] codec pinned.
func NewJSONSource(path string, opts ...FileOption) (Source, error) {
	return NewFileSource(path, append([]FileOption{WithFileCodec(JSON)}, opts...)...)
}

// NewJSONCSource is [NewFileSource] with the [JSONC] codec pinned.
// Accepts both `.jsonc` and `.json5` files.
func NewJSONCSource(path string, opts ...FileOption) (Source, error) {
	return NewFileSource(path, append([]FileOption{WithFileCodec(JSONC)}, opts...)...)
}

// NewDotenvSource is [NewFileSource] with the [Dotenv] codec pinned.
// The result holds a flat keyspace.
func NewDotenvSource(path string, opts ...FileOption) (Source, error) {
	return NewFileSource(path, append([]FileOption{WithFileCodec(Dotenv)}, opts...)...)
}
