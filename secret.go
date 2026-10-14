package recon

import "github.com/go-rotini/env"

// Secret is [env.Secret]: a wrapper whose fmt, slog and JSON output is
// a redaction marker instead of the value. A Secret[T] field binds from
// the value's string form, decoded as T by the wrapper's UnmarshalEnv
// method. The `secret`
// tag option is separate: it marks the key secret so recon redacts it
// in [Registry.Describe], [Registry.Save] and errors.
type Secret[T any] = env.Secret[T]

// NewSecret returns a [Secret] holding v; it is [env.NewSecret].
func NewSecret[T any](v T) Secret[T] { return env.NewSecret(v) }
