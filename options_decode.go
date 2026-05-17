package recon

import (
	"context"
	"reflect"
)

// DecodeOption configures a single [Registry.Bind] / [Registry.Unmarshal]
// call. Most applications will not need DecodeOption: the registry-level
// options control the default behavior, and per-call overrides are for
// niche cases (a strict bind in an otherwise-lenient app, a context-aware
// hook, etc.).
type DecodeOption func(*decodeOptions)

// decodeOptions is the internal struct DecodeOption closures mutate.
type decodeOptions struct {
	strict         *bool          // nil ⇒ inherit registry default
	errorBehavior  *ErrorBehavior // nil ⇒ inherit registry default
	tagName        string         // default "recon"
	ctx            context.Context
	customDecoders map[string]customDecoderFn // by reflect.Type.String()
}

// customDecoderFn is the type-erased shape WithCustomDecoder stores. The
// generic registration helper preserves T at the call site; the registry
// reflects on the field type at decode time and dispatches by string match.
type customDecoderFn func(Value) (any, error)

// WithDecodeStrict turns on strict decoding for this call regardless of the
// registry's setting.
func WithDecodeStrict() DecodeOption {
	return func(o *decodeOptions) { t := true; o.strict = &t }
}

// WithDecodeLenient turns off strict decoding for this call regardless of
// the registry's setting.
func WithDecodeLenient() DecodeOption {
	return func(o *decodeOptions) { t := false; o.strict = &t }
}

// WithDecodeErrorBehavior overrides the registry's error-accumulation
// behavior for this call.
func WithDecodeErrorBehavior(b ErrorBehavior) DecodeOption {
	return func(o *decodeOptions) { o.errorBehavior = &b }
}

// WithDecodeTag changes which struct tag the decoder inspects. Defaults to
// "recon"; the decoder falls back through "env", "json", "yaml", "toml" when
// the primary tag is absent.
func WithDecodeTag(name string) DecodeOption {
	return func(o *decodeOptions) { o.tagName = name }
}

// WithDecodeContext threads a context through the decode pass — used by
// [UnmarshalerContext] / [ValidatorContext] hooks the bind target may
// implement.
func WithDecodeContext(ctx context.Context) DecodeOption {
	return func(o *decodeOptions) { o.ctx = ctx }
}

// WithCustomDecoder registers a per-call decoder for type T. When the
// decoder walks a struct field whose Go type matches T, it invokes fn with
// the resolved [Value] instead of running the built-in coercion. The
// function returns the typed result; the decoder type-asserts the return
// into the field.
//
// Note: generic type parameters are used at the call site to preserve T,
// then erased to the internal customDecoderFn shape for storage.
func WithCustomDecoder[T any](fn func(Value) (T, error)) DecodeOption {
	return func(o *decodeOptions) {
		if o.customDecoders == nil {
			o.customDecoders = map[string]customDecoderFn{}
		}
		var zero T
		o.customDecoders[typeKey(zero)] = func(v Value) (any, error) {
			return fn(v)
		}
	}
}

// typeKey returns a stable string identifier for T. Implementation detail
// of [WithCustomDecoder]. Uses reflect.TypeFor so interface-typed T (whose
// zero value is nil any) resolves cleanly.
func typeKey[T any](_ T) string {
	return reflect.TypeFor[T]().String()
}
