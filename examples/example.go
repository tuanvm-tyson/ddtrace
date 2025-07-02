package examples

import "context"

//go:generate ddtrace gen -i Speak,Move  -o trace_example.go

type Speak interface {
	SayHello(ctx context.Context, name string) string
}

type Move interface {
	Walk(ctx context.Context, distance int) string
}
