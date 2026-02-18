package codegen

import (
	"go/token"
	"sync"

	"golang.org/x/tools/go/packages"

	"github.com/moneyforward/ddtrace/internal/scanner"
)

// PackageCache caches loaded packages and parsed ASTs to avoid redundant
// work when multiple interfaces reference the same external packages.
// All methods are safe for concurrent use by multiple goroutines.
type PackageCache struct {
	mu     sync.RWMutex
	loaded map[string]*packages.Package
	asts   map[string]*scanner.Package
}

// NewPackageCache returns an empty PackageCache.
func NewPackageCache() *PackageCache {
	return &PackageCache{
		loaded: make(map[string]*packages.Package),
		asts:   make(map[string]*scanner.Package),
	}
}

// Seed pre-populates the cache with already-loaded packages (e.g. from batch loading).
func (c *PackageCache) Seed(pkgs map[string]*packages.Package) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for path, p := range pkgs {
		c.loaded[path] = p
	}
}

func (c *PackageCache) load(path string) (*packages.Package, error) {
	c.mu.RLock()
	if p, ok := c.loaded[path]; ok {
		c.mu.RUnlock()
		return p, nil
	}
	c.mu.RUnlock()

	p, err := scanner.Load(path)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.loaded[path] = p
	c.mu.Unlock()
	return p, nil
}

func (c *PackageCache) ast(fs *token.FileSet, p *packages.Package) (*scanner.Package, error) {
	c.mu.RLock()
	if a, ok := c.asts[p.PkgPath]; ok {
		c.mu.RUnlock()
		return a, nil
	}
	c.mu.RUnlock()

	a, err := scanner.AST(fs, p)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.asts[p.PkgPath] = a
	c.mu.Unlock()
	return a, nil
}
