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

type UserServiceWithTracing struct { ... }
func NewUserServiceWithTracing(base service.UserService, instance string, ...) { ... }

type OrderServiceWithTracing struct { ... }
func NewOrderServiceWithTracing(base service.OrderService, instance string, ...) { ... }
```

4. Use the traced wrapper in your application:

```go
import "myapp/service/trace"

userSvc := service.NewUserServiceImpl(repo)
tracedSvc := trace.NewUserServiceWithTracing(userSvc, "user-service")
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

## Custom Span Decorators

Add custom span attributes using the optional span decorator:

```go
tracedSvc := trace.NewUserServiceWithTracing(userSvc, "user-service",
    func(span ddtrace.Span, params, results map[string]interface{}) {
        if userID, ok := params["id"].(string); ok {
            span.SetTag("user.id", userID)
        }
    },
)
```
