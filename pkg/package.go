package pkg

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"

	"golang.org/x/tools/go/packages"
)

var errPackageNotFound = errors.New("package not found")

// loadMode is the standard set of information requested from the Go toolchain.
var loadMode = packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedImports | packages.NeedDeps

type Package struct {
	Name  string
	Files map[string]*ast.File
}

// Load loads package by its import path
func Load(path string) (*packages.Package, error) {
	cfg := &packages.Config{Mode: loadMode}
	pkgs, err := packages.Load(cfg, path)
	if err != nil {
		return nil, err
	}

	if len(pkgs) < 1 {
		return nil, errPackageNotFound
	}

	if len(pkgs[0].Errors) > 0 {
		return nil, pkgs[0].Errors[0]
	}

	return pkgs[0], nil
}

// LoadAll loads multiple packages in a single batch call.
// This is significantly faster than calling Load in a loop because the Go
// toolchain resolves the shared dependency graph once for all packages.
// Packages that fail to load (e.g., no Go files) are silently omitted.
func LoadAll(paths []string) (map[string]*packages.Package, error) {
	if len(paths) == 0 {
		return nil, nil
	}

	cfg := &packages.Config{Mode: loadMode}
	pkgs, err := packages.Load(cfg, paths...)
	if err != nil {
		return nil, err
	}

	result := make(map[string]*packages.Package, len(pkgs))
	for _, p := range pkgs {
		if len(p.Errors) > 0 {
			continue
		}
		result[p.PkgPath] = p
	}
	return result, nil
}

// AST returns package's abstract syntax tree
func AST(fs *token.FileSet, p *packages.Package) (*Package, error) {
	dir := Dir(p)

	pkgs, err := parser.ParseDir(fs, dir, nil, parser.DeclarationErrors|parser.ParseComments)
	if err != nil {
		return nil, err
	}

	if ap, ok := pkgs[p.Name]; ok {
		return &Package{
			Name:  p.Name,
			Files: ap.Files,
		}, nil
	}

	return &Package{Name: p.Name}, nil
}

// Dir returns absolute path of the package in a filesystem
func Dir(p *packages.Package) string {
	files := append(p.GoFiles, p.OtherFiles...)
	if len(files) < 1 {
		return p.PkgPath
	}

	return filepath.Dir(files[0])
}
