# DDTrace

DDTrace is a command line tool that auto-generates DataDog tracing decorators for all Go interface types in a package.
It scans your source code, discovers interfaces, and generates type-safe tracing wrappers with zero configuration.

## Installation

```bash
go install github.com/tuanvm-tyson/ddtrace/cmd/ddtrace@latest
```

## Quick Start

1. Add a `//go:generate` directive to any Go file in your package:

```go
// service/interfaces.go
package service

import "context"

//go:generate ddtrace gen

type UserService interface {
    GetUser(ctx context.Context, id string) (*User, error)
    CreateUser(ctx context.Context, req CreateUserRequest) (*User, error)
}

type OrderService interface {
    CreateOrder(ctx context.Context, req CreateOrderRequest) (*Order, error)
}
```

2. Run `go generate`:

```bash
go generate ./service/...
```

3. This creates `service/trace/interfaces_trace.go` with tracing wrappers for all interfaces:

```go
// service/trace/interfaces_trace.go (auto-generated)
package trace

import (
    "context"
    service "myapp/service"
    "github.com/tuanvm-tyson/ddtrace/tracing"
)

// Per-interface decorators -- no inline boilerplate
type UserServiceWithTracing struct { ... }
func NewUserServiceWithTracing(base service.UserService, opts ...tracing.TracingOption) { ... }

type OrderServiceWithTracing struct { ... }
func NewOrderServiceWithTracing(base service.OrderService, opts ...tracing.TracingOption) { ... }
```

4. Add the tracing library dependency:

```bash
go get github.com/tuanvm-tyson/ddtrace/tracing@latest
```

5. Use the traced wrapper in your application:

```go
import "myapp/service/trace"

userSvc := service.NewUserServiceImpl(repo)
tracedSvc := trace.NewUserServiceWithTracing(userSvc)
```

## Usage

```
ddtrace gen [-p package] [-o output_dir] [-g]
```

| Flag | Default | Description |
|------|---------|-------------|
| `-p` | `./` | Source package path |
| `-o` | `./trace` | Output directory (relative to source package) |
| `-g` | `false` | Don't put `//go:generate` instruction in generated code |

### Examples

```bash
# Auto-generate for current package (most common)
ddtrace gen

# Specify source package
ddtrace gen -p ./service

# Custom output directory
ddtrace gen -p ./service -o ./instrumented
```

## Output Structure

For each source file containing interfaces, DDTrace generates a corresponding `_trace.go` file in the output directory:

```
service/
  interfaces.go          → service/trace/interfaces_trace.go
  repository.go          → service/trace/repository_trace.go
  helpers.go             → (no output if no interfaces or all ignored)
```

## Skipping Interfaces

Use `//ddtrace:ignore` to exclude specific interfaces from generation:

```go
type UserService interface { ... }      // generated

//ddtrace:ignore
type InternalHelper interface { ... }   // skipped

type OrderService interface { ... }     // generated
```

## How It Works

- Scans **all interfaces** in the source package
- Generates tracing wrappers only for methods that accept `context.Context` as the first parameter
- Methods without `context.Context` are passed through to the base implementation
- Errors are automatically tagged on the span when the last return value is `error`
- Supports Go generics, embedded interfaces, and cross-package types

## Global Defaults

Set package-level defaults once at startup -- they apply to ALL tracing decorators and manual `StartSpan` calls automatically:

```go
import (
    "myapp/service/trace"
    "github.com/tuanvm-tyson/ddtrace/tracing"
)

func main() {
    // Set global context decorator -- extracts request-scoped values for ALL spans
    tracing.SetDefaultContextDecorator(func(ctx context.Context, span ddtrace.Span) {
        if userID := auth.GetUserID(ctx); userID != "" {
            span.SetTag("user.id", userID)
        }
        if tenantID := auth.GetTenantID(ctx); tenantID != "" {
            span.SetTag("tenant.id", tenantID)
        }
    })

    // Set global span options -- applied to ALL spans
    tracing.SetDefaultSpanOptions(tracer.ServiceName("my-service"))

    // Create decorators with NO options -- globals are applied automatically
    tracedUserSvc := trace.NewUserServiceWithTracing(userSvc)
    tracedOrderSvc := trace.NewOrderServiceWithTracing(orderSvc)
}
```

## Functional Options

