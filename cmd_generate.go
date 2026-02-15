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
	"strings"
	"text/template"
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

	sourcePkg  string
	outputDir  string
	configPath string
	noGenerate bool

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

	gc.BaseCommand = BaseCommand{
		Short: "generate tracing decorators for all interfaces in a package",
		Usage: "[-p package] [-o output_dir] [-g] [--config path]",
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
		return gc.runWithConfig(cfg, stdout)
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
func (gc *GenerateCommand) runWithConfig(cfg *Config, stdout io.Writer) error {
	resolved, err := cfg.ResolvePackages()
	if err != nil {
		return errors.Wrap(err, "failed to resolve packages from config")
	}

	// Shared resources across ALL packages in this run
	sharedFS := token.NewFileSet()
	pkgCache := generator.NewPackageCache()

	headerTmpl, err := template.New("header").Funcs(helperFuncs).Parse(minimalHeaderTemplate)
	if err != nil {
		return errors.Wrap(err, "failed to parse header template")
	}
	bodyTmpl, err := template.New("body").Funcs(helperFuncs).Parse(datadogTemplate)
	if err != nil {
		return errors.Wrap(err, "failed to parse body template")
	}

	for _, rp := range resolved {
		if err := gc.processPackage(rp, cfg, headerTmpl, bodyTmpl, sharedFS, pkgCache); err != nil {
			return errors.Wrapf(err, "failed to generate for package %s", rp.ImportPath)
		}
	}

	return nil
}

// processPackage generates tracing decorators for a single package from config.
func (gc *GenerateCommand) processPackage(
	rp ResolvedPackage,
	cfg *Config,
	headerTmpl *template.Template,
	bodyTmpl *template.Template,
	sharedFS *token.FileSet,
	pkgCache *generator.PackageCache,
) error {
	// Load source package
	sourcePackage, err := pkg.Load(rp.ImportPath)
	if err != nil {
		return errors.Wrap(err, "failed to load source package")
	}

	// Skip packages with no Go files (e.g. test-only / mock packages discovered via "..." pattern)
	if len(sourcePackage.GoFiles) == 0 && len(sourcePackage.CompiledGoFiles) == 0 {
		return nil
	}

	// Parse AST
	fset := token.NewFileSet()
	astPkg, err := pkg.AST(fset, sourcePackage)
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
	if existing, err := os.ReadFile(outFilePath); err == nil && bytes.Equal(existing, processed) {
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
