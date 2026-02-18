package generate

import (
	"regexp"
	"strings"
	"text/template"
	"unicode"

	"github.com/Masterminds/sprig/v3"
)

const (
	// TraceSuffix is the file suffix for generated trace files.
	TraceSuffix = "_trace.go"

	// TestSuffix is the file suffix for Go test files.
	TestSuffix = "_test.go"
)

// minimalHeaderTemplate is used when generating individual interface bodies.
const minimalHeaderTemplate = `package {{.Package.Name}}

import(
{{range $import := .Options.Imports}}	{{$import}}
{{end}})
`

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
