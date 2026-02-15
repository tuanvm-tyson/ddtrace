package ddtrace

import (
	"bytes"
	"flag"
	"fmt"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"text/template"
	"time"
	"unicode"

	"github.com/Masterminds/sprig/v3"
	"github.com/pkg/errors"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/imports"

	"github.com/tuanvm-tyson/ddtrace/generator"
	"github.com/tuanvm-tyson/ddtrace/pkg"
)

// GenerateCommand implements Command interface
type GenerateCommand struct {
	BaseCommand

	sourcePkg       string
	outputDir       string
	configPath      string
	noGenerate      bool
	forceRegenerate bool

	filepath fileSystem
}

type fileSystem struct {
	WriteFile func(string, []byte, os.FileMode) error
	MkdirAll  func(string, os.FileMode) error
}

// NewGenerateCommand creates GenerateCommand
func NewGenerateCommand() *GenerateCommand {
	gc := &GenerateCommand{
		filepath: fileSystem{
			WriteFile: os.WriteFile,
			MkdirAll:  os.MkdirAll,
		},
	}

	fs := &flag.FlagSet{}
	fs.StringVar(&gc.sourcePkg, "p", "", `source package path (default: "./")`)
	fs.StringVar(&gc.outputDir, "o", "", `output directory for generated files (default: "./trace")`)
	fs.StringVar(&gc.configPath, "config", "", `path to .ddtrace.yaml config file (auto-detected if omitted)`)
	fs.BoolVar(&gc.noGenerate, "g", false, "don't put //go:generate instruction to the generated code")
	fs.BoolVar(&gc.forceRegenerate, "force", false, "regenerate all packages regardless of file modification times")

	gc.BaseCommand = BaseCommand{
		Short: "generate tracing decorators for all interfaces in a package",
		Usage: "[-p package] [-o output_dir] [-g] [--config path] [--force]",
		Flags: fs,
	}

	return gc
}

// Run implements Command interface
func (gc *GenerateCommand) Run(args []string, stdout io.Writer) error {
	if err := gc.FlagSet().Parse(args); err != nil {
		return CommandLineError(err.Error())
	}

	// Decide between config-driven and legacy single-package mode.
	// If -p is explicitly set, always use legacy mode.
	if gc.sourcePkg != "" {
		return gc.runSinglePackage()
	}

	// Try config-based generation.
	configPath := gc.configPath
	if configPath == "" {
		configPath = FindConfig()
	}
	if configPath != "" {
		cfg, err := LoadConfig(configPath)
		if err != nil {
			return err
		}
		return gc.runWithConfig(cfg, configPath, stdout)
	}

	// No config found, no -p flag: fall back to single-package mode with defaults.
	gc.sourcePkg = "./"
	return gc.runSinglePackage()
}

