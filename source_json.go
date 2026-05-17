package recon

// JSONSource returns a [FileSource] pre-wired with the [JSON] codec.
// Convenience wrapper — see [YAMLSource].
func JSONSource(path string, opts ...FileOption) (Source, error) {
	all := append([]FileOption{WithFileCodec(JSON)}, opts...)
	return NewFileSource(path, all...)
}
