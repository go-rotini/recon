package recon

// DefaultCodecs returns a fresh [Codecs] registry populated with the
// bundled format codecs ([YAML], [TOML], [JSONC], [JSON], [Dotenv]).
// Each call returns an independent registry; callers that need
// shared state should obtain one and pass it around. Calling it links
// every bundled codec into the program; pass [NewCodecs] with just the
// formats you read to the *WithCodecs constructors to avoid that.
func DefaultCodecs() *Codecs {
	return NewCodecs(YAML, TOML, JSONC, JSON, Dotenv)
}
