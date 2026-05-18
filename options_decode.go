package recon

import (
	"context"
	"reflect"
)

// DecodeOption configures one [Registry.Bind] / [Registry.Unmarshal]
// call. Registry-level options supply the defaults; DecodeOption is
// for per-call overrides.
type DecodeOption func(*decodeOptions)

type decodeOptions struct {
	strict         *bool          // nil → inherit registry default
	errorBehavior  *ErrorBehavior // nil → inherit registry default
	tagName        string         // default "recon"
	ctx            context.Context
	customDecoders map[string]customDecoderFn // by reflect.Type.String()
}

// customDecoderFn is the type-erased shape [WithCustomDecoder] stores.
type customDecoderFn func(Value) (any, error)

// WithDecodeStrict turns on strict decoding for this call.
func WithDecodeStrict() DecodeOption {
	return func(o *decodeOptions) { t := true; o.strict = &t }
}

// WithDecodeLenient turns off strict decoding for this call.
func WithDecodeLenient() DecodeOption {
	return func(o *decodeOptions) { t := false; o.strict = &t }
}

// WithDecodeErrorBehavior overrides the registry's error-accumulation
// behavior for this call.
func WithDecodeErrorBehavior(b ErrorBehavior) DecodeOption {
	return func(o *decodeOptions) { o.errorBehavior = &b }
}

// WithDecodeTag changes which struct tag the decoder inspects.
// Default "recon"; the decoder falls back through env / json / yaml /
// toml when the primary tag is absent.
func WithDecodeTag(name string) DecodeOption {
	return func(o *decodeOptions) { o.tagName = name }
}

// WithDecodeContext threads ctx through the decode pass, available to
// [ValidatorContext] hooks the bind target may implement.
func WithDecodeContext(ctx context.Context) DecodeOption {
	return func(o *decodeOptions) { o.ctx = ctx }
}

// WithCustomDecoder registers a per-call decoder for type T. When a
// bound field has Go type T, fn receives the resolved [Value] in
// place of the built-in coercion.
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

func typeKey[T any](_ T) string {
	return reflect.TypeFor[T]().String()
}
