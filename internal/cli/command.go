package cli

import (
	"flag"
	"io"
)

// Command interface represents ddtrace subcommand
type Command interface {
	// FlagSet returns command specific flag set. If command doesn't have any flags nil should be returned.
	FlagSet() *flag.FlagSet

	// Run runs command
	Run(args []string, stdout io.Writer) error

	// ShortDescription returns short description of a command that is shown in the help message
	ShortDescription() string

	UsageLine() string
	HelpMessage(w io.Writer) error
}

// BaseCommand implements Command interface
type BaseCommand struct {
	Flags *flag.FlagSet
	Short string
	Usage string
	Help  string
}

// ShortDescription implements Command
func (b BaseCommand) ShortDescription() string {
	return b.Short
}

// UsageLine implements Command
func (b BaseCommand) UsageLine() string {
	return b.Usage
}

// FlagSet implements Command
func (b BaseCommand) FlagSet() *flag.FlagSet {
	return b.Flags
}

// HelpMessage implements Command
func (b BaseCommand) HelpMessage(w io.Writer) error {
	_, err := w.Write([]byte(b.Help))
	return err
}
