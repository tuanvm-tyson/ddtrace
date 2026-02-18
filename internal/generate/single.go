package generate

import (
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/pkg/errors"
	"golang.org/x/tools/go/packages"

	"github.com/moneyforward/ddtrace/internal/codegen"
	"github.com/moneyforward/ddtrace/internal/scanner"
)

// runSinglePackage executes the legacy single-package generation flow.
func (gc *GenerateCommand) runSinglePackage() error {
	if gc.outputDir == "" {
		gc.outputDir = "./trace"
	}

	sourcePackage, err := scanner.Load(gc.sourcePkg)
	if err != nil {
		return errors.Wrap(err, "failed to load source package")
	}

	fset := token.NewFileSet()
	astPkg, err := scanner.AST(fset, sourcePackage)
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

	srcDir := scanner.Dir(sourcePackage)
	outDir := gc.outputDir
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

	headerTmpl, err := template.New("header").Funcs(helperFuncs).Parse(minimalHeaderTemplate)
	if err != nil {
		return errors.Wrap(err, "failed to parse header template")
	}
	bodyTmpl, err := template.New("body").Funcs(helperFuncs).Parse(datadogTemplate)
	if err != nil {
		return errors.Wrap(err, "failed to parse body template")
	}

	sharedFS := token.NewFileSet()
	pkgCache := codegen.NewPackageCache()

	wroteGoGenerate := false
	for _, fg := range fileGroups {
		outFileName := strings.TrimSuffix(fg.FileName, ".go") + TraceSuffix
		outFilePath := filepath.Join(outDir, outFileName)

		includeGoGenerate := !gc.noGenerate && !wroteGoGenerate

		if err := gc.generateFileDecorators(sourcePackage, astPkg, dstPackage, headerTmpl, bodyTmpl, sharedFS, pkgCache, fg, outFilePath, includeGoGenerate, nil); err != nil {
			return errors.Wrapf(err, "failed to generate for %s", fg.FileName)
		}

		if includeGoGenerate {
			wroteGoGenerate = true
		}
	}

	return nil
}
