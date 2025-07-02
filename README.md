# DDTrace
DDTrace is a command line tool that generates DataDog tracing decorators for Go interface types using simple templates.
With DDTrace ddtrace you can easily add DataDog tracing instrumentation to your existing code in a few seconds.

## Installation
### CLI
```
go install github.com/tyson-tuanvm/ddtrace/cmd/ddtrace@latest
```
### As module
```
go get -u github.com/tyson-tuanvm/ddtrace/cmd/ddtrace
```

## Usage of ddtraceß

```
Usage: ddtrace gen -p package -i interfaceName -o output_file.go
  -g	don't put //go:generate instruction into the generated code
  -i string
    	the source interface name(s), i.e. "Reader" or "Reader,Writer,Closer" for multiple interfaces
  -o string
    	the output file name
  -p string
    	the source package import path, i.e. "io", "github.com/tyson-tuanvm/ddtrace" or
    	a relative import path like "./generator"
  -v value
    	a key-value pair to parametrize the template,
    	arguments without an equal sign are treated as a bool values,
    	i.e. -v DecoratorName=MyDecorator -v disableChecks
```

This will generate an implementation of the io.Reader interface wrapped with DataDog tracing

```
  $ ddtrace gen -p io -i Reader -o reader_with_tracing.go
```

This will generate implementations for multiple interfaces (Reader, Writer, and Closer) with DataDog tracing in a single file:

```
  $ ddtrace gen -p io -i Reader,Writer,Closer -o io_with_tracing.go
```

This will generate a DataDog tracing decorator for the Connector interface that can be found in the ./connector subpackage:

```
  $ ddtrace gen -p ./connector -i Connector -o ./connector/with_tracing.go
```

Run `ddtrace help` for more options

## DataDog Template

The built-in DataDog template instruments your interfaces with DataDog tracing spans. It automatically:

- Creates spans for each method call
- Sets service name tags
- Handles context propagation
- Reports errors to DataDog
- Supports custom span decorators

The generated code uses the official DataDog Go tracing library (`gopkg.in/DataDog/dd-trace-go.v1/ddtrace`).

