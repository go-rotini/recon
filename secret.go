package recon

import "github.com/go-rotini/env"

// Secret aliases [env.Secret] so the two are the same Go type and
// round-trip across packages without conversion. The `secret`
// struct-tag option tells the bind decoder to wrap the resolved
// value in this type and redact every textual rendering.
type Secret[T any] = env.Secret[T]

// NewSecret aliases [env.NewSecret] — wraps v in a [Secret] so it
// redacts on every textual output.
func NewSecret[T any](v T) Secret[T] { return env.NewSecret(v) }
