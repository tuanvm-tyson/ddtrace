package cli

import (
	"flag"
	"io"
	"text/template"
)

var version string = "dev"

var commands = map[string]Command{}

// RegisterCommand adds command to the global Commands map
func RegisterCommand(name string, cmd Command) {
	commands[name] = cmd
	if fs := cmd.FlagSet(); fs != nil {
		fs.Init("", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
	}
}

// GetCommand returns command from the global Commands map
func GetCommand(name string) Command {
	return commands[name]
}

// Usage writes ddtrace usage message to w
func Usage(w io.Writer) error {
	return usageTemplate.Execute(w, struct {
		Commands map[string]Command
		Version  string
	}{commands, version})
}

var usageTemplate = template.Must(template.New("usage").Parse(`ddtrace({{.Version}}) is a tool for generating decorators for the Go interfaces

Usage:

	ddtrace command [arguments]

The commands are:
{{ range $name, $cmd := .Commands }}
	{{ printf "%-10s" $name }}{{ $cmd.ShortDescription }}
{{ end }}
Use "ddtrace help [command]" for more information about a command.
`))
