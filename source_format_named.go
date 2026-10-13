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

// NewJSONCSource is [NewFileSource] with the [JSONC] codec pinned,
// whatever the file's extension.
func NewJSONCSource(path string, opts ...FileOption) (Source, error) {
	return NewFileSource(path, append([]FileOption{WithFileCodec(JSONC)}, opts...)...)
}

// NewDotenvSource is [NewFileSource] with the [Dotenv] codec pinned.
// The result holds a flat keyspace; add [WithFileEnvKeys] to read its
// names as environment variables, so APP_SERVER_PORT answers
// server.port.
func NewDotenvSource(path string, opts ...FileOption) (Source, error) {
	return NewFileSource(path, append([]FileOption{WithFileCodec(Dotenv)}, opts...)...)
}