// runSinglePackage executes the legacy single-package generation flow.
func (gc *GenerateCommand) runSinglePackage() error {
	// Apply defaults
	if gc.outputDir == "" {
		gc.outputDir = "./trace"
	}

	// Load source package
	sourcePackage, err := pkg.Load(gc.sourcePkg)
	if err != nil {
		return errors.Wrap(err, "failed to load source package")
	}

	// Parse AST
	fset := token.NewFileSet()
	astPkg, err := pkg.AST(fset, sourcePackage)
	if err != nil {
		return errors.Wrap(err, "failed to parse source package AST")
	}

	// Scan for all interfaces, filter out //ddtrace:ignore
	fileGroups, err := pkg.ScanPackage(astPkg)
	if err != nil {
		return errors.Wrap(err, "failed to scan interfaces")
	}

	if len(fileGroups) == 0 {
		return nil // No interfaces found, not an error
	}

	// Resolve output directory relative to source package directory
	srcDir := pkg.Dir(sourcePackage)
	outDir := gc.outputDir
	if !filepath.IsAbs(outDir) {
		outDir = filepath.Join(srcDir, outDir)
	}

	if err := gc.filepath.MkdirAll(outDir, os.ModePerm); err != nil {
		return errors.Wrap(err, "failed to create output directory")
	}

	// Load destination package once (all output files share the same directory)
	outPkgName := filepath.Base(outDir)
	dstPackage, err := pkg.Load(outDir)
	if err != nil {
		// Destination package may not exist yet; fallback to output dir name.
		dstPackage = &packages.Package{Name: outPkgName}
	}

	// Pre-parse templates once (they are the same for every interface)
	headerTmpl, err := template.New("header").Funcs(helperFuncs).Parse(minimalHeaderTemplate)
	if err != nil {
		return errors.Wrap(err, "failed to parse header template")
	}
	bodyTmpl, err := template.New("body").Funcs(helperFuncs).Parse(datadogTemplate)
	if err != nil {
		return errors.Wrap(err, "failed to parse body template")
	}

	// Shared resources for all generators in this run
	sharedFS := token.NewFileSet()
	pkgCache := generator.NewPackageCache()

	// Generate one output file per source file
	wroteGoGenerate := false
	for _, fg := range fileGroups {
		outFileName := strings.TrimSuffix(fg.FileName, ".go") + "_trace.go"
		outFilePath := filepath.Join(outDir, outFileName)

		// Only write //go:generate in the first output file
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

// runWithConfig processes all packages defined in a .ddtrace.yaml config file.
func (gc *GenerateCommand) runWithConfig(cfg *Config, configPath string, stdout io.Writer) error {
	resolved, err := cfg.ResolvePackages()
	if err != nil {
		return errors.Wrap(err, "failed to resolve packages from config")
	}

	// --- Incremental generation: skip unchanged packages before loading ---
	toProcess := resolved
	if !gc.forceRegenerate {
		moduleRoot, modulePath, modErr := findModuleRoot(filepath.Dir(configPath))
		if modErr == nil {
			configTime := fileModTime(configPath)

			// Determine the last successful run time by finding the newest
			// _trace.go file across all packages. This lets us skip packages
			// that were checked before and had no interfaces.
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

			var filtered []ResolvedPackage
			for _, rp := range resolved {
				srcDir := importPathToDir(moduleRoot, modulePath, rp.ImportPath)
				if srcDir == "" {
					// External package or can't determine dir; always process.
					filtered = append(filtered, rp)
					continue
				}
				outDir := filepath.Join(srcDir, rp.Config.Output)
				newestOutput := newestTraceModTime(outDir)

				if !newestOutput.IsZero() {
					// Has output: regenerate only if source or config changed.
					if sourceNewerThan(srcDir, newestOutput) ||
						configTime.After(newestOutput) {
						filtered = append(filtered, rp)
					}
					continue
				}

				// No output directory: either first run or package had no
				// interfaces last time. Include if this is the first run,
				// if source was added/modified since the last run, or if
				// the config changed since the last run.
				if lastRunTime.IsZero() ||
					sourceNewerThan(srcDir, lastRunTime) ||
					configTime.After(lastRunTime) {
					filtered = append(filtered, rp)
				}
			}
			toProcess = filtered
		}
		// If findModuleRoot fails, fall through and process everything.
	}

	if len(toProcess) == 0 {
		return nil // nothing changed
	}

	// Collect import paths for batch loading.
	importPaths := make([]string, len(toProcess))
	for i, rp := range toProcess {
		importPaths[i] = rp.ImportPath
	}

	// Batch-load source packages in a single packages.Load call.
	// The Go toolchain resolves the shared dependency graph once.
	pkgMap, err := pkg.LoadAll(importPaths)
	if err != nil {
		return errors.Wrap(err, "failed to batch-load source packages")
	}

	// Shared resources across ALL packages in this run.
	// token.FileSet is documented as safe for concurrent use.
	sharedFS := token.NewFileSet()
	pkgCache := generator.NewPackageCache()
	pkgCache.Seed(pkgMap) // pre-populate cache with batch-loaded packages

	headerTmpl, err := template.New("header").Funcs(helperFuncs).Parse(minimalHeaderTemplate)
	if err != nil {
		return errors.Wrap(err, "failed to parse header template")
	}
	bodyTmpl, err := template.New("body").Funcs(helperFuncs).Parse(datadogTemplate)
	if err != nil {
		return errors.Wrap(err, "failed to parse body template")
	}

	// Process packages in parallel.
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
			continue // package had load errors, skip
		}

		// Skip packages with no Go files (e.g. test-only / mock packages)
		if len(sourcePkg.GoFiles) == 0 && len(sourcePkg.CompiledGoFiles) == 0 {
			continue
		}

		wg.Add(1)
		go func(rp ResolvedPackage, sourcePkg *packages.Package) {
			defer wg.Done()
			sem <- struct{}{}        // acquire worker slot
			defer func() { <-sem }() // release worker slot

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
// sourcePackage must be pre-loaded (e.g. via batch loading).
func (gc *GenerateCommand) processPackage(
	rp ResolvedPackage,
	cfg *Config,
	sourcePackage *packages.Package,
	headerTmpl *template.Template,
	bodyTmpl *template.Template,
	sharedFS *token.FileSet,
	pkgCache *generator.PackageCache,
) error {
	// Parse AST
	astPkg, err := pkg.AST(sharedFS, sourcePackage)
	if err != nil {
		return errors.Wrap(err, "failed to parse source package AST")
	}

	// Scan for all interfaces
	fileGroups, err := pkg.ScanPackage(astPkg)
	if err != nil {
		return errors.Wrap(err, "failed to scan interfaces")
	}

	if len(fileGroups) == 0 {
		return nil
	}

	// Filter interfaces by config
	fileGroups = filterInterfaces(fileGroups, rp.Config)

	if len(fileGroups) == 0 {
		return nil
	}

	// Resolve output directory
	srcDir := pkg.Dir(sourcePackage)
	outDir := rp.Config.Output
	if !filepath.IsAbs(outDir) {
		outDir = filepath.Join(srcDir, outDir)
	}

	if err := gc.filepath.MkdirAll(outDir, os.ModePerm); err != nil {
		return errors.Wrap(err, "failed to create output directory")
	}

	// Load destination package
	outPkgName := filepath.Base(outDir)
	dstPackage, err := pkg.Load(outDir)
	if err != nil {
		dstPackage = &packages.Package{Name: outPkgName}
	}

	noGenerate := cfg.NoGenerate

	// Generate one output file per source file
	wroteGoGenerate := false
	for _, fg := range fileGroups {
		outFileName := strings.TrimSuffix(fg.FileName, ".go") + "_trace.go"
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

// filterInterfaces applies interface-level include/ignore rules from config.
func filterInterfaces(fileGroups []pkg.FileInterfaces, pkgCfg PackageConfig) []pkg.FileInterfaces {
	if len(pkgCfg.Interfaces) == 0 {
		return fileGroups // No filtering: include all
	}

	var result []pkg.FileInterfaces
	for _, fg := range fileGroups {
		var filtered []pkg.InterfaceInfo
		for _, iface := range fg.Interfaces {
			ifaceCfg, explicit := pkgCfg.Interfaces[iface.Name]
			if explicit && ifaceCfg != nil && ifaceCfg.Ignore {
				continue // skip ignored interfaces
			}
			filtered = append(filtered, iface)
		}
		if len(filtered) > 0 {
			result = append(result, pkg.FileInterfaces{
				FileName:   fg.FileName,
				Interfaces: filtered,
			})
		}
	}
	return result
}

// generateFileDecorators generates tracing decorators for all interfaces in a single source file.
// interfaceConfigs is optional (nil in legacy mode); when set, per-interface overrides are applied.
func (gc *GenerateCommand) generateFileDecorators(
	sourcePackage *packages.Package,
	sourcePackageAST *pkg.Package,
	dstPackage *packages.Package,
	headerTmpl *template.Template,
	bodyTmpl *template.Template,
	sharedFS *token.FileSet,
	pkgCache *generator.PackageCache,
	fg pkg.FileInterfaces,
	outFilePath string,
	includeGoGenerate bool,
	interfaceConfigs map[string]*InterfaceConfig,
) error {
	var buf bytes.Buffer

	// Determine output package name from the output directory name
	outPkgName := filepath.Base(filepath.Dir(outFilePath))

	// Write file header
	fmt.Fprintf(&buf, "// Code generated by ddtrace. DO NOT EDIT.\n")
	fmt.Fprintf(&buf, "// source: %s\n", fg.FileName)
	fmt.Fprintf(&buf, "// ddtrace: http://github.com/tuanvm-tyson/ddtrace\n\n")
	fmt.Fprintf(&buf, "package %s\n\n", outPkgName)

	if includeGoGenerate {
		fmt.Fprintf(&buf, "//go:generate ddtrace gen -p %s -o %s\n\n", sourcePackage.PkgPath, gc.outputDir)
	}

	// Generate each interface through the existing generator engine.
	// The first interface's output includes imports (with the aliased source package import).
	// Subsequent interfaces only contribute their struct/method bodies.
	generatedAny := false
	for _, iface := range fg.Interfaces {
		// Build per-interface template vars from config
		vars := make(map[string]interface{})
		if interfaceConfigs != nil {
			if ic, ok := interfaceConfigs[iface.Name]; ok && ic != nil {
				if ic.DecoratorName != "" {
					vars["DecoratorName"] = ic.DecoratorName
				}
				if ic.SpanPrefix != "" {
					vars["SpanNamePrefix"] = ic.SpanPrefix
				}
			}
		}

		genOutput, err := gc.generateInterfaceOutput(sourcePackage, sourcePackageAST, dstPackage, headerTmpl, bodyTmpl, sharedFS, pkgCache, iface.Name, outFilePath, vars)
		if err != nil {
			// Skip interfaces that cannot be generated (e.g., empty interfaces)
			continue
		}

		if !generatedAny {
			// First interface: include imports + body (strip only the package line)
			buf.WriteString(extractAfterPackage(genOutput))
		} else {
			// Subsequent interfaces: body only (strip package + imports)
			buf.WriteString(extractBodyOnly(genOutput))
		}
		buf.WriteString("\n")
		generatedAny = true
	}

	if !generatedAny {
		return nil // All interfaces were empty or had issues, skip this file
	}

	// Format with goimports to merge/clean imports and format code
	processed, err := imports.Process(outFilePath, buf.Bytes(), nil)
	if err != nil {
		return errors.Wrapf(err, "failed to format generated code:\n%s", buf.String())
	}

	// Skip write if output file already has identical content (avoids
	// unnecessary rebuilds and file-system churn on repeated runs).
	// Touch the mtime so incremental detection knows this output is current.
	if existing, err := os.ReadFile(outFilePath); err == nil && bytes.Equal(existing, processed) {
		now := time.Now()
		os.Chtimes(outFilePath, now, now) //nolint: errcheck
		return nil
	}

	return gc.filepath.WriteFile(outFilePath, processed, 0664)
}

// generateInterfaceOutput uses the existing generator engine to produce a complete
// formatted Go file for a single interface.
func (gc *GenerateCommand) generateInterfaceOutput(
	sourcePackage *packages.Package,
	sourcePackageAST *pkg.Package,
	destinationPackage *packages.Package,
	headerTmpl *template.Template,
	bodyTmpl *template.Template,
	sharedFS *token.FileSet,
	pkgCache *generator.PackageCache,
	interfaceName string,
	outFilePath string,
	vars map[string]interface{},
) (string, error) {
	if vars == nil {
		vars = make(map[string]interface{})
	}

	options := generator.Options{
		InterfaceName:              interfaceName,
		OutputFile:                 outFilePath,
		SourcePackage:              sourcePackage.PkgPath,
		SourcePackageInstance:      sourcePackage,
		SourcePackageAST:           sourcePackageAST,
		DestinationPackageInstance: destinationPackage,
		SkipImportsProcessing:      true,
		HeaderTemplateParsed:       headerTmpl,
		BodyTemplateParsed:         bodyTmpl,
		FileSet:                    sharedFS,
		PackageCache:               pkgCache,
		Funcs:                      helperFuncs,
		Vars:                       vars,
		HeaderVars:                 make(map[string]interface{}),
	}

	gen, err := generator.NewGenerator(options)
	if err != nil {
		return "", err
	}

	var genBuf bytes.Buffer
	if err := gen.Generate(&genBuf); err != nil {
		return "", err
	}

	return genBuf.String(), nil
}

// extractAfterPackage strips the package declaration line (and preceding comments)
// from generated Go source, keeping imports and all code.
func extractAfterPackage(content string) string {
	lines := strings.Split(content, "\n")
	foundPackage := false

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !foundPackage && strings.HasPrefix(trimmed, "package ") {
			foundPackage = true
			// Return everything after the package line
			rest := strings.Join(lines[i+1:], "\n")
			return strings.TrimLeft(rest, "\n")
		}
	}

	return content
}

// extractBodyOnly strips the package declaration, imports, and leading comments
// from generated Go source, returning only struct and method definitions.
func extractBodyOnly(content string) string {
	lines := strings.Split(content, "\n")
	var bodyLines []string
	inImportBlock := false
	foundBody := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Handle multi-line import block
		if !foundBody && (strings.HasPrefix(trimmed, "import (") || trimmed == "import(") {
			inImportBlock = true
			continue
		}
		if !foundBody && strings.HasPrefix(trimmed, "import ") && !strings.Contains(trimmed, "(") {
			continue
		}
		if inImportBlock {
			if trimmed == ")" {
				inImportBlock = false
			}
			continue
		}

		// Skip package declaration and comments before body
		if !foundBody {
			if trimmed == "" || strings.HasPrefix(trimmed, "package ") || strings.HasPrefix(trimmed, "//") {
				continue
			}
			foundBody = true
		}

		bodyLines = append(bodyLines, line)
	}

	// Trim trailing empty lines
	for len(bodyLines) > 0 && strings.TrimSpace(bodyLines[len(bodyLines)-1]) == "" {
		bodyLines = bodyLines[:len(bodyLines)-1]
	}

	return strings.Join(bodyLines, "\n")
}

// ---------------------------------------------------------------------------
// Incremental generation helpers
// ---------------------------------------------------------------------------

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
			break // reached filesystem root
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
// files in dir. Returns zero time if the directory doesn't exist or has no
// trace files.
func newestTraceModTime(dir string) time.Time {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return time.Time{}
	}
	var newest time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_trace.go") {
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
		return true // can't read → assume changed
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
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

// minimalHeaderTemplate is used when generating individual interface bodies.
// It includes the package clause and source package imports so cross-package
// type references (e.g., _sourcePkg.InterfaceName) resolve correctly.
const minimalHeaderTemplate = `package {{.Package.Name}}

import(
{{range $import := .Options.Imports}}	{{$import}}
{{end}})
`

var helperFuncs template.FuncMap

func init() {
	helperFuncs = sprig.TxtFuncMap()

	helperFuncs["up"] = strings.ToUpper
	helperFuncs["down"] = strings.ToLower
	helperFuncs["upFirst"] = upFirst
	helperFuncs["downFirst"] = downFirst
	helperFuncs["replace"] = strings.ReplaceAll
	helperFuncs["snake"] = toSnakeCase
}

func upFirst(s string) string {
	for _, v := range s {
		return string(unicode.ToUpper(v)) + s[len(string(v)):]
	}
	return ""
}

func downFirst(s string) string {
	for _, v := range s {
		return string(unicode.ToLower(v)) + s[len(string(v)):]
	}
	return ""
}

var matchFirstCap = regexp.MustCompile("(.)([A-Z][a-z]+)")
var matchAllCap = regexp.MustCompile("([a-z0-9])([A-Z])")

func toSnakeCase(str string) string {
	result := matchFirstCap.ReplaceAllString(str, "${1}_${2}")
	result = matchAllCap.ReplaceAllString(result, "${1}_${2}")
	return strings.ToLower(result)
}

const datadogTemplate = `import (
    "context"

    "github.com/tuanvm-tyson/ddtrace/tracing"
)

{{ $decorator := (or .Vars.DecoratorName (printf "%sWithTracing" .Interface.Name)) }}
{{ $spanNameType := (or .Vars.SpanNamePrefix .Interface.Name) }}

// {{$decorator}} implements {{.Interface.Name}} interface instrumented with Datadog tracing
type {{$decorator}} struct {
  {{.Interface.Type}}
  _cfg tracing.TracingConfig
}

// New{{$decorator}} returns {{$decorator}}
func New{{$decorator}} (base {{.Interface.Type}}, opts ...tracing.TracingOption) {{$decorator}} {
  return {{$decorator}} {
    {{.Interface.Name}}: base,
    _cfg: tracing.NewTracingConfig(opts...),
  }
}

{{range $method := .Interface.Methods}}
  {{if $method.AcceptsContext}}
    // {{$method.Name}} implements {{$.Interface.Name}}
func (_d {{$decorator}}) {{$method.Declaration}} {
  span, ctx := _d._cfg.StartSpan(ctx, "{{$spanNameType}}.{{$method.Name}}")
  defer func() {
    _d._cfg.FinishSpan(span, {{if $method.ReturnsError}}err{{else}}nil{{end}}, {{$method.ParamsMap}}, {{$method.ResultsMap}})
  }()
  {{$method.Pass (printf "_d.%s." $.Interface.Name) }}
}
  {{end}}
{{end}}
`