The generated constructors use the functional options pattern for per-instance customization.
Per-instance options layer on top of global defaults. Option types come from the `tracing` library:

```go
import "github.com/tuanvm-tyson/ddtrace/tracing"

// Simple usage - no options needed (global defaults apply)
tracedSvc := trace.NewUserServiceWithTracing(userSvc)

// With per-instance context decorator (runs after global context decorator)
tracedSvc := trace.NewUserServiceWithTracing(userSvc,
    tracing.WithContextDecorator(func(ctx context.Context, span ddtrace.Span) {
        span.SetTag("payment.gateway", "stripe")
    }),
)

// With custom span decorator (for result-based tags)
tracedSvc := trace.NewUserServiceWithTracing(userSvc,
    tracing.WithSpanDecorator(func(span ddtrace.Span, params, results map[string]interface{}) {
        if userID, ok := params["id"].(string); ok {
            span.SetTag("user.id", userID)
        }
    }),
)

// With additional span options
tracedSvc := trace.NewUserServiceWithTracing(userSvc,
    tracing.WithSpanOptions(tracer.ServiceName("user-service")),
)

// Combine multiple options
tracedSvc := trace.NewUserServiceWithTracing(userSvc,
    tracing.WithSpanOptions(tracer.ServiceName("user-service")),
    tracing.WithSpanDecorator(myCustomDecorator),
    tracing.WithContextDecorator(myContextDecorator),
)
```

## Manual Tracing Helpers

The `tracing` library provides helper functions for **manual tracing** of code not covered by
interface decorators -- private functions, handler-level code, closures, etc.
These helpers share the same global defaults (`SetDefaultContextDecorator`, `SetDefaultSpanOptions`)
as the generated decorators, so all spans are consistent.

```go
import "github.com/tuanvm-tyson/ddtrace/tracing"
```

### StartSpan

Creates a new span. The operation name is **auto-detected from the call stack** by default.

```go
// Auto-detect operation name from caller (most common)
span, ctx := tracing.StartSpan(ctx)

// Override operation name
span, ctx := tracing.StartSpan(ctx, tracing.WithOperationName("CustomOperation"))

// With additional tracer options
span, ctx := tracing.StartSpan(ctx, tracing.WithTracerOptions(tracer.ServiceName("payment-svc")))
```

### FinishSpan

Finishes a span and automatically sets error tags if `err != nil`. Use in `defer`:

```go
func (s *serviceImpl) doPrivateWork(ctx context.Context) (err error) {
    span, ctx := tracing.StartSpan(ctx)
    defer func() { tracing.FinishSpan(span, err) }()

    // business logic...
    return s.repo.Save(ctx, data)
}
```

### SetError

Sets error tags on a span without finishing it. Useful in handlers where you `defer span.Finish()`
separately and set errors at different branch points:

```go
func (h *Handler) GetUser(c *gin.Context) {
    span, ctx := tracing.StartSpan(c.Request.Context())
    defer span.Finish()
    c.Request = c.Request.WithContext(ctx)

    user, err := h.userService.GetUser(ctx, c.Param("id"))
    if err != nil {
        tracing.SetError(span, err)
        c.JSON(500, gin.H{"error": err.Error()})
        return
    }
    c.JSON(200, user)
}
```

### GIN / ECHO Handler Examples

**GIN:**

```go
func (h *UserHandler) GetUser(c *gin.Context) {
    span, ctx := tracing.StartSpan(c.Request.Context())  // auto: "UserHandler.GetUser"
    defer span.Finish()
    c.Request = c.Request.WithContext(ctx)

    user, err := h.userService.GetUser(ctx, c.Param("id"))
    if err != nil {
        tracing.SetError(span, err)
        c.JSON(500, gin.H{"error": err.Error()})
        return
    }
    c.JSON(200, user)
}
```

**ECHO:**

```go
func (h *UserHandler) GetUser(c echo.Context) error {
    span, ctx := tracing.StartSpan(c.Request().Context())  // auto: "UserHandler.GetUser"
    defer span.Finish()
    c.SetRequest(c.Request().WithContext(ctx))

    user, err := h.userService.GetUser(ctx, c.Param("id"))
    if err != nil {
        tracing.SetError(span, err)
        return c.JSON(500, map[string]string{"error": err.Error()})
    }
    return c.JSON(200, user)
}
```
