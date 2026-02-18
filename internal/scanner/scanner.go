package scanner

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
)

const ignoreDirective = "//ddtrace:ignore"

// InterfaceInfo represents a discovered interface in a source file.
type InterfaceInfo struct {
	Name string
}

// FileInterfaces represents all non-ignored interfaces found in a single source file.
type FileInterfaces struct {
	FileName   string
	Interfaces []InterfaceInfo
}

// ScanPackage scans all files in a package and returns interfaces grouped by file.
// Interfaces annotated with //ddtrace:ignore are excluded.
// Files ending in _test.go or _trace.go are skipped.
func ScanPackage(p *Package) ([]FileInterfaces, error) {
	var result []FileInterfaces

	fileNames := make([]string, 0, len(p.Files))
	for name := range p.Files {
		fileNames = append(fileNames, name)
	}
	sort.Strings(fileNames)

	for _, fullPath := range fileNames {
		baseName := filepath.Base(fullPath)

		if strings.HasSuffix(baseName, "_test.go") || strings.HasSuffix(baseName, "_trace.go") {
			continue
		}

		f := p.Files[fullPath]
		if f == nil {
			continue
		}

		interfaces := scanFile(f)
		if len(interfaces) == 0 {
			continue
		}

		result = append(result, FileInterfaces{
			FileName:   baseName,
			Interfaces: interfaces,
		})
	}

	return result, nil
}

func scanFile(f *ast.File) []InterfaceInfo {
	var interfaces []InterfaceInfo

	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}

		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}

			if _, isIface := ts.Type.(*ast.InterfaceType); !isIface {
				continue
			}

			if hasIgnoreDirective(gd.Doc) || hasIgnoreDirective(ts.Doc) {
				continue
			}

			interfaces = append(interfaces, InterfaceInfo{
				Name: ts.Name.Name,
			})
		}
	}

	return interfaces
}

func hasIgnoreDirective(cg *ast.CommentGroup) bool {
	if cg == nil {
		return false
	}
	for _, c := range cg.List {
		if strings.TrimSpace(c.Text) == ignoreDirective {
			return true
		}
	}
	return false
}
