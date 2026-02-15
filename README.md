# DDTrace

DDTrace is a command line tool that auto-generates DataDog tracing decorators for all Go interface types in a package.
It scans your source code, discovers interfaces, and generates type-safe tracing wrappers with zero configuration.

## Installation

```bash
go install github.com/tyson-tuanvm/ddtrace/cmd/ddtrace@latest
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

// Shared functional option types
type TracingOption func(*tracingConfig)
func WithSpanDecorator(f func(...)) TracingOption { ... }
func WithSpanOptions(opts ...tracer.StartSpanOption) TracingOption { ... }

// Per-interface decorators
type UserServiceWithTracing struct { ... }
func NewUserServiceWithTracing(base service.UserService, opts ...TracingOption) { ... }

type OrderServiceWithTracing struct { ... }
func NewOrderServiceWithTracing(base service.OrderService, opts ...TracingOption) { ... }
```

4. Use the traced wrapper in your application:

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

Set package-level defaults once at startup -- they apply to ALL tracing decorators automatically:

```go
import "myapp/service/trace"

func main() {
    // Set global context decorator -- extracts request-scoped values for ALL spans
    trace.SetDefaultContextDecorator(func(ctx context.Context, span ddtrace.Span) {
        if userID := auth.GetUserID(ctx); userID != "" {
            span.SetTag("user.id", userID)
        }
        if tenantID := auth.GetTenantID(ctx); tenantID != "" {
            span.SetTag("tenant.id", tenantID)
        }
    })

    // Set global span options -- applied to ALL spans
    trace.SetDefaultSpanOptions(tracer.ServiceName("my-service"))

    // Create decorators with NO options -- globals are applied automatically
    tracedUserSvc := trace.NewUserServiceWithTracing(userSvc)
    tracedOrderSvc := trace.NewOrderServiceWithTracing(orderSvc)
}
```

## Functional Options

The generated constructors use the functional options pattern for per-instance customization.
Per-instance options layer on top of global defaults:

```go
// Simple usage - no options needed (global defaults apply)
tracedSvc := trace.NewUserServiceWithTracing(userSvc)

// With per-instance context decorator (runs after global context decorator)
tracedSvc := trace.NewUserServiceWithTracing(userSvc,
    trace.WithContextDecorator(func(ctx context.Context, span ddtrace.Span) {
        span.SetTag("payment.gateway", "stripe")
    }),
)

// With custom span decorator (for result-based tags)
tracedSvc := trace.NewUserServiceWithTracing(userSvc,
    trace.WithSpanDecorator(func(span ddtrace.Span, params, results map[string]interface{}) {
        if userID, ok := params["id"].(string); ok {
            span.SetTag("user.id", userID)
        }
    }),
)

// With additional span options
tracedSvc := trace.NewUserServiceWithTracing(userSvc,
    trace.WithSpanOptions(tracer.ServiceName("user-service")),
)

// Combine multiple options
tracedSvc := trace.NewUserServiceWithTracing(userSvc,
    trace.WithSpanOptions(tracer.ServiceName("user-service")),
    trace.WithSpanDecorator(myCustomDecorator),
    trace.WithContextDecorator(myContextDecorator),
)
```
