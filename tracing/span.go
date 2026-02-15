package tracing

import (
	"context"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// SpanOption configures StartSpan behavior.
type SpanOption func(*spanStartConfig)

type spanStartConfig struct {
	operationName string
	tracerOpts    []tracer.StartSpanOption
}

// WithOperationName overrides the auto-detected operation name for StartSpan.
func WithOperationName(name string) SpanOption {
	return func(c *spanStartConfig) {
		c.operationName = name
	}
}

// WithTracerOptions passes additional tracer.StartSpanOption to the span
// created by StartSpan.
func WithTracerOptions(opts ...tracer.StartSpanOption) SpanOption {
	return func(c *spanStartConfig) {
		c.tracerOpts = append(c.tracerOpts, opts...)
	}
}

// StartSpan creates a new span from the given context.
// By default, the operation name is auto-detected from the calling function's name
// using runtime.Caller. Use WithOperationName to override.
// Global defaults (globalSpanOpts, globalContextDecorator) are applied automatically.
//
// Example (private function):
//
//	func (s *service) doWork(ctx context.Context) (err error) {
//	    span, ctx := tracing.StartSpan(ctx)
//	    defer func() { tracing.FinishSpan(span, err) }()
//	    // ... business logic ...
//	}
//
// Example (GIN handler):
//
//	func (h *Handler) GetUser(c *gin.Context) {
//	    span, ctx := tracing.StartSpan(c.Request.Context())
//	    defer span.Finish()
//	    c.Request = c.Request.WithContext(ctx)
//	    // ... handler logic ...
//	}
//
// Example (override name):
//
//	span, ctx := tracing.StartSpan(ctx, tracing.WithOperationName("CustomOp"))
func StartSpan(ctx context.Context, opts ...SpanOption) (ddtrace.Span, context.Context) {
	cfg := spanStartConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.operationName == "" {
		cfg.operationName = callerFuncName(1)
	}
	allOpts := append(append([]tracer.StartSpanOption{}, globalSpanOpts...), cfg.tracerOpts...)
	span, ctx := tracer.StartSpanFromContext(ctx, cfg.operationName, allOpts...)
	if globalContextDecorator != nil {
		globalContextDecorator(ctx, span)
	}
	return span, ctx
}

// FinishSpan finishes a span and automatically sets error tags if err is not nil.
// Use this in a defer to ensure spans are always properly finished.
//
// Example:
//
//	func doWork(ctx context.Context) (err error) {
//	    span, ctx := tracing.StartSpan(ctx)
//	    defer func() { tracing.FinishSpan(span, err) }()
//	    return someOperation(ctx)
//	}
func FinishSpan(span ddtrace.Span, err error) {
	if err != nil {
		SetError(span, err)
	}
	span.Finish()
}

// SetError sets error tags on a span without finishing it.
// Use this when you need to tag an error but want span.Finish() to be called separately
// (e.g., in a deferred call).
//
// Example (GIN handler):
//
//	func (h *Handler) GetUser(c *gin.Context) {
//	    span, ctx := tracing.StartSpan(c.Request.Context())
//	    defer span.Finish()
//	    c.Request = c.Request.WithContext(ctx)
//
//	    user, err := h.svc.GetUser(ctx, c.Param("id"))
//	    if err != nil {
//	        tracing.SetError(span, err)
//	        c.JSON(500, gin.H{"error": err.Error()})
//	        return
//	    }
//	    c.JSON(200, user)
//	}
func SetError(span ddtrace.Span, err error) {
	if err != nil {
		span.SetTag(ext.Error, err)
		span.SetTag(ext.ErrorMsg, err.Error())
		span.SetTag(ext.ErrorType, "error")
	}
}
