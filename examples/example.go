package examples

import "context"

//go:generate ddtrace gen

type Speak interface {
	SayHello(ctx context.Context, name string) string
}

type Move interface {
	Walk(ctx context.Context, distance int) string
}
