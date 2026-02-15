package tracing

import (
	"context"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// TracingConfig holds per-instance tracing configuration for generated decorators.
type TracingConfig struct {
	spanDecorator    func(span ddtrace.Span, params, results map[string]interface{})
	contextDecorator func(ctx context.Context, span ddtrace.Span)
	spanOpts         []tracer.StartSpanOption
}

// TracingOption configures a TracingConfig.
type TracingOption func(*TracingConfig)

// NewTracingConfig creates a TracingConfig with the given options.
// Global span options are automatically prepended; per-instance options take precedence.
func NewTracingConfig(opts ...TracingOption) TracingConfig {
	cfg := TracingConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	// Prepend global span options; per-instance options take precedence
	cfg.spanOpts = append(append([]tracer.StartSpanOption{}, globalSpanOpts...), cfg.spanOpts...)
	return cfg
}

// WithSpanDecorator sets a custom span decorator that is called on every span
// with the method parameters and results, allowing you to add custom tags.
func WithSpanDecorator(f func(span ddtrace.Span, params, results map[string]interface{})) TracingOption {
	return func(c *TracingConfig) {
		c.spanDecorator = f
	}
}

// WithContextDecorator sets a per-instance context decorator that runs after the global
// context decorator (if set). Use this for instance-specific span tags.
func WithContextDecorator(f func(ctx context.Context, span ddtrace.Span)) TracingOption {
	return func(c *TracingConfig) {
		c.contextDecorator = f
	}
}

// WithSpanOptions sets additional tracer.StartSpanOption to be applied
// to every span created by the tracing decorator.
func WithSpanOptions(opts ...tracer.StartSpanOption) TracingOption {
	return func(c *TracingConfig) {
		c.spanOpts = append(c.spanOpts, opts...)
	}
}

// StartSpan creates a new span using this config's span options and context decorators.
// Global defaults (globalSpanOpts, globalContextDecorator) are included via NewTracingConfig.
// This method is called by generated decorator code.
func (c *TracingConfig) StartSpan(ctx context.Context, operationName string) (ddtrace.Span, context.Context) {
	span, ctx := tracer.StartSpanFromContext(ctx, operationName, c.spanOpts...)
	if globalContextDecorator != nil {
		globalContextDecorator(ctx, span)
	}
	if c.contextDecorator != nil {
		c.contextDecorator(ctx, span)
	}
	return span, ctx
}

// FinishSpan finishes a span. If a spanDecorator is set, it is called with params and results.
// Otherwise, if err is not nil, error tags are automatically set on the span.
// This method is called by generated decorator code.
func (c *TracingConfig) FinishSpan(span ddtrace.Span, err error, params, results map[string]interface{}) {
	if c.spanDecorator != nil {
		c.spanDecorator(span, params, results)
	} else if err != nil {
		SetError(span, err)
	}
	span.Finish()
}
