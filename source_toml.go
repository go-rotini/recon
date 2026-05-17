package recon

// TOMLSource returns a [FileSource] pre-wired with the [TOML] codec.
// Convenience wrapper — see [YAMLSource] for the format-named-source
// pattern.
func TOMLSource(path string, opts ...FileOption) (Source, error) {
	all := append([]FileOption{WithFileCodec(TOML)}, opts...)
	return NewFileSource(path, all...)
}
