package recon

// DotenvSource returns a [FileSource] pre-wired with the [Dotenv] codec.
// The result holds a flat keyspace — `.env` files have no nesting.
func DotenvSource(path string, opts ...FileOption) (Source, error) {
	all := append([]FileOption{WithFileCodec(Dotenv)}, opts...)
	return NewFileSource(path, all...)
}
