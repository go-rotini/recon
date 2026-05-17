package recon

// YAMLSource returns a [FileSource] pre-wired with the [YAML] codec. The
// convenience form lets callers write `recon.YAMLSource("config.yaml")`
// without having to register or reference a codec separately — handy in
// the rotini codegen path and in one-line `recon.New` calls.
//
// Additional [FileOption] values are applied after the codec is pinned.
func YAMLSource(path string, opts ...FileOption) (Source, error) {
	all := append([]FileOption{WithFileCodec(YAML)}, opts...)
	return NewFileSource(path, all...)
}
