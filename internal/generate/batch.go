package generate

import (
	"go/token"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/pkg/errors"
	"golang.org/x/tools/go/packages"

	"github.com/moneyforward/ddtrace/internal/codegen"
	"github.com/moneyforward/ddtrace/internal/config"
	"github.com/moneyforward/ddtrace/internal/scanner"
)

// runWithConfig processes all packages defined in a .ddtrace.yaml config file.
func (gc *GenerateCommand) runWithConfig(cfg *config.Config, configPath string, stdout io.Writer) error {
	resolved, err := cfg.ResolvePackages()
	if err != nil {
		return errors.Wrap(err, "failed to resolve packages from config")
	}

	toProcess := resolved
	if !gc.forceRegenerate {
		moduleRoot, modulePath, modErr := findModuleRoot(filepath.Dir(configPath))
		if modErr == nil {
			configTime := fileModTime(configPath)

			var lastRunTime time.Time
			for _, rp := range resolved {
				srcDir := importPathToDir(moduleRoot, modulePath, rp.ImportPath)
				if srcDir == "" {
					continue
				}
				outDir := filepath.Join(srcDir, rp.Config.Output)
				if t := newestTraceModTime(outDir); t.After(lastRunTime) {
					lastRunTime = t
				}
			}

			var filtered []config.ResolvedPackage
			for _, rp := range resolved {
				srcDir := importPathToDir(moduleRoot, modulePath, rp.ImportPath)
				if srcDir == "" {
					filtered = append(filtered, rp)
					continue
				}
				outDir := filepath.Join(srcDir, rp.Config.Output)
				newestOutput := newestTraceModTime(outDir)

				if !newestOutput.IsZero() {
					if sourceNewerThan(srcDir, newestOutput) ||
						configTime.After(newestOutput) {
						filtered = append(filtered, rp)
					}
					continue
				}

				if lastRunTime.IsZero() ||
					sourceNewerThan(srcDir, lastRunTime) ||
					configTime.After(lastRunTime) {
					filtered = append(filtered, rp)
				}
			}
			toProcess = filtered
		}
	}

	if len(toProcess) == 0 {
		return nil
	}

	importPaths := make([]string, len(toProcess))
	for i, rp := range toProcess {
		importPaths[i] = rp.ImportPath
	}

	pkgMap, err := scanner.LoadAll(importPaths)
	if err != nil {
		return errors.Wrap(err, "failed to batch-load source packages")
	}

	sharedFS := token.NewFileSet()
	pkgCache := codegen.NewPackageCache()
	pkgCache.Seed(pkgMap)

	headerTmpl, err := template.New("header").Funcs(helperFuncs).Parse(minimalHeaderTemplate)
	if err != nil {
		return errors.Wrap(err, "failed to parse header template")
	}
	bodyTmpl, err := template.New("body").Funcs(helperFuncs).Parse(datadogTemplate)
	if err != nil {
		return errors.Wrap(err, "failed to parse body template")
	}

	workers := runtime.NumCPU()
	if workers > len(toProcess) {
		workers = len(toProcess)
	}
	sem := make(chan struct{}, workers)

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)

	for _, rp := range toProcess {
		sourcePkg, ok := pkgMap[rp.ImportPath]
		if !ok {
			continue
		}

		if len(sourcePkg.GoFiles) == 0 && len(sourcePkg.CompiledGoFiles) == 0 {
			continue
		}

		wg.Add(1)
		go func(rp config.ResolvedPackage, sourcePkg *packages.Package) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if err := gc.processPackage(rp, cfg, sourcePkg, headerTmpl, bodyTmpl, sharedFS, pkgCache); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = errors.Wrapf(err, "failed to generate for package %s", rp.ImportPath)
				}
				mu.Unlock()
			}
		}(rp, sourcePkg)
	}

	wg.Wait()
	return firstErr
}

// processPackage generates tracing decorators for a single package from config.
func (gc *GenerateCommand) processPackage(
	rp config.ResolvedPackage,
	cfg *config.Config,
	sourcePackage *packages.Package,
	headerTmpl *template.Template,
	bodyTmpl *template.Template,
	sharedFS *token.FileSet,
	pkgCache *codegen.PackageCache,
) error {
	astPkg, err := scanner.AST(sharedFS, sourcePackage)
	if err != nil {
		return errors.Wrap(err, "failed to parse source package AST")
	}

	fileGroups, err := scanner.ScanPackage(astPkg)
	if err != nil {
		return errors.Wrap(err, "failed to scan interfaces")
	}

	if len(fileGroups) == 0 {
		return nil
	}

	fileGroups = filterInterfaces(fileGroups, rp.Config)

	if len(fileGroups) == 0 {
		return nil
	}

	srcDir := scanner.Dir(sourcePackage)
	outDir := rp.Config.Output
	if !filepath.IsAbs(outDir) {
		outDir = filepath.Join(srcDir, outDir)
	}

	if err := gc.fs.MkdirAll(outDir, os.ModePerm); err != nil {
		return errors.Wrap(err, "failed to create output directory")
	}

	outPkgName := filepath.Base(outDir)
	dstPackage, err := scanner.Load(outDir)
	if err != nil {
		dstPackage = &packages.Package{Name: outPkgName}
	}

	noGenerate := cfg.NoGenerate

	wroteGoGenerate := false
	for _, fg := range fileGroups {
		outFileName := strings.TrimSuffix(fg.FileName, ".go") + TraceSuffix
		outFilePath := filepath.Join(outDir, outFileName)

		includeGoGenerate := !noGenerate && !wroteGoGenerate

		if err := gc.generateFileDecorators(sourcePackage, astPkg, dstPackage, headerTmpl, bodyTmpl, sharedFS, pkgCache, fg, outFilePath, includeGoGenerate, rp.Config.Interfaces); err != nil {
			return errors.Wrapf(err, "failed to generate for %s", fg.FileName)
		}

		if includeGoGenerate {
			wroteGoGenerate = true
		}
	}

	return nil
}
