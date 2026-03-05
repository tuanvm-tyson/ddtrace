package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	"golang.org/x/mod/modfile"
	"gopkg.in/yaml.v3"
)

const FileName = ".ddtrace.yaml"

// Config represents the top-level .ddtrace.yaml configuration.
type Config struct {
	// Output is the default output subdirectory relative to each source package (default: "trace").
	Output string `yaml:"output"`

	// NoGenerate disables //go:generate tags in generated files.
	NoGenerate bool `yaml:"no-generate"`

	// Exclude lists path segments to skip when expanding "..." patterns.
	// A package is excluded if any segment in its import path matches an entry.
	// For example, "mock" excludes "app/service/mock" and "app/service/mock/sub"
	// but NOT "app/mockservice".
	// The output directory (e.g. "trace") is always excluded automatically.
	Exclude []string `yaml:"exclude"`

	// Packages maps package import paths (or patterns ending in /...) to per-package config.
	Packages map[string]*PackageConfig `yaml:"packages"`
}

// PackageConfig holds per-package generation settings.
type PackageConfig struct {
	// Output overrides the global output subdirectory for this package.
	Output string `yaml:"output"`

	// Interfaces maps interface names to per-interface config.
	// If nil/empty, all discovered interfaces are included.
	Interfaces map[string]*InterfaceConfig `yaml:"interfaces"`
}

// InterfaceConfig holds per-interface generation settings.
type InterfaceConfig struct {
	// Ignore skips this interface during generation.
	Ignore bool `yaml:"ignore"`

	// DecoratorName overrides the generated decorator struct name.
	DecoratorName string `yaml:"decorator-name"`

	// SpanPrefix overrides the span name prefix used in tracing.
	SpanPrefix string `yaml:"span-prefix"`
}

// ResolvedPackage is a single package to process after pattern expansion.
type ResolvedPackage struct {
	// ImportPath is the fully-qualified Go import path.
	ImportPath string

	// Config is the merged package-level configuration.
	Config PackageConfig
}

// Load reads and parses a .ddtrace.yaml file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read config file")
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, errors.Wrap(err, "failed to parse config file")
	}

	if cfg.Output == "" {
		cfg.Output = "trace"
	}

	return &cfg, nil
}

// Find walks up from the current working directory looking for .ddtrace.yaml.
// Returns the absolute path if found, or empty string if not found.
func Find() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}

	for {
		candidate := filepath.Join(dir, FileName)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return ""
}

// ResolvePackages expands package patterns (e.g. ./...) and returns
// a flat list of concrete packages to process.
func (c *Config) ResolvePackages() ([]ResolvedPackage, error) {
	if len(c.Packages) == 0 {
		return nil, errors.New("no packages defined in config")
	}

	var result []ResolvedPackage

	for pattern, pkgCfg := range c.Packages {
		if pkgCfg == nil {
			pkgCfg = &PackageConfig{}
		}

		merged := c.mergePackageConfig(pkgCfg)

		if strings.HasSuffix(pattern, "/...") {
			paths, err := expandWildcardPackages(pattern)
			if err != nil {
				return nil, errors.Wrapf(err, "failed to resolve pattern %q", pattern)
			}
			outputSuffix := "/" + merged.Output
			for _, p := range paths {
				if strings.HasSuffix(p, outputSuffix) || strings.Contains(p, outputSuffix+"/") {
					continue
				}
				if c.shouldExclude(p) {
					continue
				}
				result = append(result, ResolvedPackage{
					ImportPath: p,
					Config:     merged,
				})
			}
		} else {
			result = append(result, ResolvedPackage{
				ImportPath: pattern,
				Config:     merged,
			})
		}
	}

	return result, nil
}

// shouldExclude returns true if importPath contains a path segment matching
// any entry in Config.Exclude. Matching is exact per segment.
func (c *Config) shouldExclude(importPath string) bool {
	for _, seg := range c.Exclude {
		suffix := "/" + seg
		if strings.HasSuffix(importPath, suffix) || strings.Contains(importPath, suffix+"/") {
			return true
		}
	}
	return false
}

func (c *Config) mergePackageConfig(pkgCfg *PackageConfig) PackageConfig {
	merged := *pkgCfg
	if merged.Output == "" {
		merged.Output = c.Output
	}
	return merged
}

// findModuleRoot walks up from cwd to find go.mod and returns (module path, directory).
func findModuleRoot() (modulePath string, rootDir string, err error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", "", errors.Wrap(err, "failed to get working directory")
	}

	for {
		goModPath := filepath.Join(dir, "go.mod")
		data, err := os.ReadFile(goModPath)
		if err == nil {
			mf, err := modfile.ParseLax(goModPath, data, nil)
			if err != nil {
				return "", "", errors.Wrap(err, "failed to parse go.mod")
			}
			return mf.Module.Mod.Path, dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", "", errors.New("go.mod not found")
}

// expandWildcardPackages finds all Go packages under the given import path prefix
// by walking the filesystem instead of using `go list`.
func expandWildcardPackages(pattern string) ([]string, error) {
	prefix := strings.TrimSuffix(pattern, "/...")

	modulePath, rootDir, err := findModuleRoot()
	if err != nil {
		return nil, err
	}

	if !strings.HasPrefix(prefix, modulePath) {
		return nil, errors.Errorf("pattern %q is outside module %q", pattern, modulePath)
	}

	relPath := strings.TrimPrefix(prefix, modulePath)
	relPath = strings.TrimPrefix(relPath, "/")
	searchDir := filepath.Join(rootDir, relPath)

	var paths []string
	err = filepath.WalkDir(searchDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible directories
		}
		if !d.IsDir() {
			return nil
		}
		// skip hidden directories and testdata
		name := d.Name()
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" || name == "vendor" {
			return filepath.SkipDir
		}
		// check if directory contains .go files
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
				rel, _ := filepath.Rel(rootDir, path)
				importPath := modulePath + "/" + filepath.ToSlash(rel)
				paths = append(paths, importPath)
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, errors.Wrapf(err, "failed to walk %s", searchDir)
	}

	return paths, nil
}
