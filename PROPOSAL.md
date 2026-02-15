# Proposal: Interface-Based Tracing Pattern for Go Services

**Author:** Platform Engineering Team  
**Date:** January 2026  
**Status:** Draft  

---

## Executive Summary

This document proposes adopting the **Interface Decorator Pattern** with code generation for distributed tracing instrumentation in Go services. This approach uses the `ddtrace` tool to automatically generate tracing wrappers for Go interfaces, reducing boilerplate while maintaining type safety and testability.

---

## Table of Contents

1. [Problem Statement](#problem-statement)
2. [Proposed Solution](#proposed-solution)
3. [Alternative Approaches](#alternative-approaches)
4. [Detailed Comparison](#detailed-comparison)
5. [Implementation Examples](#implementation-examples)
6. [Recommendation](#recommendation)

---

## Problem Statement

Adding observability to Go services requires instrumenting code with tracing spans. Current challenges include:

- **Boilerplate overhead**: Each method requires 5-10 lines of tracing code
- **Inconsistency**: Different developers implement tracing differently
- **Maintenance burden**: Tracing code interleaved with business logic
- **Error-prone**: Easy to forget span finishing, error tagging, or context propagation
- **Testing complexity**: Hard to unit test business logic in isolation from tracing

---

## Proposed Solution

### Interface Decorator Pattern with Code Generation

Use the `ddtrace` CLI tool to automatically generate tracing decorators for Go interfaces.

```bash
# Generate tracing wrapper for UserService interface
ddtrace gen -p ./service -i UserService -o ./service/user_service_tracing.go
```

### How It Works

```
┌─────────────────┐     ┌──────────────────────┐     ┌─────────────────┐
│   Application   │────▶│  GeneratedDecorator  │────▶│  Original Impl  │
│                 │     │  (tracing wrapper)   │     │                 │
└─────────────────┘     └──────────────────────┘     └─────────────────┘
                               │
                               ▼
                        ┌─────────────┐
                        │   DataDog   │
                        │     APM     │
                        └─────────────┘
```

### Example

**Input Interface:**

```go
// service/user_service.go
package service

type UserService interface {
    GetUser(ctx context.Context, id string) (*User, error)
    CreateUser(ctx context.Context, req CreateUserRequest) (*User, error)
    DeleteUser(ctx context.Context, id string) error
}
```

**Generated Decorator:**

```go
// service/user_service_tracing.go (auto-generated)
package service

import (
    "context"
    "gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
    "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
    "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

type UserServiceWithTracing struct {
    UserService
    _spanDecorator func(span ddtrace.Span, params, results map[string]interface{})
}

func NewUserServiceWithTracing(base UserService, spanDecorator ...func(span ddtrace.Span, params, results map[string]interface{})) UserServiceWithTracing {
    d := UserServiceWithTracing{UserService: base}
    if len(spanDecorator) > 0 && spanDecorator[0] != nil {
        d._spanDecorator = spanDecorator[0]
    }
    return d
}

func (_d UserServiceWithTracing) GetUser(ctx context.Context, id string) (u1 *User, err error) {
    span, ctx := tracer.StartSpanFromContext(ctx, "UserService.GetUser")
    defer func() {
        if _d._spanDecorator != nil {
            _d._spanDecorator(span, map[string]interface{}{"ctx": ctx, "id": id}, 
                              map[string]interface{}{"u1": u1, "err": err})
        } else if err != nil {
            span.SetTag(ext.Error, err)
            span.SetTag(ext.ErrorMsg, err.Error())
            span.SetTag(ext.ErrorType, "error")
        }
        span.Finish()
    }()
    return _d.UserService.GetUser(ctx, id)
}

// ... similar for CreateUser, DeleteUser
```

**Usage in Application:**

```go
func main() {
    // Create base implementation
    userRepo := repository.NewUserRepository(db)
    userService := service.NewUserServiceImpl(userRepo)
    
    // Wrap with tracing decorator
    tracedService := service.NewUserServiceWithTracing(userService)
    
    // Use traced service
    app := NewApp(tracedService)
    app.Run()
}
```

---

## Alternative Approaches

### Approach 1: Manual Instrumentation

Directly embed tracing code in each method.

```go
func (s *UserServiceImpl) GetUser(ctx context.Context, id string) (*User, error) {
    span, ctx := tracer.StartSpanFromContext(ctx, "UserService.GetUser")
    defer span.Finish()
    
    span.SetTag("user.id", id)
    
    user, err := s.repo.FindByID(ctx, id)
    if err != nil {
        span.SetTag(ext.Error, err)
        span.SetTag(ext.ErrorMsg, err.Error())
        return nil, err
    }
    
    return user, nil
}
```

| Pros | Cons |
|------|------|
| ✅ Full control over span attributes | ❌ Repetitive boilerplate in every method |
| ✅ No external tooling required | ❌ Business logic mixed with tracing code |
| ✅ Easy to customize per-method | ❌ Easy to forget span.Finish() or error handling |
| | ❌ Hard to maintain consistency across team |
| | ❌ Difficult to test business logic in isolation |

---

### Approach 2: Middleware/Interceptor Pattern (gRPC)

Use gRPC interceptors for automatic tracing.

```go
import grpctrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/google.golang.org/grpc"

func main() {
    // Server-side interceptor
    server := grpc.NewServer(
        grpc.UnaryInterceptor(grpctrace.UnaryServerInterceptor()),
        grpc.StreamInterceptor(grpctrace.StreamServerInterceptor()),
    )
    
    // Client-side interceptor
    conn, _ := grpc.Dial(addr,
        grpc.WithUnaryInterceptor(grpctrace.UnaryClientInterceptor()),
    )
}
```

| Pros | Cons |
|------|------|
| ✅ Zero code changes in business logic | ❌ Only works for gRPC/HTTP boundaries |
| ✅ Automatic for all endpoints | ❌ No visibility into internal service layers |
| ✅ Consistent across all services | ❌ Cannot add custom span attributes easily |
| ✅ Official DataDog support | ❌ Coarse-grained spans only |

---

### Approach 3: OpenTelemetry Auto-Instrumentation

Use OpenTelemetry SDK with contrib packages.

```go
import (
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/trace"
    "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func (s *UserServiceImpl) GetUser(ctx context.Context, id string) (*User, error) {
    ctx, span := otel.Tracer("user-service").Start(ctx, "GetUser")
    defer span.End()
    
    span.SetAttributes(attribute.String("user.id", id))
    
    user, err := s.repo.FindByID(ctx, id)
    if err != nil {
        span.RecordError(err)
        span.SetStatus(codes.Error, err.Error())
        return nil, err
    }
    
    return user, nil
}
```

| Pros | Cons |
|------|------|
| ✅ Vendor-agnostic (OTLP protocol) | ❌ Still requires manual instrumentation |
| ✅ Rich ecosystem of contrib packages | ❌ More verbose API than DataDog native |
| ✅ Future-proof standard | ❌ Additional translation layer to DataDog |
| ✅ Can export to multiple backends | ❌ Slightly higher overhead |

---

### Approach 4: Aspect-Oriented Programming (Reflection-based)

Use runtime reflection to wrap functions dynamically.

```go
import "github.com/some/aop-library"

func main() {
    userService := &UserServiceImpl{}
    
    // Wrap with tracing aspect at runtime
    traced := aop.Wrap(userService, TracingAspect{})
}

type TracingAspect struct{}

func (a TracingAspect) Before(ctx context.Context, method string, args []interface{}) context.Context {
    span, ctx := tracer.StartSpanFromContext(ctx, method)
    return context.WithValue(ctx, "span", span)
}

func (a TracingAspect) After(ctx context.Context, results []interface{}, err error) {
    span := ctx.Value("span").(ddtrace.Span)
    if err != nil {
        span.SetTag(ext.Error, err)
    }
    span.Finish()
}
```

| Pros | Cons |
|------|------|
| ✅ No code generation step | ❌ Runtime overhead from reflection |
| ✅ Dynamic, can be toggled at runtime | ❌ Loss of compile-time type safety |
| ✅ Flexible aspect composition | ❌ Harder to debug (magic behavior) |
| | ❌ Not idiomatic Go |
| | ❌ Limited library support in Go |

---

### Approach 5: Code Generation (DDTrace - Proposed)

Generate type-safe decorators at build time.

```bash
//go:generate ddtrace gen -p . -i UserService -o user_service_tracing.go
```

| Pros | Cons |
|------|------|
| ✅ Zero runtime overhead | ❌ Requires build-time code generation |
| ✅ Full compile-time type safety | ❌ Generated code must be committed/reviewed |
| ✅ Clean separation of concerns | ❌ Only traces context-aware methods |
| ✅ Business logic remains testable | ❌ Tied to DataDog (currently) |
| ✅ Consistent tracing across all interfaces | ❌ Additional tooling in CI/CD |
| ✅ Supports Go generics | |
| ✅ Custom span decorators for flexibility | |

---

## Detailed Comparison

### Comparison Matrix

| Criteria | Manual | Middleware | OpenTelemetry | AOP | DDTrace (Proposed) |
|----------|--------|------------|---------------|-----|-------------------|
| **Boilerplate** | High | None | Medium | Low | None |
| **Type Safety** | ✅ | ✅ | ✅ | ❌ | ✅ |
| **Runtime Overhead** | Low | Low | Medium | High | Low |
| **Granularity** | Fine | Coarse | Fine | Fine | Fine |
| **Testability** | Poor | N/A | Poor | Medium | Excellent |
| **Consistency** | Low | High | Medium | Medium | High |
| **Flexibility** | High | Low | High | High | Medium |
| **Learning Curve** | Low | Low | Medium | High | Low |
| **Vendor Lock-in** | High | Medium | Low | Low | High |
| **Go Idiomaticity** | ✅ | ✅ | ✅ | ❌ | ✅ |

### Effort Comparison (10 Service Methods)

| Approach | Initial Setup | Per-Method Effort | Total LOC |
|----------|---------------|-------------------|-----------|
| Manual | 0 | ~8 lines | ~80 lines |
| Middleware | ~10 lines | 0 | ~10 lines |
| OpenTelemetry | ~20 lines | ~6 lines | ~80 lines |
| DDTrace | ~1 command | 0 | ~5 lines (usage) |

---

## Implementation Examples

### Complete Example: User Service

#### Step 1: Define Interface

```go
// service/interfaces.go
package service

import "context"

//go:generate ddtrace gen -p . -i UserService,OrderService -o tracing_decorators.go

type UserService interface {
    GetUser(ctx context.Context, id string) (*User, error)
    CreateUser(ctx context.Context, req CreateUserRequest) (*User, error)
    UpdateUser(ctx context.Context, id string, req UpdateUserRequest) (*User, error)
    DeleteUser(ctx context.Context, id string) error
    ListUsers(ctx context.Context, filter UserFilter) ([]*User, error)
}

type OrderService interface {
    CreateOrder(ctx context.Context, req CreateOrderRequest) (*Order, error)
    GetOrder(ctx context.Context, id string) (*Order, error)
    CancelOrder(ctx context.Context, id string) error
}
```

#### Step 2: Implement Business Logic (Clean, No Tracing)

```go
// service/user_service_impl.go
package service

type userServiceImpl struct {
    repo   UserRepository
    cache  Cache
    events EventPublisher
}

func NewUserService(repo UserRepository, cache Cache, events EventPublisher) UserService {
    return &userServiceImpl{repo: repo, cache: cache, events: events}
}

func (s *userServiceImpl) GetUser(ctx context.Context, id string) (*User, error) {
    // Pure business logic - no tracing concerns
    if cached, ok := s.cache.Get(ctx, id); ok {
        return cached.(*User), nil
    }
    
    user, err := s.repo.FindByID(ctx, id)
    if err != nil {
        return nil, fmt.Errorf("failed to get user: %w", err)
    }
    
    s.cache.Set(ctx, id, user, 5*time.Minute)
    return user, nil
}

// ... other methods
```

#### Step 3: Generate Tracing Decorators

```bash
$ go generate ./service/...
```

#### Step 4: Wire Dependencies with Tracing

```go
// cmd/server/main.go
package main

func main() {
    // Initialize tracer
    tracer.Start(
        tracer.WithService("user-service"),
        tracer.WithEnv("production"),
    )
    defer tracer.Stop()
    
    // Build dependency graph
    db := database.Connect()
    cache := redis.NewClient()
    events := kafka.NewPublisher()
    
    // Create implementations
    userRepo := repository.NewUserRepository(db)
    userService := service.NewUserService(userRepo, cache, events)
    
    // Wrap with tracing (one line per service!)
    tracedUserService := service.NewUserServiceWithTracing(userService)
    
    // Optional: Add custom span decorator for business metrics
    tracedUserService := service.NewUserServiceWithTracing(userService, 
        func(span ddtrace.Span, params, results map[string]interface{}) {
            if userID, ok := params["id"].(string); ok {
                span.SetTag("user.id", userID)
            }
        },
    )
    
    // Use in handlers
    handler := api.NewHandler(tracedUserService)
    http.ListenAndServe(":8080", handler)
}
```

#### Step 5: Unit Test (Without Tracing Concerns)

```go
// service/user_service_test.go
package service

func TestGetUser(t *testing.T) {
    // Test pure business logic without any tracing setup
    mockRepo := &MockUserRepository{}
    mockCache := &MockCache{}
    mockEvents := &MockEventPublisher{}
    
    svc := NewUserService(mockRepo, mockCache, mockEvents)
    
    mockRepo.On("FindByID", mock.Anything, "123").Return(&User{ID: "123"}, nil)
    
    user, err := svc.GetUser(context.Background(), "123")
    
    assert.NoError(t, err)
    assert.Equal(t, "123", user.ID)
}
```

---

## Migration Strategy

### Phase 1: New Services (Week 1-2)
- Adopt `ddtrace` for all new service interfaces
- Update project templates with `//go:generate` directives
- Add `ddtrace` to CI/CD pipeline

### Phase 2: Critical Services (Week 3-4)
- Identify top 5 services by traffic
- Extract interfaces from existing implementations
- Generate tracing decorators
- A/B test tracing overhead

### Phase 3: Full Rollout (Week 5-8)
- Apply pattern to remaining services
- Remove manual tracing code
- Document best practices

---

## CI/CD Integration

### Makefile

```makefile
.PHONY: generate
generate:
	go generate ./...

.PHONY: verify-generate
verify-generate: generate
	git diff --exit-code || (echo "Generated files are out of date. Run 'make generate'" && exit 1)
```

### GitHub Actions

```yaml
- name: Verify generated code
  run: |
    go install github.com/tyson-tuanvm/ddtrace/cmd/ddtrace@latest
    make verify-generate
```

---

## Recommendation

**We recommend adopting the DDTrace code generation approach** for the following reasons:

1. **Best Developer Experience**: Zero boilerplate, clean business logic
2. **Compile-Time Safety**: No runtime reflection, full type checking
3. **Testability**: Business logic can be unit tested without tracing setup
4. **Consistency**: Enforced uniform tracing across all services
5. **Performance**: No runtime overhead from reflection or AOP

### When to Use Other Approaches

| Scenario | Recommended Approach |
|----------|---------------------|
| HTTP/gRPC boundaries only | Middleware interceptors |
| Multi-vendor export needed | OpenTelemetry + DDTrace decorators |
| Fine-grained custom attributes | Manual + DDTrace decorators |
| Legacy code without interfaces | Manual instrumentation |

---

## Appendix

### Installation

```bash
go install github.com/tyson-tuanvm/ddtrace/cmd/ddtrace@latest
```

### References

- [DDTrace Repository](https://github.com/tyson-tuanvm/ddtrace)
- [DataDog Go Tracing](https://docs.datadoghq.com/tracing/setup_overview/setup/go/)
- [OpenTelemetry Go](https://opentelemetry.io/docs/instrumentation/go/)
- [Go Generate](https://go.dev/blog/generate)


