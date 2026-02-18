package generate

import (
	"flag"
	"io"
	"os"

	"github.com/tuanvm-tyson/ddtrace/internal/cli"
	"github.com/tuanvm-tyson/ddtrace/internal/config"
)

// GenerateCommand implements cli.Command interface
type GenerateCommand struct {
	cli.BaseCommand

	sourcePkg       string
	outputDir       string
	configPath      string
	noGenerate      bool
	forceRegenerate bool

	fs fileSystem
}

type fileSystem struct {
	WriteFile func(string, []byte, os.FileMode) error
	MkdirAll  func(string, os.FileMode) error
}

// NewGenerateCommand creates GenerateCommand
func NewGenerateCommand() *GenerateCommand {
	gc := &GenerateCommand{
		fs: fileSystem{
			WriteFile: os.WriteFile,
			MkdirAll:  os.MkdirAll,
		},
	}

	flags := &flag.FlagSet{}
	flags.StringVar(&gc.sourcePkg, "p", "", `source package path (default: "./")`)
	flags.StringVar(&gc.outputDir, "o", "", `output directory for generated files (default: "./trace")`)
	flags.StringVar(&gc.configPath, "config", "", `path to .ddtrace.yaml config file (auto-detected if omitted)`)
	flags.BoolVar(&gc.noGenerate, "g", false, "don't put //go:generate instruction to the generated code")
	flags.BoolVar(&gc.forceRegenerate, "force", false, "regenerate all packages regardless of file modification times")

	gc.BaseCommand = cli.BaseCommand{
		Short: "generate tracing decorators for all interfaces in a package",
		Usage: "[-p package] [-o output_dir] [-g] [--config path] [--force]",
		Flags: flags,
	}

	return gc
}

// Run implements cli.Command interface
func (gc *GenerateCommand) Run(args []string, stdout io.Writer) error {
	if err := gc.FlagSet().Parse(args); err != nil {
		return cli.CommandLineError(err.Error())
	}

	if gc.sourcePkg != "" {
		return gc.runSinglePackage()
	}

	configPath := gc.configPath
	if configPath == "" {
		configPath = config.Find()
	}
	if configPath != "" {
		cfg, err := config.Load(configPath)
		if err != nil {
			return err
		}
		return gc.runWithConfig(cfg, configPath, stdout)
	}

	gc.sourcePkg = "./"
	return gc.runSinglePackage()
}
