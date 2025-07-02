package examples

import "context"

//go:generate gowrap gen -i Speak  -o trace_example.go
//go:generate gowrap gen -i Move  -o trace_example.go

type Speak interface {
	SayHello(ctx context.Context, name string) string
}

type Move interface {
	Walk(ctx context.Context, distance int) string
}
