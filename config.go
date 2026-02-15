package ddtrace

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	"gopkg.in/yaml.v3"
)

const configFileName = ".ddtrace.yaml"

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

// LoadConfig reads and parses a .ddtrace.yaml file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.Wrap(err, "failed to read config file")
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, errors.Wrap(err, "failed to parse config file")
	}

	// Apply defaults
	if cfg.Output == "" {
		cfg.Output = "trace"
	}

	return &cfg, nil
}

// FindConfig walks up from the current working directory looking for .ddtrace.yaml.
// Returns the absolute path if found, or empty string if not found.
func FindConfig() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}

	for {
		candidate := filepath.Join(dir, configFileName)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached filesystem root
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
		// Ensure we have a non-nil config
		if pkgCfg == nil {
			pkgCfg = &PackageConfig{}
		}

		// Merge global defaults into package config
		merged := c.mergePackageConfig(pkgCfg)

		if strings.HasSuffix(pattern, "/...") {
			// Recursive pattern: expand via go list
			paths, err := goListPackages(pattern)
			if err != nil {
				return nil, errors.Wrapf(err, "failed to resolve pattern %q", pattern)
			}
			// Filter out sub-packages that should not contain source interfaces:
			// - output directories (e.g. /trace) are always excluded automatically
			// - user-defined exclude patterns from config (e.g. mock, dto)
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
// any entry in Config.Exclude. Matching is exact per segment: "mock" matches
// "app/mock" and "app/mock/sub" but not "app/mockservice".
func (c *Config) shouldExclude(importPath string) bool {
	for _, seg := range c.Exclude {
		suffix := "/" + seg
		if strings.HasSuffix(importPath, suffix) || strings.Contains(importPath, suffix+"/") {
			return true
		}
	}
	return false
}

// mergePackageConfig applies global defaults to a package config.
func (c *Config) mergePackageConfig(pkgCfg *PackageConfig) PackageConfig {
	merged := *pkgCfg
	if merged.Output == "" {
		merged.Output = c.Output
	}
	return merged
}

// goListPackages uses `go list` to expand a package pattern.
func goListPackages(pattern string) ([]string, error) {
	cmd := exec.Command("go", "list", pattern)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, errors.Wrapf(err, "go list %s: %s", pattern, stderr.String())
	}

	var paths []string
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			paths = append(paths, line)
		}
	}

	return paths, nil
}
