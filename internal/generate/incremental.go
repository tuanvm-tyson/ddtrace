package generate

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"
)

// findModuleRoot walks up from startDir to find go.mod and returns the
// directory containing it (module root) and the module path declared in it.
func findModuleRoot(startDir string) (rootDir, modulePath string, err error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", "", err
	}

	for {
		gomod := filepath.Join(dir, "go.mod")
		data, err := os.ReadFile(gomod)
		if err == nil {
			modPath := parseModulePath(data)
			if modPath != "" {
				return dir, modPath, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", "", errors.New("go.mod not found")
}

// parseModulePath extracts the module path from go.mod contents.
func parseModulePath(gomod []byte) string {
	for _, line := range strings.Split(string(gomod), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(line[len("module "):])
		}
	}
	return ""
}

// importPathToDir converts a fully-qualified Go import path to a filesystem
// directory by stripping the module prefix and joining with the module root.
// Returns "" if the import path is not within the module.
func importPathToDir(moduleRoot, modulePath, importPath string) string {
	if !strings.HasPrefix(importPath, modulePath) {
		return ""
	}
	rel := strings.TrimPrefix(importPath, modulePath)
	return filepath.Join(moduleRoot, filepath.FromSlash(rel))
}

// fileModTime returns the modification time of a file, or zero time on error.
func fileModTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// newestTraceModTime returns the newest modification time among *_trace.go
// files in dir.
func newestTraceModTime(dir string) time.Time {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return time.Time{}
	}
	var newest time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), TraceSuffix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return newest
}

// sourceNewerThan checks whether any non-test .go file in srcDir has a
// modification time strictly after threshold.
func sourceNewerThan(srcDir string, threshold time.Time) bool {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return true
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, TestSuffix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return true
		}
		if info.ModTime().After(threshold) {
			return true
		}
	}
	return false
}
