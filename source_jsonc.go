package recon

// JSONCSource returns a [FileSource] pre-wired with the [JSONC] codec.
// Accepts both `.jsonc` and `.json5` files.
func JSONCSource(path string, opts ...FileOption) (Source, error) {
	all := append([]FileOption{WithFileCodec(JSONC)}, opts...)
	return NewFileSource(path, all...)
}
