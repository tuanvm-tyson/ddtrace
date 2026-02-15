package tracing

import (
	"context"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

var (
	globalContextDecorator func(ctx context.Context, span ddtrace.Span)
	globalSpanOpts         []tracer.StartSpanOption
)

// SetDefaultContextDecorator sets a context decorator that is automatically applied to ALL spans
// created by any tracing decorator or manual StartSpan call. Use this to extract request-scoped
// values (e.g., userId, tenantId) from context and set them as span tags.
//
// Call this once at application startup, before creating any decorator instances.
func SetDefaultContextDecorator(f func(ctx context.Context, span ddtrace.Span)) {
	globalContextDecorator = f
}

// SetDefaultSpanOptions sets span options that are automatically prepended to ALL spans
// created by any tracing decorator or manual StartSpan call.
//
// Call this once at application startup, before creating any decorator instances.
func SetDefaultSpanOptions(opts ...tracer.StartSpanOption) {
	globalSpanOpts = opts
}
