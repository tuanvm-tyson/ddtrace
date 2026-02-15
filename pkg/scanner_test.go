package pkg

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parseSource(t *testing.T, filename, src string) *Package {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
	require.NoError(t, err)
	return &Package{
		Name:  "testpkg",
		Files: map[string]*ast.File{filename: f},
	}
}

func TestScanPackage_AllInterfaces(t *testing.T) {
	p := parseSource(t, "service.go", `
package testpkg

import "context"

type UserService interface {
	GetUser(ctx context.Context, id string) error
}

type OrderService interface {
	CreateOrder(ctx context.Context) error
}
`)

	result, err := ScanPackage(p)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "service.go", result[0].FileName)
	assert.Len(t, result[0].Interfaces, 2)
	assert.Equal(t, "UserService", result[0].Interfaces[0].Name)
	assert.Equal(t, "OrderService", result[0].Interfaces[1].Name)
}

func TestScanPackage_IgnoreDirective(t *testing.T) {
	p := parseSource(t, "service.go", `
package testpkg

import "context"

type UserService interface {
	GetUser(ctx context.Context, id string) error
}

//ddtrace:ignore
type InternalHelper interface {
	DoSomething(ctx context.Context) error
}

type OrderService interface {
	CreateOrder(ctx context.Context) error
}
`)

	result, err := ScanPackage(p)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Len(t, result[0].Interfaces, 2)
	assert.Equal(t, "UserService", result[0].Interfaces[0].Name)
	assert.Equal(t, "OrderService", result[0].Interfaces[1].Name)
}

func TestScanPackage_IgnoreOnTypeSpec(t *testing.T) {
	// Test ignore directive on TypeSpec.Doc (inside grouped type declaration)
	p := parseSource(t, "service.go", `
package testpkg

import "context"

type (
	UserService interface {
		GetUser(ctx context.Context, id string) error
	}

	//ddtrace:ignore
	InternalHelper interface {
		DoSomething(ctx context.Context) error
	}
)
`)

	result, err := ScanPackage(p)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Len(t, result[0].Interfaces, 1)
	assert.Equal(t, "UserService", result[0].Interfaces[0].Name)
}

func TestScanPackage_NoInterfaces(t *testing.T) {
	p := parseSource(t, "types.go", `
package testpkg

type User struct {
	Name string
}

type Status int
`)

	result, err := ScanPackage(p)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestScanPackage_NonInterfaceTypesIgnored(t *testing.T) {
	p := parseSource(t, "mixed.go", `
package testpkg

import "context"

type User struct {
	Name string
}

type UserService interface {
	GetUser(ctx context.Context, id string) error
}

type Status int
`)

	result, err := ScanPackage(p)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Len(t, result[0].Interfaces, 1)
	assert.Equal(t, "UserService", result[0].Interfaces[0].Name)
}

func TestScanPackage_SkipTestFiles(t *testing.T) {
	fset := token.NewFileSet()
	f1, err := parser.ParseFile(fset, "service.go", `
package testpkg

import "context"

type UserService interface {
	GetUser(ctx context.Context, id string) error
}
`, parser.ParseComments)
	require.NoError(t, err)

	f2, err := parser.ParseFile(fset, "service_test.go", `
package testpkg

import "context"

type MockService interface {
	GetUser(ctx context.Context, id string) error
}
`, parser.ParseComments)
	require.NoError(t, err)

	p := &Package{
		Name: "testpkg",
		Files: map[string]*ast.File{
			"service.go":      f1,
			"service_test.go": f2,
		},
	}

	result, err := ScanPackage(p)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "service.go", result[0].FileName)
	assert.Len(t, result[0].Interfaces, 1)
	assert.Equal(t, "UserService", result[0].Interfaces[0].Name)
}

func TestScanPackage_SkipTraceFiles(t *testing.T) {
	fset := token.NewFileSet()
	f1, err := parser.ParseFile(fset, "service.go", `
package testpkg

import "context"

type UserService interface {
	GetUser(ctx context.Context, id string) error
}
`, parser.ParseComments)
	require.NoError(t, err)

	f2, err := parser.ParseFile(fset, "service_trace.go", `
package testpkg

type UserServiceWithTracing struct {}
`, parser.ParseComments)
	require.NoError(t, err)

	p := &Package{
		Name: "testpkg",
		Files: map[string]*ast.File{
			"service.go":       f1,
			"service_trace.go": f2,
		},
	}

	result, err := ScanPackage(p)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "service.go", result[0].FileName)
}

func TestScanPackage_MultipleFiles(t *testing.T) {
	fset := token.NewFileSet()
	f1, err := parser.ParseFile(fset, "interfaces.go", `
package testpkg

import "context"

type UserService interface {
	GetUser(ctx context.Context, id string) error
}

type OrderService interface {
	CreateOrder(ctx context.Context) error
}
`, parser.ParseComments)
	require.NoError(t, err)

	f2, err := parser.ParseFile(fset, "repository.go", `
package testpkg

import "context"

type UserRepository interface {
	FindByID(ctx context.Context, id string) error
}
`, parser.ParseComments)
	require.NoError(t, err)

	p := &Package{
		Name: "testpkg",
		Files: map[string]*ast.File{
			"interfaces.go": f1,
			"repository.go": f2,
		},
	}

	result, err := ScanPackage(p)
	require.NoError(t, err)
	require.Len(t, result, 2)

	// Sorted by filename
	assert.Equal(t, "interfaces.go", result[0].FileName)
	assert.Len(t, result[0].Interfaces, 2)

	assert.Equal(t, "repository.go", result[1].FileName)
	assert.Len(t, result[1].Interfaces, 1)
	assert.Equal(t, "UserRepository", result[1].Interfaces[0].Name)
}

func TestScanPackage_EmptyInterface(t *testing.T) {
	p := parseSource(t, "empty.go", `
package testpkg

type Empty interface {}
`)

	result, err := ScanPackage(p)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "Empty", result[0].Interfaces[0].Name)
}

func TestScanPackage_AllIgnored(t *testing.T) {
	p := parseSource(t, "service.go", `
package testpkg

import "context"

//ddtrace:ignore
type UserService interface {
	GetUser(ctx context.Context, id string) error
}

//ddtrace:ignore
type OrderService interface {
	CreateOrder(ctx context.Context) error
}
`)

	result, err := ScanPackage(p)
	require.NoError(t, err)
	assert.Empty(t, result)
}

func TestScanPackage_EmptyPackage(t *testing.T) {
	p := &Package{
		Name:  "testpkg",
		Files: map[string]*ast.File{},
	}

	result, err := ScanPackage(p)
	require.NoError(t, err)
	assert.Empty(t, result)
}
