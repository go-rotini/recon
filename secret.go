package recon

import "github.com/go-rotini/env"

// Secret aliases [env.Secret] so a *env.Secret[T] and a *recon.Secret[T] are
// the same Go type and round-trip across both packages without conversion.
// The `secret` struct-tag option on a [FieldTag] tells the decoder to wrap
// the resolved value in this type and to redact every textual output
// (fmt.Stringer, fmt.GoStringer, slog.LogValuer, JSON marshaling).
//
// The aliasing is deliberate: anything produced by go-rotini/env — loaded
// structs, env.Live[T] snapshots, env.NewSecret(v) values — flows through
// recon without conversion, and vice versa. See the env package for the
// underlying behavior.
type Secret[T any] = env.Secret[T]

// NewSecret aliases [env.NewSecret] — wraps v in a [Secret] so it redacts
// on every textual output. Returned value is the same Go type as
// env.NewSecret(v) and can be passed across package boundaries freely.
func NewSecret[T any](v T) Secret[T] { return env.NewSecret(v) }
